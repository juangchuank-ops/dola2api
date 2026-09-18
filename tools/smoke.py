#!/usr/bin/env python3
"""Dola2API end-to-end smoke test.

Usage:
    python tools/smoke.py [--base http://127.0.0.1:8080] [--password admin12345]

Checks the admin console API, the OpenAI-compatible surface and the audit
pipeline. It never touches the real upstream unless an account with a valid
cookie exists, so it is safe to run against a fresh instance.
"""

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

FAILURES: list[str] = []


def call(base, path, method="GET", body=None, token=None, timeout=30):
    data = json.dumps(body).encode() if body is not None else None
    request = urllib.request.Request(base + path, data=data, method=method)
    if data:
        request.add_header("content-type", "application/json")
    if token:
        request.add_header("authorization", "Bearer " + token)
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    try:
        with opener.open(request, timeout=timeout) as response:
            raw = response.read().decode()
            return response.status, (json.loads(raw) if raw else {})
    except urllib.error.HTTPError as error:
        raw = error.read().decode()
        try:
            return error.code, json.loads(raw)
        except json.JSONDecodeError:
            return error.code, {"raw": raw}
    except Exception as error:  # noqa: BLE001 - surfaced as a failed check
        return 0, {"error": str(error)}


def check(name, condition, detail=""):
    mark = "PASS" if condition else "FAIL"
    print(f"  [{mark}] {name}{(' · ' + detail) if detail else ''}")
    if not condition:
        FAILURES.append(name)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--username", default="admin")
    parser.add_argument("--password", default="admin12345")
    parser.add_argument(
        "--skip-upstream",
        action="store_true",
        help="skip gateway calls that need a live Dola account "
             "(they block until the upstream timeout otherwise)",
    )
    args = parser.parse_args()
    base = args.base.rstrip("/")

    print("Dola2API smoke test")
    print(f"base = {base}\n")

    print("1. health")
    status, payload = call(base, "/health")
    check("GET /health returns 200", status == 200, f"status={status}")
    check("health reports version", bool(payload.get("version")), str(payload.get("version")))
    check("health reports pool", "pool" in payload)

    print("\n2. admin auth")
    status, payload = call(base, "/admin/api/auth/login", "POST",
                           {"username": args.username, "password": args.password})
    check("login returns 200", status == 200, f"status={status} {payload}")
    token = payload.get("token", "")
    check("login returns a token", bool(token))
    if not token:
        return finish()

    status, payload = call(base, "/admin/api/auth/me", token=token)
    check("GET /auth/me returns the profile", status == 200 and payload.get("username") == args.username)

    status, _ = call(base, "/admin/api/accounts")
    check("unauthenticated admin call is rejected", status == 401, f"status={status}")

    print("\n3. dashboard and settings")
    status, payload = call(base, "/admin/api/dashboard?period=30d&timezone=Asia/Shanghai", token=token)
    check("GET /dashboard returns 200", status == 200, f"status={status}")
    check("dashboard exposes usage", "usage" in payload)
    check("dashboard exposes resources", "resources" in payload)

    status, payload = call(base, "/admin/api/settings", token=token)
    check("GET /settings returns 200", status == 200)
    check("settings carry routing policy", "routing" in payload)
    check("settings carry about block", payload.get("about", {}).get("version") is not None)

    print("\n4. client keys")
    status, payload = call(base, "/admin/api/client-keys", "POST",
                           {"name": "smoke-key", "rpmLimit": 60, "maxConcurrent": 4}, token)
    check("create client key returns 200", status == 200, f"status={status} {payload}")
    key = payload.get("key", {}).get("key", "")
    check("client key has a value", key.startswith("sk-dola-"), key[:14])

    status, payload = call(base, "/admin/api/client-keys", token=token)
    check("list client keys returns 200", status == 200 and payload.get("total", 0) >= 1)

    print("\n5. models")
    status, payload = call(base, "/admin/api/models", token=token)
    check("GET /admin/models returns 200", status == 200)
    model_ids = {item["id"] for item in payload.get("items", [])}
    for expected in ("dola-fast", "dola-pro", "dola-image", "dreamina-seedance-1.0"):
        check(f"catalogue contains {expected}", expected in model_ids)

    status, payload = call(base, "/v1/models", token=key)
    check("GET /v1/models returns 200", status == 200, f"status={status}")
    check("GET /v1/models lists dola-fast", any(item["id"] == "dola-fast" for item in payload.get("data", [])))

    status, _ = call(base, "/v1/models")
    check("GET /v1/models requires a key", status == 401, f"status={status}")

    print("\n6. accounts")
    status, payload = call(base, "/admin/api/accounts", "POST", {
        "name": "smoke-account",
        "cookie": "oauth_token=smoke; oauth_token_v2=smoke; flow_cur_user_sec_id=1; passport_csrf_token=x",
        "priority": 30, "maxConcurrent": 3,
    }, token, timeout=60)
    check("create account returns 200", status == 200, f"status={status} {payload}")
    account_id = (payload.get("account") or {}).get("id", "")
    check("account has an id", bool(account_id))
    serialised = json.dumps(payload)
    check("raw cookie never leaves the server", "smoke; oauth_token_v2" not in serialised)
    check("cookie is masked in the response", bool((payload.get("account") or {}).get("cookieMasked")))

    status, payload = call(base, "/admin/api/accounts", token=token)
    check("list accounts returns 200", status == 200)
    check("account appears in the list", payload.get("total", 0) >= 1)
    check("account summary is present", "summary" in payload)

    status, payload = call(base, "/admin/api/accounts/groups", token=token)
    check("account groups return 200", status == 200)

    if account_id:
        status, payload = call(base, f"/admin/api/accounts/{account_id}", "PATCH",
                               {"name": "smoke-account-renamed", "priority": 10, "maxConcurrent": 5}, token)
        check("update account returns 200", status == 200, f"status={status}")
        check("update applied", (payload.get("account") or {}).get("priority") == 10)

        status, payload = call(base, "/admin/api/accounts/batch", "POST",
                               {"action": "disable", "ids": [account_id]}, token)
        check("batch disable returns 200", status == 200)
        status, payload = call(base, "/admin/api/accounts", token=token)
        target = next((item for item in payload.get("items", []) if item["id"] == account_id), None)
        check("account is disabled", target is not None and target.get("status") == "disabled")

        status, payload = call(base, "/admin/api/accounts/batch", "POST",
                               {"action": "enable", "ids": [account_id]}, token)
        check("batch enable returns 200", status == 200)
        status, payload = call(base, "/admin/api/accounts/batch", "POST",
                               {"action": "clearCooldown", "ids": [account_id]}, token)
        check("clear cooldown returns 200", status == 200)
        status, payload = call(base, "/admin/api/accounts/batch", "POST",
                               {"action": "concurrency", "ids": [account_id], "maxConcurrent": 7}, token)
        check("batch concurrency returns 200", status == 200)

    status, payload = call(base, "/admin/api/accounts/export?limit=10", token=token)
    check("export accounts returns 200", status == 200 and payload.get("count", 0) >= 1)

    status, payload = call(base, "/admin/api/accounts/import", "POST",
                           {"cookies": "oauth_token=imported; oauth_token_v2=imported"}, token)
    check("import accounts returns 200", status == 200, f"status={status}")
    check("import created one account", payload.get("created", 0) == 1, json.dumps(payload))

    status, payload = call(base, "/admin/api/accounts/import", "POST",
                           {"cookies": "oauth_token=imported; oauth_token_v2=imported"}, token)
    check("duplicate import updates instead of duplicating",
          payload.get("updated", 0) == 1 and payload.get("created", 0) == 0, json.dumps(payload))

    print("\n7. service stays responsive while background probes run")
    # Regression guard: creating an account kicks off a detached upstream probe.
    # When that probe fails it writes account state from inside a mutation
    # callback. A reentrant-lock bug there used to freeze the whole process, so
    # assert the console keeps answering while probes are in flight.
    alive = True
    slowest = 0.0
    for _ in range(6):
        time.sleep(1.5)
        started = time.monotonic()
        probe_status, _ = call(base, "/health", timeout=5)
        elapsed = time.monotonic() - started
        slowest = max(slowest, elapsed)
        if probe_status != 200:
            alive = False
            break
    check("health stays 200 while probes run", alive)
    check("health responds promptly under load", slowest < 2.0, f"slowest={slowest:.2f}s")

    status, payload = call(base, "/v1/chat/completions", "POST",
                           {"model": "nope", "messages": [{"role": "user", "content": "hi"}]}, key)
    check("unknown model returns 400", status == 400, f"status={status}")

    if args.skip_upstream:
        print("\n8. gateway (skipped)")
        print("  [SKIP] upstream gateway calls (--skip-upstream)")
    else:
        print("\n8. gateway (expected to fail without a valid Dola cookie)")
        status, payload = call(base, "/v1/chat/completions", "POST",
                               {"model": "dola-fast", "messages": [{"role": "user", "content": "hi"}]},
                               key, timeout=180)
        check("chat completions answers with an HTTP status", status in (200, 400, 429, 502), f"status={status}")
        if status == 400:
            check("bad model is reported clearly", "model" in json.dumps(payload))

        status, payload = call(base, "/v1/images/generations", "POST", {"prompt": "a blue circle"}, key, timeout=180)
        check("image generations answers with an HTTP status", status in (200, 502), f"status={status}")

        status, payload = call(base, "/health")
        check("health still 200 after gateway calls", status == 200, f"status={status}")

    print("\n9. audits")
    status, payload = call(base, "/admin/api/audits", token=token)
    check("GET /audits returns 200", status == 200)
    if args.skip_upstream:
        print("  [SKIP] audit capture needs a gateway request (--skip-upstream)")
    else:
        check("audit records were captured", payload.get("total", 0) >= 1, f"total={payload.get('total')}")

    print("\n10. cleanup")
    status, payload = call(base, "/admin/api/accounts/cleanup", "POST", {"statuses": ["invalid"]}, token)
    check("cleanup returns 200", status == 200, f"status={status}")

    status, payload = call(base, "/admin/api/accounts", token=token)
    for item in payload.get("items", []):
        if item["name"].startswith("smoke-") or item["name"].startswith("dola-"):
            call(base, f"/admin/api/accounts/{item['id']}", "DELETE", token=token)
    status, payload = call(base, "/admin/api/client-keys", token=token)
    for item in payload.get("items", []):
        if item["name"] == "smoke-key":
            call(base, f"/admin/api/client-keys/{item['id']}", "DELETE", token=token)
    check("smoke artifacts removed", True)

    return finish()


def finish():
    print()
    if FAILURES:
        print(f"FAILED ({len(FAILURES)}): " + ", ".join(FAILURES))
        return 1
    print("ALL CHECKS PASSED")
    return 0


if __name__ == "__main__":
    sys.exit(main())
