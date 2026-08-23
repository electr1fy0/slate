import json

from fastapi import FastAPI
from starlette.testclient import TestClient

from middleware import (
    RateLimitMiddleware,
    RequestIdMiddleware,
    auth_rate_limits,
    log_json,
)


def build_app(limits=None):
    app = FastAPI()
    app.add_middleware(RateLimitMiddleware, limits=limits or {})
    app.add_middleware(RequestIdMiddleware)

    @app.get("/ping")
    def ping():
        return {"ok": True}

    @app.post("/auth/login")
    def login():
        return {"ok": True}

    return TestClient(app)


# ------------------------------------------------------------------- log_json

def test_log_json_emits_single_line_with_fields(capsys):
    log_json("seed_exam", exam_id="x1", reason=None)
    line = capsys.readouterr().out.strip()
    record = json.loads(line)
    assert record["event"] == "seed_exam"
    assert record["exam_id"] == "x1"
    assert "reason" not in record  # None values are dropped
    assert "ts" in record


# -------------------------------------------------------- RequestIdMiddleware

def test_request_id_generated_and_echoed():
    client = build_app()
    resp = client.get("/ping")
    assert resp.headers["x-request-id"]
    assert len(resp.headers["x-request-id"]) == 16


def test_request_id_honours_incoming_header():
    client = build_app()
    resp = client.get("/ping", headers={"x-request-id": "my-correlation-id"})
    assert resp.headers["x-request-id"] == "my-correlation-id"


# ------------------------------------------------------- RateLimitMiddleware

def test_rate_limit_blocks_after_limit_and_reports_retry_after():
    client = build_app(limits={"POST /auth/login": (2, 60)})
    assert client.post("/auth/login").status_code == 200
    assert client.post("/auth/login").status_code == 200
    blocked = client.post("/auth/login")
    assert blocked.status_code == 429
    assert blocked.json()["detail"] == "too many requests; slow down"
    assert int(blocked.headers["Retry-After"]) >= 1


def test_rate_limit_window_expiry_restores_access(monkeypatch):
    import middleware

    clock = {"now": 1000.0}
    monkeypatch.setattr(middleware.time, "monotonic", lambda: clock["now"])
    client = build_app(limits={"POST /auth/login": (1, 10)})
    assert client.post("/auth/login").status_code == 200
    assert client.post("/auth/login").status_code == 429
    clock["now"] += 11  # beyond the 10s window
    assert client.post("/auth/login").status_code == 200


def test_rate_limit_scopes_by_method_and_path():
    client = build_app(limits={"POST /auth/login": (1, 60)})
    assert client.post("/auth/login").status_code == 200
    # Same path, different method: unaffected.
    assert client.get("/auth/login").status_code == 405
    # Untouched paths are never limited.
    assert client.get("/ping").status_code == 200


def test_rate_limit_handles_missing_client_scope():
    import asyncio

    from starlette.requests import Request

    async def call_next(_request):
        class StubResponse:
            status_code = 200

            def __init__(self):
                self.headers = {}

        return StubResponse()

    middleware = RateLimitMiddleware(app=None, limits={"GET /x": (1, 60)})
    scope = {"type": "http", "method": "GET", "path": "/x", "headers": [], "query_string": b""}
    request = Request(scope)
    assert request.client is None
    decision = asyncio.run(middleware.dispatch(request, call_next))
    assert decision.status_code == 200


def test_auth_rate_limits_shape():
    from config import settings

    limits = auth_rate_limits()
    assert limits["POST /auth/login"][0] == settings.login_rate_limit
    assert limits["POST /auth/register"][0] == settings.register_rate_limit
    assert limits["POST /auth/login"][1] == settings.rate_limit_window_s
