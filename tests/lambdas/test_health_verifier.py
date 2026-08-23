import json
import urllib.error

import pytest

TABLE = "continuum"
ENV = {"TABLE_NAME": TABLE, "TARGET_URL": "http://app.internal", "ROLE_LABEL": "primary"}


class FakeResp:
    def __init__(self, status=200, body=None):
        self.status = status
        self._raw = json.dumps(body if body is not None else {}).encode()

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def read(self):
        return self._raw


class FakeUrlopen:
    def __init__(self, *responses):
        self.responses = list(responses)
        self.calls = []

    def __call__(self, url, timeout=None):
        self.calls.append((url, timeout))
        result = self.responses.pop(0)
        if isinstance(result, Exception):
            raise result
        return result


@pytest.fixture()
def hv(lambda_loader, make_table):
    make_table(TABLE)
    return lambda_loader("health_verifier", ENV)


def test_probe_http_without_target_url(lambda_loader):
    mod = lambda_loader("health_verifier", {"TABLE_NAME": TABLE, "TARGET_URL": ""})
    out = mod._probe_http(None)
    assert out == {"ok": False, "reason": "no target url"}


def test_probe_http_healthy_app(hv, monkeypatch):
    fake = FakeUrlopen(FakeResp(200, {"healthy": True, "checks": {"ddb": {"ok": True}}}))
    monkeypatch.setattr("urllib.request.urlopen", fake)
    out = hv._probe_http(None)
    assert out["ok"] is True
    assert out["http_status"] == 200
    assert out["latency_ms"] >= 0
    assert out["detail"] == {"ddb": {"ok": True}}
    assert fake.calls[0][0] == "http://app.internal/health/deep"


def test_probe_http_strips_trailing_slash(hv, monkeypatch):
    fake = FakeUrlopen(FakeResp(200, {"healthy": True}))
    monkeypatch.setattr("urllib.request.urlopen", fake)
    hv._probe_http("http://host.example/")
    assert fake.calls[0][0].startswith("http://host.example/health/deep")


def test_probe_http_process_up_but_unhealthy_body(hv, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", FakeUrlopen(FakeResp(200, {"healthy": False})))
    assert hv._probe_http(None)["ok"] is False


def test_probe_http_non_200_status(hv, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", FakeUrlopen(FakeResp(503, {"healthy": True})))
    out = hv._probe_http(None)
    assert out["ok"] is False
    assert out["http_status"] == 503


def test_probe_http_connection_error_reports_reason(hv, monkeypatch):
    monkeypatch.setattr(
        "urllib.request.urlopen",
        FakeUrlopen(urllib.error.URLError("connection refused")))
    out = hv._probe_http(None)
    assert out["ok"] is False
    assert "connection refused" in out["reason"]


def test_probe_ddb_direct_roundtrip(hv):
    out = hv._probe_ddb_direct()
    assert out["ok"] is True
    assert out["latency_ms"] >= 0


def test_probe_ddb_direct_failure_reason(hv, monkeypatch):
    class Broken:
        def put_item(self, **kwargs):
            raise RuntimeError("dynamodb throttled")

    monkeypatch.setattr(hv, "dynamodb", Broken())
    out = hv._probe_ddb_direct()
    assert out["ok"] is False
    assert "throttled" in out["reason"]


def test_publish_disabled_is_noop(hv):
    hv._publish(False, False)
    assert hv.cloudwatch.list_metrics(Namespace="Continuum")["Metrics"] == []


def test_publish_enabled_writes_health_metric(hv):
    hv._publish(False, True)
    metrics = hv.cloudwatch.list_metrics(Namespace="Continuum")["Metrics"]
    assert any(m["MetricName"] == "Health" for m in metrics)
    dims = next(m for m in metrics if m["MetricName"] == "Health")["Dimensions"]
    assert dims == [{"Name": "Role", "Value": "primary"}]


def test_handler_scheduled_mode_returns_200_and_publishes(hv, capsys):
    result = hv.handler({"mode": "scheduled"}, None)
    assert result == {"statusCode": 200}
    assert hv.cloudwatch.list_metrics(Namespace="Continuum")["Metrics"]
    assert '"role": "primary"' in capsys.readouterr().out


def test_handler_force_mode_default_target_and_no_publish_when_url_given(hv, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", FakeUrlopen(FakeResp(200, {"healthy": True})))
    result = hv.handler({"mode": "force", "target_url": "http://x",
                         "publish_metric": False}, None)
    assert result["healthy"] is True
    assert result["mode"] == "force"
    assert result["target"] == "primary"
    assert hv.cloudwatch.list_metrics(Namespace="Continuum")["Metrics"] == []


def test_handler_force_mode_target_override(hv, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", FakeUrlopen(FakeResp(200, {"healthy": True})))
    result = hv.handler({"mode": "force", "target": "standby",
                         "target_url": "http://sb", "publish_metric": False}, None)
    assert result["target"] == "standby"


def test_handler_retry_once_still_unhealthy_after_retry(hv, monkeypatch):
    slept = []
    monkeypatch.setattr(hv.time, "sleep", lambda s: slept.append(s))
    monkeypatch.setattr(
        "urllib.request.urlopen",
        FakeUrlopen(urllib.error.URLError("down"), urllib.error.URLError("still down")))
    result = hv.handler({"mode": "force", "target_url": "http://x",
                         "publish_metric": False, "retry_once": True}, None)
    assert result["healthy"] is False
    assert slept == [10]
    assert "still down" in result["app_probe_retry"]["reason"]


def test_handler_retry_once_recovers_second_time(hv, monkeypatch):
    monkeypatch.setattr(hv.time, "sleep", lambda s: None)
    monkeypatch.setattr(
        "urllib.request.urlopen",
        FakeUrlopen(urllib.error.URLError("blip"), FakeResp(200, {"healthy": True})))
    result = hv.handler({"mode": "force", "target_url": "http://x",
                         "publish_metric": False, "retry_once": True,
                         "retry_delay_s": 0}, None)
    assert result["healthy"] is True


def test_handler_healthy_first_attempt_skips_retry(hv, monkeypatch):
    fake = FakeUrlopen(FakeResp(200, {"healthy": True}))
    monkeypatch.setattr("urllib.request.urlopen", fake)
    result = hv.handler({"mode": "force", "target_url": "http://x",
                         "publish_metric": False, "retry_once": True,
                         "retry_delay_s": 0}, None)
    assert result["healthy"] is True
    assert len(fake.calls) == 1
    assert "app_probe_retry" not in result


def test_handler_unhealthy_ddb_blocks_even_with_healthy_http(hv, monkeypatch):
    monkeypatch.setattr("urllib.request.urlopen", FakeUrlopen(FakeResp(200, {"healthy": True})))

    class Broken:
        def put_item(self, **kwargs):
            raise RuntimeError("no ddb")

    monkeypatch.setattr(hv, "dynamodb", Broken())
    result = hv.handler({"mode": "force", "target_url": "http://x",
                         "publish_metric": False}, None)
    assert result["healthy"] is False
    assert result["region_probe"]["ok"] is False
