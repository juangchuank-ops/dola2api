#!/usr/bin/env node
/**
 * Real-browser render check over the Chrome DevTools Protocol.
 *
 * The route and field checks talk HTTP, so they cannot see a React render
 * crash, an unhandled rejection or a silently blank page. This script drives a
 * headless Chrome, walks every console route, and fails if a page throws,
 * renders an error boundary, or produces an empty shell.
 *
 * Start Chrome first:
 *   chrome --headless=new --disable-gpu --remote-debugging-port=9222 \
 *          --user-data-dir=<temp dir> about:blank
 *
 * Usage:
 *   node tools/render.mjs [base] [cdp] [password] [screenshot-dir]
 */

import fs from "node:fs";
import path from "node:path";

const BASE = (process.argv[2] || "http://127.0.0.1:18080").replace(/\/$/, "");
const CDP = (process.argv[3] || "http://127.0.0.1:9222").replace(/\/$/, "");
const PASSWORD = process.argv[4] || "admin12345";
const SHOT_DIR = process.argv[5] || "";
const USERNAME = "admin";
const TOKEN_KEY = "dola2api:admin-token";

// [route, text that must appear] - asserting the heading catches a route that
// renders but mounts the wrong component.
const ROUTES = [
  ["/dashboard", "仪表盘"],
  ["/accounts", "号池管理"],
  ["/client-keys", "客户端密钥"],
  ["/models", "模型"],
  ["/gallery", "生成画廊"],
  ["/request-audits", "请求审计"],
  ["/docs/chat/completions", "接口文档"],
  ["/settings", "运行时设置"],
];

const problems = [];
const entries = [];

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function newTarget() {
  const res = await fetch(`${CDP}/json/new?about:blank`, { method: "PUT" });
  if (!res.ok) throw new Error(`cannot create target: HTTP ${res.status}`);
  return res.json();
}

async function attach(wsUrl) {
  const ws = new WebSocket(wsUrl);
  await new Promise((resolve, reject) => {
    ws.addEventListener("open", resolve, { once: true });
    ws.addEventListener("error", () => reject(new Error("websocket failed")), {
      once: true,
    });
  });

  let nextId = 0;
  const pending = new Map();
  const listeners = [];

  ws.addEventListener("message", (event) => {
    const msg = JSON.parse(event.data);
    if (msg.id !== undefined && pending.has(msg.id)) {
      const { resolve, reject } = pending.get(msg.id);
      pending.delete(msg.id);
      if (msg.error) reject(new Error(JSON.stringify(msg.error)));
      else resolve(msg.result);
      return;
    }
    if (msg.method) for (const fn of listeners) fn(msg);
  });

  return {
    send(method, params = {}) {
      const id = ++nextId;
      return new Promise((resolve, reject) => {
        pending.set(id, { resolve, reject });
        ws.send(JSON.stringify({ id, method, params }));
      });
    },
    on(fn) {
      listeners.push(fn);
    },
    close() {
      ws.close();
    },
  };
}

async function evaluate(session, expression) {
  const { result, exceptionDetails } = await session.send("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (exceptionDetails) {
    throw new Error(exceptionDetails.exception?.description || exceptionDetails.text);
  }
  return result.value;
}

async function navigate(session, url) {
  await session.send("Page.navigate", { url });
  for (let i = 0; i < 60; i += 1) {
    await sleep(100);
    const state = await evaluate(session, "document.readyState").catch(() => "");
    if (state === "complete") break;
  }
  // Let React mount and the initial data queries settle.
  await sleep(1100);
}

const INSPECT = `(() => {
  const root = document.getElementById("root");
  const text = document.body.innerText || "";
  return {
    path: location.pathname,
    rootChildren: root ? root.children.length : -1,
    textLength: text.length,
    text: text.replace(/\\s+/g, " ").trim().slice(0, 400),
    errorBoundary: /Unexpected Application Error|Something went wrong|渲染出错/i.test(text),
    headings: Array.from(document.querySelectorAll("h1, h2"))
      .map((el) => (el.innerText || "").trim())
      .filter(Boolean)
      .slice(0, 3),
  };
})()`;

async function checkPage(session, label, route, opts = {}) {
  const before = entries.length;
  await navigate(session, BASE + route);

  let info;
  try {
    info = await evaluate(session, INSPECT);
  } catch (error) {
    problems.push(`${label}: cannot inspect DOM (${error.message})`);
    console.log(`  [FAIL] ${label.padEnd(16)} ${route}  -> DOM unreachable`);
    return;
  }

  const fresh = entries.slice(before);
  const failed = [];
  const haystack = `${info.text} ${info.headings.join(" ")}`;
  if (info.errorBoundary) failed.push("error boundary");
  if (info.rootChildren <= 0) failed.push("empty #root");
  if (info.textLength < 40) failed.push(`thin content (${info.textLength} chars)`);
  if (opts.expectText && !new RegExp(opts.expectText, "i").test(haystack)) {
    failed.push(`missing expected text /${opts.expectText}/ (saw "${info.text.slice(0, 80)}")`);
  }
  if (opts.expectPath && info.path !== opts.expectPath) {
    failed.push(`expected path ${opts.expectPath}, got ${info.path}`);
  }
  if (fresh.length) failed.push(`${fresh.length} console error(s)`);

  if (failed.length) {
    problems.push(`${label}: ${failed.join(", ")}`);
    console.log(`  [FAIL] ${label.padEnd(16)} ${route}  -> ${failed.join(", ")}`);
    for (const line of fresh.slice(0, 4)) console.log(`         ${line}`);
  } else {
    const heading = info.headings[0] ? ` "${info.headings[0]}"` : "";
    console.log(
      `  [ok] ${label.padEnd(16)} ${route}  ${info.textLength} chars${heading}`,
    );
  }

  if (SHOT_DIR) {
    // Name shots after the label, not the route: /login is visited twice
    // (anonymous and guarded) and would otherwise overwrite itself.
    const name = label.replace(/[^a-zA-Z0-9]+/g, "-").replace(/^-|-$/g, "").toLowerCase();
    const file = path.join(SHOT_DIR, `${name}.png`);
    const shot = await session
      .send("Page.captureScreenshot", { format: "png" })
      .catch(() => null);
    if (shot?.data) {
      fs.writeFileSync(file, Buffer.from(shot.data, "base64"));
      console.log(`         saved ${file}`);
    }
  }
}

async function main() {
  console.log("Dola2API real-browser render check");
  console.log(`base = ${BASE}`);
  console.log(`cdp  = ${CDP}`);
  if (SHOT_DIR) console.log(`shots = ${SHOT_DIR}`);
  console.log();

  if (SHOT_DIR) fs.mkdirSync(SHOT_DIR, { recursive: true });

  let target;
  try {
    target = await newTarget();
  } catch (error) {
    console.log(`cannot reach Chrome DevTools at ${CDP}: ${error.message}`);
    console.log("start it with: chrome --headless=new --remote-debugging-port=9222 ...");
    return 1;
  }

  const session = await attach(target.webSocketDebuggerUrl);

  session.on((msg) => {
    if (msg.method === "Runtime.exceptionThrown") {
      const d = msg.params.exceptionDetails;
      entries.push(`[exception] ${d.exception?.description || d.text}`.split("\n")[0]);
    } else if (msg.method === "Runtime.consoleAPICalled" && msg.params.type === "error") {
      const text = msg.params.args
        .map((a) => a.value ?? a.description ?? "")
        .join(" ")
        .split("\n")[0];
      entries.push(`[console.error] ${text}`);
    } else if (msg.method === "Log.entryAdded" && msg.params.entry.level === "error") {
      entries.push(`[log] ${msg.params.entry.text}`.split("\n")[0]);
    }
  });

  await session.send("Runtime.enable");
  await session.send("Log.enable");
  await session.send("Page.enable");
  // Always fetch fresh assets: a cached index.html keeps pointing at the
  // previous hashed bundle and silently invalidates the whole check.
  await session.send("Network.enable");
  await session.send("Network.setCacheDisabled", { cacheDisabled: true });
  await session.send("Emulation.setDeviceMetricsOverride", {
    width: 1440,
    height: 900,
    deviceScaleFactor: 1,
    mobile: false,
  });

  console.log("public surface");
  // Establish the origin first so localStorage is reachable, then drop any
  // token left over from a previous run - otherwise the auth guard redirects
  // /login straight to the console and the login page never renders.
  await navigate(session, BASE + "/login");
  await evaluate(session, `localStorage.clear(); "ok"`);
  await checkPage(session, "login page", "/login", { expectText: "管理员登录" });

  console.log("\nsigning in");
  const token = await evaluate(
    session,
    `(async () => {
      const res = await fetch("/admin/api/auth/login", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ username: ${JSON.stringify(USERNAME)}, password: ${JSON.stringify(PASSWORD)} }),
      });
      const data = await res.json();
      return data.token || "";
    })()`,
  );
  if (!token) {
    console.log("  [FAIL] could not obtain an admin token");
    problems.push("login via page fetch failed");
  } else {
    console.log(`  [ok] token acquired (${token.slice(0, 12)}...)`);
    await evaluate(
      session,
      `localStorage.setItem(${JSON.stringify(TOKEN_KEY)}, ${JSON.stringify(token)}); "ok"`,
    );
    // With a token present the guard must bounce /login to the console.
    await checkPage(session, "login guard", "/login", {
      expectPath: "/dashboard",
      expectText: "仪表盘",
    });
  }

  console.log("\nconsole routes");
  for (const [route, expect] of ROUTES) {
    await checkPage(session, route.replace(/^\//, ""), route, { expectText: expect });
  }

  console.log("\nroot redirect");
  await checkPage(session, "root -> dashboard", "/", { expectText: "仪表盘" });

  await session.send("Page.captureScreenshot", { format: "png" }).catch(() => {});
  session.close();

  console.log();
  if (problems.length) {
    console.log(`RENDER PROBLEMS (${problems.length}):`);
    for (const item of problems) console.log(`  - ${item}`);
    return 1;
  }
  console.log("RENDER OK - every console route mounted without errors");
  return 0;
}

main()
  .then((code) => process.exit(code))
  .catch((error) => {
    console.error("render check crashed:", error);
    process.exit(1);
  });
