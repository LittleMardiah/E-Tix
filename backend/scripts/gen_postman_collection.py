#!/usr/bin/env python3
"""Generate backend/postman_collection.json from the live route table.

FIX P9a-9 TD-014: Postman/Newman API contract collection.

Source of truth = backend/cmd/api/main.go route registrations. Regenerate with:
    python3 backend/scripts/gen_postman_collection.py

The generator parses `router.<METHOD>("/path"` and `<group>.<METHOD>("/path"`
where the group prefix comes from `x := router.Group("/prefix")`.
"""
import json
import re
import sys
from collections import OrderedDict
from pathlib import Path

BACKEND = Path(__file__).resolve().parent.parent
MAIN = BACKEND / "cmd" / "api" / "main.go"
OUT = BACKEND / "postman_collection.json"

EXAMPLE_UUID = "11111111-1111-4111-8111-111111111111"

# Folder order = execution order (auth first so accessToken is populated).
FOLDER_ORDER = [
    "Health",
    "Auth",
    "Users",
    "Queue",
    "Events",
    "Bookings",
    "Payments",
    "Tickets",
    "Gate",
    "Resale",
    "Admin",
    "Webhook",
    "Observability",
]

# Endpoints that must NOT carry a bearer token.
PUBLIC = {("GET", "/healthz/liveness"), ("GET", "/healthz/readiness"),
          ("GET", "/metrics"), ("POST", "/auth/register"), ("POST", "/auth/login"),
          ("POST", "/auth/refresh-token"), ("POST", "/auth/verify-otp"),
          ("POST", "/queue/verify-captcha"), ("POST", "/webhook/payment")}

# Endpoints that need the admin 2FA header (admin_2faMiddleware).
ADMIN_2FA = {
    "POST /admin/events/:event_id/bulk-cancel",
    "POST /admin/ledger/reverse",
    "POST /admin/force-release",
    "PATCH /admin/users/:user_id/suspend",
    "PATCH /admin/users/:user_id/unsuspend",
    "POST /admin/blacklist",
    "DELETE /admin/blacklist/:id",
}

# Endpoints behind IdempotencyMiddleware (require X-Idempotency-Key header).
IDEMPOTENT = {
    "POST /resale",
    "POST /resale/:id/buy",
    "DELETE /resale/listings/:id",
    "POST /admin/events/:event_id/bulk-cancel",
    "POST /admin/ledger/reverse",
    "POST /admin/force-release",
    "POST /admin/blacklist",
    "DELETE /admin/blacklist/:id",
    "POST /bookings/:booking_id/payments",
}

QUERY_PARAMS = {
    "GET /tickets": [("status", "CONFIRMED"), ("page", "1"), ("limit", "20")],
    "GET /events": [("status", "ON_SALE"), ("search", ""), ("sort_by", "event_date"),
                    ("page", "1"), ("limit", "20")],
    "GET /admin/ledger": [("date_from", ""), ("date_to", ""), ("status", ""),
                           ("page", "1"), ("limit", "50")],
    "GET /admin/stuck-locks": [("page", "1"), ("limit", "50")],
    "GET /admin/users": [("role", ""), ("status", ""), ("search", ""),
                         ("page", "1"), ("limit", "50")],
    "GET /admin/blacklist": [("page", "1"), ("limit", "50")],
    "GET /queue/status": [("event_id", EXAMPLE_UUID)],
    "GET /events/:event_id/dashboard": [],
    "GET /events/:event_id/analytics": [],
    "GET /events/:event_id/post-event-report": [("format", "json")],
}

BODIES = {
    "POST /auth/register": {"email": "newman+e2e@example.com", "password": "Passw0rd!123",
                            "name": "Newman E2E", "phone": "+628111222333", "role": "USER"},
    "POST /auth/login": {"email": "newman+e2e@example.com", "password": "Passw0rd!123"},
    "POST /auth/refresh-token": {"refresh_token": "{{refreshToken}}"},
    "POST /auth/verify-otp": {"user_id": "{{userId}}", "code": "123456"},
    "POST /queue/verify-captcha": {"token": "dummy-turnstile-token"},
    "POST /webhook/payment": {"external_transaction_id": "ext-tx-newman",
                              "status": "SUCCESS", "amount": "1000000"},
}


def parse_routes(src):
    """Return [(method, full_path)] from main.go."""
    prefixes = dict(re.findall(r'(\w+)\s*:=\s*router\.Group\("([^"]+)"', src))
    routes = []
    for m in re.finditer(r'(\w+)\.(GET|POST|PUT|PATCH|DELETE)\("([^"]+)"', src):
        recv, method, path = m.group(1), m.group(2), m.group(3)
        if recv == "router":
            full = path
        elif recv in prefixes:
            full = prefixes[recv] + path
        else:
            continue
        routes.append((method, full))
    seen, out = set(), []
    for method, full in routes:
        if (method, full) in seen:
            continue
        seen.add((method, full))
        out.append((method, full))
    return out


def folder_for(path):
    if path.startswith("/healthz"):
        return "Health"
    if path.startswith("/auth"):
        return "Auth"
    if path.startswith("/users"):
        return "Users"
    if path.startswith("/queue"):
        return "Queue"
    if path.startswith("/events"):
        return "Events"
    if path.startswith("/bookings"):
        return "Bookings"
    if path.startswith("/payments"):
        return "Payments"
    if path.startswith("/tickets"):
        return "Tickets"
    if path.startswith("/gate"):
        return "Gate"
    if path.startswith("/resale"):
        return "Resale"
    if path.startswith("/admin"):
        return "Admin"
    if path.startswith("/webhook"):
        return "Webhook"
    if path == "/metrics":
        return "Observability"
    return "Other"


def path_vars(path):
    return re.findall(r':([A-Za-z_][A-Za-z0-9_]*)', path)


def build_item(method, path):
    pvars = path_vars(path)
    url = {
        "raw": "{{baseUrl}}" + path,
        "host": ["{{baseUrl}}"],
        "path": [seg for seg in path.strip("/").split("/") if seg],
    }
    if pvars:
        url["variable"] = [{"key": v, "value": EXAMPLE_UUID} for v in pvars]
    key = f"{method} {path}"
    qp = QUERY_PARAMS.get(key)
    if qp:
        url["query"] = [{"key": k, "value": v} for k, v in qp]

    headers = [{"key": "Content-Type", "value": "application/json"}]
    if key not in PUBLIC:
        headers.append({"key": "Authorization", "value": "Bearer {{accessToken}}"})
    if key in ADMIN_2FA:
        headers.append({"key": "X-Admin-2FA-Token", "value": "{{admin2faToken}}"})
    if key in IDEMPOTENT:
        headers.append({"key": "X-Idempotency-Key", "value": "{{$guid}}"})

    body = BODIES.get(key)
    request = {"method": method, "header": headers, "url": url}
    if body is not None:
        request["body"] = {"mode": "raw", "raw": json.dumps(body, indent=2),
                           "options": {"raw": {"language": "json"}}}

    # Health + metrics do not use the {data,error,meta,success} envelope.
    plain = path.startswith("/healthz") or path == "/metrics"
    script = [
        "pm.test('no server error (status < 500)', function () {",
        "  pm.expect(pm.response.code).to.be.below(500);",
        "});",
    ]
    if not plain:
        script += [
            "pm.test('response is a JSON envelope', function () {",
            "  pm.response.to.be.json;",
            "  pm.expect(pm.response.json()).to.have.property('success');",
            "});",
        ]
    events = [{
        "listen": "test",
        "script": {"type": "text/javascript", "exec": script},
    }]
    if key == "POST /auth/login":
        events[0]["script"]["exec"] += [
            "var body = pm.response.json();",
            "if (body.access_token) { pm.collectionVariables.set('accessToken', body.access_token); }",
        ]
    if key == "POST /auth/register":
        events[0]["script"]["exec"] += [
            "var body = pm.response.json();",
            "if (body.access_token) { pm.collectionVariables.set('accessToken', body.access_token); }",
            "if (body.refresh_token) { pm.collectionVariables.set('refreshToken', body.refresh_token); }",
            "if (body.user_id) { pm.collectionVariables.set('userId', body.user_id); }",
        ]
    if key == "POST /auth/refresh-token":
        events[0]["script"]["exec"] += [
            "var body = pm.response.json();",
            "if (body.access_token) { pm.collectionVariables.set('accessToken', body.access_token); }",
        ]
    return {"name": key, "request": request, "event": events}


def main():
    if not MAIN.exists():
        sys.exit("MAIN_NOT_FOUND: %s" % MAIN)
    src = MAIN.read_text()
    routes = parse_routes(src)
    if not routes:
        sys.exit("NO_ROUTES_PARSED")

    groups = OrderedDict((f, []) for f in FOLDER_ORDER)
    for method, path in routes:
        groups.setdefault(folder_for(path), []).append(build_item(method, path))

    collection = {
        "info": {
            "name": "E-Tix Flash API",
            "_postman_id": "b6c045b-p9a9-td014",
            "description": ("Auto-generated from backend/cmd/api/main.go by "
                            "backend/scripts/gen_postman_collection.py. "
                            "%d endpoints. FIX P9a-9 TD-014." % len(routes)),
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "variable": [
            {"key": "baseUrl", "value": "http://localhost:8080"},
            {"key": "accessToken", "value": ""},
            {"key": "refreshToken", "value": ""},
            {"key": "adminToken", "value": ""},
            {"key": "admin2faToken", "value": ""},
            {"key": "queueToken", "value": ""},
            {"key": "userId", "value": EXAMPLE_UUID},
        ],
        "item": [],
    }
    for name, items in groups.items():
        if items:
            collection["item"].append({"name": name, "item": items})

    OUT.write_text(json.dumps(collection, indent=2) + "\n")
    total = sum(len(g["item"]) for g in collection["item"])
    print("ROUTES_PARSED=%d" % len(routes))
    print("COLLECTION_WRITTEN=%s" % OUT)
    print("FOLDERS=%d ENDPOINTS=%d" % (len(collection["item"]), total))


if __name__ == "__main__":
    main()
