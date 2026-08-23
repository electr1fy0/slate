import random

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

import chaos as chaos_mod
from chaos import ChaosMiddleware, chaos_state, error_rate, reset_chaos


@pytest.fixture()
def mini_app():
    app = FastAPI()
    app.add_middleware(ChaosMiddleware)

    @app.get("/work")
    def work():
        return {"ok": True}

    @app.get("/health/live")
    def live():
        return {"status": "alive"}

    @app.get("/admin/thing")
    def admin():
        return {"admin": True}

    return app


def test_passthrough_records_status(mini_app):
    with TestClient(mini_app) as client:
        assert client.get("/work").json() == {"ok": True}
    assert list(chaos_state.status_codes) == [200]


def test_latency_injection_sleeps(mini_app, monkeypatch):
    slept = []
    monkeypatch.setattr(chaos_mod.time, "sleep", lambda s: slept.append(s))
    chaos_state.latency_ms = 250
    with TestClient(mini_app) as client:
        assert client.get("/work").status_code == 200
    assert slept == [0.25]


def test_error_injection_hits(mini_app, monkeypatch):
    monkeypatch.setattr(random, "randint", lambda a, b: 1)
    chaos_state.error_pct = 100
    with TestClient(mini_app) as client:
        resp = client.get("/work")
    assert resp.status_code == 500
    assert resp.json() == {"error": "chaos injected"}
    assert list(chaos_state.status_codes) == [500]


def test_error_injection_misses_on_high_roll(mini_app, monkeypatch):
    monkeypatch.setattr(random, "randint", lambda a, b: 100)
    chaos_state.error_pct = 50
    with TestClient(mini_app) as client:
        assert client.get("/work").status_code == 200


def test_probe_and_admin_paths_exempt_from_injection(mini_app):
    chaos_state.error_pct = 100
    with TestClient(mini_app) as client:
        assert client.get("/health/live").status_code == 200
        assert client.get("/admin/thing").status_code == 200


def test_error_rate_empty_window():
    chaos_state.status_codes.clear()
    assert error_rate() == 0.0


def test_error_rate_mixed_window():
    chaos_state.status_codes.clear()
    for code in (200, 500, 503, 200):
        chaos_state.status_codes.append(code)
    assert error_rate() == pytest.approx(0.5)


def test_reset_chaos_restores_neutral_flags():
    chaos_state.error_pct = 99
    chaos_state.latency_ms = 999
    chaos_state.db_stall = True
    reset_chaos()
    assert (chaos_state.error_pct, chaos_state.latency_ms, chaos_state.db_stall) == (0, 0, False)


def test_chaos_state_seeds_from_settings(monkeypatch):
    monkeypatch.setattr(chaos_mod.settings, "chaos_error_pct", 11)
    monkeypatch.setattr(chaos_mod.settings, "chaos_latency_ms", 22)
    monkeypatch.setattr(chaos_mod.settings, "db_stall", True)
    monkeypatch.setattr(chaos_mod.settings, "error_rate_window", 3)
    state = chaos_mod.ChaosState()
    assert state.error_pct == 11
    assert state.latency_ms == 22
    assert state.db_stall is True
    assert state.status_codes.maxlen == 3
