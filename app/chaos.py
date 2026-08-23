import random
import time
from collections import deque

from fastapi import Request, Response
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.responses import JSONResponse

from config import settings


class ChaosState:
    def __init__(self):
        self.error_pct = settings.chaos_error_pct
        self.latency_ms = settings.chaos_latency_ms
        self.db_stall = settings.db_stall
        self.status_codes = deque(maxlen=settings.error_rate_window)


chaos_state = ChaosState()


class ChaosMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        chaos_state.status_codes.append(None)
        idx = len(chaos_state.status_codes) - 1
        if chaos_state.latency_ms:
            time.sleep(chaos_state.latency_ms / 1000.0)
        path = request.url.path
        is_probe_exempt = path.startswith("/health/live") or path.startswith("/admin/")
        if not is_probe_exempt and chaos_state.error_pct and random.randint(1, 100) <= chaos_state.error_pct:
            status = 500
            chaos_state.status_codes[idx] = status
            return JSONResponse({"error": "chaos injected"}, status_code=status)
        response: Response = await call_next(request)
        chaos_state.status_codes[idx] = response.status_code
        return response


def error_rate() -> float:
    codes = [c for c in chaos_state.status_codes if c is not None]
    if not codes:
        return 0.0
    return sum(1 for c in codes if c >= 500) / len(codes)


def reset_chaos():
    chaos_state.error_pct = 0
    chaos_state.latency_ms = 0
    chaos_state.db_stall = False
