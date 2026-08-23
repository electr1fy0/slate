"""Runtime middleware: request correlation, structured logs, rate limiting."""

import json
import time
import uuid
from collections import deque

from starlette.middleware.base import BaseHTTPMiddleware
from starlette.requests import Request
from starlette.responses import JSONResponse


def log_json(event: str, **fields) -> None:
    """Emit one JSON log line; the only logging style used by the app."""
    record = {"ts": round(time.time(), 3), "event": event}
    for key, value in fields.items():
        if value is not None:
            record[key] = value
    print(json.dumps(record), flush=True)


class RequestIdMiddleware(BaseHTTPMiddleware):
    """Attach a correlation id to every request and echo it back."""

    async def dispatch(self, request: Request, call_next):
        request_id = request.headers.get("x-request-id") or uuid.uuid4().hex[:16]
        request.state.request_id = request_id
        response = await call_next(request)
        response.headers["x-request-id"] = request_id
        return response


class RateLimitMiddleware(BaseHTTPMiddleware):
    """Sliding-window limiter for brute-forceable auth endpoints.

    Rules are keyed by ``"{method} {path}"``; clients by peer host. The window
    state is per-process and in-memory — sufficient for a single-node demo and
    honest about it.
    """

    def __init__(self, app, limits: dict[str, tuple[int, int]] | None = None):
        super().__init__(app)
        self.limits = limits or {}
        self._hits: dict[str, deque[float]] = {}

    def _prune(self, key: str, now: float, window_s: float) -> deque[float]:
        hits = self._hits.setdefault(key, deque())
        horizon = now - window_s
        while hits and hits[0] <= horizon:
            hits.popleft()
        return hits

    async def dispatch(self, request: Request, call_next):
        rule_key = f"{request.method} {request.scope.get('path', '')}"
        limit = self.limits.get(rule_key)
        if limit is not None:
            max_hits, window_s = limit
            client = request.client.host if request.client else "unknown"
            bucket_key = f"{rule_key}#{client}"
            now = time.monotonic()
            hits = self._prune(bucket_key, now, window_s)
            if len(hits) >= max_hits:
                oldest = hits[0]
                retry_after = max(1, int(window_s - (now - oldest)))
                return JSONResponse(
                    {"detail": "too many requests; slow down"},
                    status_code=429,
                    headers={"Retry-After": str(retry_after)},
                )
            hits.append(now)
        return await call_next(request)


def auth_rate_limits() -> dict[str, tuple[int, int]]:
    from config import settings

    window = settings.rate_limit_window_s
    return {
        "POST /auth/login": (settings.login_rate_limit, window),
        "POST /auth/register": (settings.register_rate_limit, window),
    }
