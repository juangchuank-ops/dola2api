#!/usr/bin/env python3
"""Contract check: every endpoint the frontend calls must exist on the backend.

The frontend is compiled, so a route mismatch (wrong path or wrong verb) only
shows up as a runtime 404/405 in the browser. This script replays every call the
console makes and fails only on 404/405, since any other status means the route
was matched (400 = validation, 401 = auth, 200 = ok).

Usage:
    python tools/contract.py [--base http://127.0.0.1:18080] [--password admin12345]
"""

import argparse
import json
import sys
import time
import urllib.error
import urllib.request

PROBLEMS: list[str] = []


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
    except Exception as error:  # noqa: BLE001
        return 0, {"error": str(error)}


def probe(base, label, path, method, body, token, timeout=30):
    status, payload = call(base, path, method, body, token, timeout)
    if status in (404, 405):
        PROBLEMS.append(f"{method} {path}")
        print(f"  [MISSING] {label:<34} {method:<6} {path}  -> {status}")
    elif status == 0:
        PROBLEMS.append(f"{method} {path} (unreachable)")
        print(f"  [UNREACHABLE] {label:<30} {method:<6} {path}  {payload}")
    else:
        print(f"  [ok] {label:<39} {method:<6} {path}  -> {status}")
    return status, payload


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--username", default="admin")
    parser.add_argument("--password", default="admin12345")
    args = parser.parse_args()
    base = args.base.rstrip("/")

    print("Dola2API frontend/backend contract check")
    print(f"base = {base}\n")

    print("auth")
    status, payload = call(base, "/admin/api/auth/login", "POST",
                           {"username": args.username, "password": args.password})
    token = payload.get("token", "")
    if not token:
        print(f"  login failed ({status}) - cannot continue: {payload}")
        return 1
    print(f"  [ok] login -> {status}")

    probe(base, "current profile", "/admin/api/auth/me", "GET", None, token)

    print("\ndashboard & settings")
    probe(base, "dashboard", "/admin/api/dashboard?period=30d&timezone=Asia/Shanghai",
          "GET", None, token)
    probe(base, "settings read", "/admin/api/settings", "GET", None, token)
    status, payload = call(base, "/admin/api/settings", token=token)
    probe(base, "settings write (no-op)", "/admin/api/settings", "PUT", payload, token)

    print("\nclient keys")
    probe(base, "key list", "/admin/api/client-keys", "GET", None, token)
    status, payload = probe(base, "key create", "/admin/api/client-keys", "POST",
                            {"name": "contract-key", "rpmLimit": 60, "maxConcurrent": 2}, token)
    key_id = (payload.get("key") or {}).get("id", "")
    key_value = (payload.get("key") or {}).get("key", "")
    if key_id:
        probe(base, "key update", f"/admin/api/client-keys/{key_id}", "PATCH",
              {"name": "contract-key-renamed"}, token)

    print("\nmodels")
    probe(base, "model catalogue", "/admin/api/models", "GET", None, token)
    status, payload = call(base, "/admin/api/models", token=token)
    items = payload.get("items", [])
    if items:
        probe(base, "model update", f"/admin/api/models/{items[0]['id']}", "PATCH",
              {"enabled": items[0].get("enabled", True)}, token)

    print("\nopenai surface")
    probe(base, "models (public)", "/v1/models", "GET", None, key_value or token)
    probe(base, "health", "/health", "GET", None, None)

    print("\naccounts")
    probe(base, "account list", "/admin/api/accounts", "GET", None, token)
    probe(base, "account groups", "/admin/api/accounts/groups", "GET", None, token)
    status, payload = probe(base, "account create", "/admin/api/accounts", "POST", {
        "name": "contract-account",
        "cookie": "oauth_token=contract; oauth_token_v2=contract",
        "priority": 40, "maxConcurrent": 2,
    }, token)
    account_id = (payload.get("account") or {}).get("id", "")
    if account_id:
        probe(base, "account update", f"/admin/api/accounts/{account_id}", "PATCH",
              {"name": "contract-account-renamed"}, token)
        probe(base, "account probe", f"/admin/api/accounts/{account_id}/probe",
              "POST", None, token)
        probe(base, "account quota", f"/admin/api/accounts/{account_id}/quota",
              "POST", None, token)
    probe(base, "account batch", "/admin/api/accounts/batch", "POST",
          {"action": "clearCooldown", "ids": [account_id] if account_id else []}, token)
    probe(base, "account export", "/admin/api/accounts/export?limit=5", "GET", None, token)
    probe(base, "account import", "/admin/api/accounts/import", "POST",
          {"cookies": "oauth_token=contract2; oauth_token_v2=contract2"}, token)
    probe(base, "probe all", "/admin/api/accounts/probe-all", "POST", None, token, timeout=60)
    probe(base, "quota all", "/admin/api/accounts/quota-all", "POST", None, token, timeout=60)
    probe(base, "account cleanup", "/admin/api/accounts/cleanup", "POST",
          {"statuses": ["invalid"]}, token)

    print("\naudits & gallery")
    probe(base, "audit list", "/admin/api/audits", "GET", None, token)
    status, payload = call(base, "/admin/api/audits?limit=5", token=token)
    audits = payload.get("items", [])
    if audits:
        probe(base, "audit detail", f"/admin/api/audits/{audits[0]['id']}", "GET", None, token)
    else:
        # No records yet (audits are only written by gateway traffic). Probe with
        # a bogus id: a routed endpoint replies with a JSON error, while an
        # unrouted one returns ServeMux's plain-text 404.
        status, payload = call(base, "/admin/api/audits/aud_does_not_exist", token=token)
        routed = isinstance(payload, dict) and "error" in payload
        if routed:
            print(f"  [ok] audit detail (no records)            GET    /admin/api/audits/{{id}}  -> {status} (json)")
        else:
            PROBLEMS.append("GET /admin/api/audits/{id}")
            print(f"  [MISSING] audit detail                    GET    /admin/api/audits/{{id}}  -> {status} (plain)")
    probe(base, "gallery", "/admin/api/gallery", "GET", None, token)

    print("\ncleanup & destructive verbs")
    probe(base, "audit clear", "/admin/api/audits", "DELETE", None, token)
    if account_id:
        probe(base, "account delete", f"/admin/api/accounts/{account_id}", "DELETE", None, token)
    if key_id:
        probe(base, "key delete", f"/admin/api/client-keys/{key_id}", "DELETE", None, token)

    print("\npassword endpoint (wrong current password, expects 400 not 404)")
    probe(base, "password change", "/admin/api/auth/password", "POST",
          {"currentPassword": "definitely-wrong", "newPassword": "whatever12345"}, token)

    print("\nlogout")
    probe(base, "logout", "/admin/api/auth/logout", "POST", None, token)

    print()
    if PROBLEMS:
        print(f"MISSING ROUTES ({len(PROBLEMS)}): " + ", ".join(PROBLEMS))
        return 1
    print("CONTRACT OK - every frontend call resolves to a backend route")
    return 0


if __name__ == "__main__":
    sys.exit(main())
