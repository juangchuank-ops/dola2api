#!/usr/bin/env python3
"""Field-level contract check between the console's TypeScript DTOs and the
JSON the backend actually returns.

Route-level checks (tools/contract.py) catch a wrong path or verb. They do not
catch a renamed or dropped field, which shows up in the browser as a silently
blank cell. This script parses the `export type Xxx = { ... }` blocks out of the
frontend api layer, pulls a live response from each endpoint, and fails if the
response is missing any field the frontend expects.

Usage:
    python tools/fields.py [--base http://127.0.0.1:18080] [--password admin12345]
"""

import argparse
import json
import re
import sys
import urllib.error
import urllib.request

PROBLEMS: list[str] = []

# (api module, TS type name, endpoint, JSON path into the response)
CHECKS = [
    ("accounts", "AccountDTO", "/admin/api/accounts", "items.0"),
    ("accounts", "AccountSummary", "/admin/api/accounts", "summary"),
    ("accounts", "AccountQuota", "/admin/api/accounts", "items.0.quota"),
    ("client-keys", "ClientKeyDTO", "/admin/api/client-keys", "items.0"),
    ("models", "ModelDTO", "/admin/api/models", "items.0"),
    ("request-audits", "AuditDTO", "/admin/api/audits", "items.0"),
    ("dashboard", "DashboardDTO", "/admin/api/dashboard?period=30d", ""),
    ("settings", "SettingsDTO", "/admin/api/settings", ""),
]

# When a collection is empty there is no live sample to inspect (audits are only
# written by gateway traffic, quota only after a successful probe). Fall back to
# comparing against the Go struct's json tags, which still catches a renamed or
# dropped field.
STATIC_SOURCES = {
    "AccountQuota": ("backend/internal/store/types.go", "Quota"),
    "AuditDTO": ("backend/internal/store/types.go", "Audit"),
}


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


def parse_ts_types(source_root):
    """Extract {type name: [dotted field paths]} from the frontend api modules.

    Nested inline objects are flattened into dotted paths ("resources.routable")
    rather than skipped. A field inside a nested object is just as easy to drop
    from a response as a top-level one, and a check that only looks at the first
    level cannot see it.
    """
    types: dict[str, list[str]] = {}
    modules: dict[str, str] = {}

    import pathlib

    for path in pathlib.Path(source_root).glob("features/*/*-api.ts"):
        text = path.read_text(encoding="utf-8")
        module = path.parent.name
        for match in re.finditer(
            r"export type (\w+) = \{(.*?)\n\};", text, re.DOTALL
        ):
            name, body = match.group(1), match.group(2)
            fields: list[str] = []
            stack: list[str] = []  # enclosing inline object names
            depth = 0
            for raw_line in body.splitlines():
                line = raw_line.strip()
                if not line or line.startswith("//") or line.startswith("*") or line.startswith("/*"):
                    continue

                opened = line.count("{")
                closed = line.count("}")

                field = re.match(r"([A-Za-z_]\w*)\??\s*:", line)
                if field:
                    prefix = ".".join(stack)
                    fields.append(f"{prefix}.{field.group(1)}" if prefix else field.group(1))
                    # Only a field whose value opens a nested object pushes a
                    # new level; `field: SomeType;` and single-line literals
                    # leave the stack alone.
                    if opened > closed:
                        stack.append(field.group(1))

                depth += opened - closed
                while len(stack) > depth:
                    stack.pop()

            types[name] = fields
            modules[name] = module
    return types, modules


def parse_go_struct(path, struct_name):
    """Extract json field names from a Go struct definition."""
    import pathlib

    file = pathlib.Path(path)
    if not file.exists():
        return None
    text = file.read_text(encoding="utf-8")
    match = re.search(
        r"^type %s struct \{(.*?)^\}" % re.escape(struct_name),
        text,
        re.DOTALL | re.MULTILINE,
    )
    if not match:
        return None
    fields: list[str] = []
    for line in match.group(1).splitlines():
        tag = re.search(r'json:"([^",]+)', line)
        if tag and tag.group(1) != "-":
            fields.append(tag.group(1))
    return fields


def dig(payload, path):
    """Walk a dotted path; numeric segments index into lists."""
    if not path:
        return payload
    node = payload
    for segment in path.split("."):
        if isinstance(node, list):
            if not segment.isdigit() or int(segment) >= len(node):
                return None
            node = node[int(segment)]
        elif isinstance(node, dict):
            if segment not in node:
                return None
            node = node[segment]
        else:
            return None
    return node


def has_path(payload, path):
    """True when a dotted path resolves, even if the value it holds is null.

    dig() cannot be used for this: it returns None both for a missing key and
    for a key that is legitimately null, and several DTO fields (quota, for one)
    are declared as nullable and expected to be present with a null value.
    """
    segments = path.split(".")
    node = payload
    index = 0
    while index < len(segments):
        segment = segments[index]
        if isinstance(node, list):
            if segment.isdigit():
                if int(segment) >= len(node):
                    return False
                node = node[int(segment)]
                index += 1
            elif not node:
                # Descending into the element type of an empty array. There is
                # nothing to inspect yet, so stay quiet rather than reporting a
                # field the endpoint simply had no data for.
                return True
            else:
                # Step into the first element without consuming the segment, so
                # it is still checked against the element itself.
                node = node[0]
        elif isinstance(node, dict):
            if segment not in node:
                return False
            node = node[segment]
            index += 1
        else:
            return False
    return True


# (case name, sample payload, dotted path, expected result)
#
# A path checker that always answers True would hand out a clean bill of health
# no matter what the endpoints return, so its own behaviour is pinned down here
# and re-verified on every run.
SELF_CHECK_CASES = [
    ("nested field missing", {"resources": {"totalAccounts": 1}}, "resources.routableAccounts", False),
    ("nested field present", {"resources": {"routableAccounts": 2}}, "resources.routableAccounts", True),
    ("nullable field present with null", {"quota": None}, "quota", True),
    ("nullable field absent", {}, "quota", False),
    ("empty array element type", {"activity": []}, "activity.id", True),
    ("populated array missing element field", {"activity": [{"x": 1}]}, "activity.id", False),
    ("populated array with element field", {"activity": [{"id": "a"}]}, "activity.id", True),
    ("indexed array path", {"items": [{"id": "a"}]}, "items.0.id", True),
    ("indexed array path out of range", {"items": []}, "items.0.id", False),
    ("two levels deep missing", {"usage": {"a": 1}}, "usage.a.b", False),
    ("two levels deep present", {"usage": {"a": {"b": None}}}, "usage.a.b", True),
    ("wrong root type", "text", "a", False),
]


def self_check():
    """Return a list of failures in has_path's own behaviour."""
    failures = []
    for name, sample, path, want in SELF_CHECK_CASES:
        got = has_path(sample, path)
        if got != want:
            failures.append(f"{name}: got {got}, want {want}")
    return failures


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--base", default="http://127.0.0.1:8080")
    parser.add_argument("--username", default="admin")
    parser.add_argument("--password", default="admin12345")
    parser.add_argument("--src", default="frontend/src")
    args = parser.parse_args()
    base = args.base.rstrip("/")

    print("Dola2API field-level contract check")
    print(f"base = {base}")
    print(f"src  = {args.src}\n")

    failures = self_check()
    if failures:
        print("path checker self-check FAILED:")
        for item in failures:
            print(f"  - {item}")
        return 1
    print(f"path checker self-check OK ({len(SELF_CHECK_CASES)} cases)\n")

    types, _ = parse_ts_types(args.src)
    if not types:
        print(f"no TypeScript DTOs found under {args.src}")
        return 1
    print(f"parsed {len(types)} DTO type(s): {', '.join(sorted(types))}\n")

    status, payload = call(base, "/admin/api/auth/login", "POST",
                           {"username": args.username, "password": args.password})
    token = payload.get("token", "")
    if not token:
        print(f"login failed ({status}) - cannot continue: {payload}")
        return 1

    # Seed one account and one key so list endpoints have a sample row to check.
    seeded_account = None
    seeded_key = None
    status, payload = call(base, "/admin/api/accounts", "POST", {
        "name": "fieldcheck-account",
        "cookie": "oauth_token=fieldcheck; oauth_token_v2=fieldcheck",
    }, token)
    seeded_account = (payload.get("account") or {}).get("id")
    status, payload = call(base, "/admin/api/client-keys", "POST",
                           {"name": "fieldcheck-key"}, token)
    seeded_key = (payload.get("key") or {}).get("id")

    skipped: list[str] = []
    for module, type_name, endpoint, path in CHECKS:
        expected = types.get(type_name)
        if expected is None:
            PROBLEMS.append(f"{type_name} (type not found in {module}-api.ts)")
            print(f"  [MISSING TYPE] {type_name}")
            continue

        status, payload = call(base, endpoint, token=token)
        sample = dig(payload, path)
        if sample is None:
            static = STATIC_SOURCES.get(type_name)
            if not static:
                skipped.append(f"{type_name} ({endpoint} had no sample at '{path or 'root'}')")
                print(f"  [skip] {type_name:<18} {endpoint}  (no sample data)")
                continue
            go_path, struct_name = static
            backend_fields = parse_go_struct(go_path, struct_name)
            if backend_fields is None:
                skipped.append(f"{type_name} (could not parse {struct_name})")
                print(f"  [skip] {type_name:<18} {endpoint}  (no sample, {struct_name} unparsed)")
                continue
            # The Go struct fallback compares against one flat struct, so only
            # top-level fields can be resolved here.
            flat = [field for field in expected if "." not in field]
            nested = [field for field in expected if "." in field]
            missing = [field for field in flat if field not in backend_fields]
            if missing:
                PROBLEMS.append(f"{type_name}: missing {', '.join(missing)}")
                print(f"  [MISSING] {type_name:<18} static vs Go {struct_name}")
                print(f"            backend omits: {', '.join(missing)}")
            else:
                note = f"  ({len(nested)} nested skipped)" if nested else ""
                print(f"  [ok] {type_name:<18} static vs Go {struct_name}  {len(flat)} field(s){note}")
            continue
        if not isinstance(sample, dict):
            PROBLEMS.append(f"{type_name} sample is not an object")
            print(f"  [BAD SAMPLE] {type_name} -> {type(sample).__name__}")
            continue

        missing = [field for field in expected if not has_path(sample, field)]
        # Extra keys are only meaningful at the top level; nested paths are
        # compared against the nested sample, not against the parent object.
        top_level = {field for field in expected if "." not in field}
        extra = [key for key in sample if key not in top_level]

        if missing:
            PROBLEMS.append(f"{type_name}: missing {', '.join(missing)}")
            print(f"  [MISSING] {type_name:<18} {endpoint}")
            print(f"            backend omits: {', '.join(missing)}")
        else:
            note = f"  (+{len(extra)} extra: {', '.join(extra)})" if extra else ""
            nested = sum(1 for field in expected if "." in field)
            shape = f"  {len(expected)} field(s), {nested} nested" if nested else f"  {len(expected)} field(s)"
            print(f"  [ok] {type_name:<18} {endpoint}{shape}{note}")

    # Cross-endpoint consistency. Every check above compares one endpoint
    # against the frontend, so none of them can see two endpoints disagreeing
    # about the same number - which is exactly how a healthy pool ended up
    # reported as 0% available.
    def read_health():
        _, body = call(base, "/health")
        return body

    def read_dashboard():
        _, body = call(base, "/admin/api/dashboard?period=30d", token=token)
        return body

    pairs = [
        ("pool total", "pool.total", "resources.totalAccounts"),
        ("pool routable", "pool.routable", "resources.routableAccounts"),
        ("pool available", "pool.routable", "upstream.poolAvailable"),
    ]
    for label, left_path, right_path in pairs:
        left = dig(read_health(), left_path)
        right = dig(read_dashboard(), right_path)
        if left is None or right is None:
            skipped.append(f"consistency {label} (missing on one side: {left!r} vs {right!r})")
            continue
        # These counts move with the clock: an account whose cooldown lapses
        # between the two reads changes the answer even though nothing is wrong.
        # Re-read before reporting, so that only a persistent disagreement - two
        # endpoints computing the same number from different definitions - fails.
        for _ in range(3):
            if left == right:
                break
            left = dig(read_health(), left_path)
            right = dig(read_dashboard(), right_path)
        if left != right:
            PROBLEMS.append(f"{label}: /health says {left}, /dashboard says {right}")
            print(f"  [MISMATCH] {label:<18} /health={left}  /dashboard={right}")
        else:
            print(f"  [ok] {label:<18} both endpoints agree on {left}")

    # Clean up the seeded rows.
    if seeded_account:
        call(base, f"/admin/api/accounts/{seeded_account}", "DELETE", token=token)
    if seeded_key:
        call(base, f"/admin/api/client-keys/{seeded_key}", "DELETE", token=token)

    print()
    if skipped:
        for item in skipped:
            print(f"skipped: {item}")
    if PROBLEMS:
        print(f"\nFIELD MISMATCH ({len(PROBLEMS)}): " + "; ".join(PROBLEMS))
        return 1
    print("\nFIELDS OK - every DTO field the console reads is present in the response")
    return 0


if __name__ == "__main__":
    sys.exit(main())
