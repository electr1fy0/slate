import pytest
from boto3.dynamodb.conditions import Attr

TABLE = "continuum"
ENV = {
    "TABLE_NAME": TABLE,
    "STANDBY_INSTANCE_ID": "i-0standby000000000",
    "PRIMARY_HEALTH_CHECK_ID": "hc-primary000000",
    "SOURCE_REGION": "ap-south-1",
    "LOCAL_REGION": "ap-south-1",
    "STANDBY_REGION": "ap-southeast-1",
}


@pytest.fixture()
def fo_factory(lambda_loader, make_table):
    make_table(TABLE)

    def _make(env_extra=None):
        return lambda_loader("failover_ops", {**ENV, **(env_extra or {})})

    return _make


@pytest.fixture()
def fo(fo_factory):
    return fo_factory()


@pytest.fixture()
def primary_health_check_id(aws):
    import boto3

    route53 = boto3.client("route53")
    hc = route53.create_health_check(
        CallerReference="continuum-test-suite",
        HealthCheckConfig={
            "Type": "HTTP",
            "FailureThreshold": 3,
            "FullyQualifiedDomainName": "primary.example.com",
            "Port": 80,
            "ResourcePath": "/health/live",
        })["HealthCheck"]
    return hc["Id"]


def test_promote_sends_ssm_command(fo):
    out = fo.handler({"action": "promote"}, None)
    assert out == {"promoted": True, "command_id": out["command_id"],
                   "instance": ENV["STANDBY_INSTANCE_ID"]}
    assert out["command_id"]


def test_traffic_shift_defaults_to_inverted(fo_factory, primary_health_check_id):
    fo = fo_factory({"PRIMARY_HEALTH_CHECK_ID": primary_health_check_id})
    out = fo.handler({"action": "traffic_shift"}, None)
    assert out == {"traffic_shifted": True,
                   "health_check": primary_health_check_id, "inverted": True}
    stored = fo.route53.get_health_check(HealthCheckId=primary_health_check_id)["HealthCheck"]
    assert stored["HealthCheckConfig"]["Inverted"] is True


def test_rollback_traffic_uninverts_health_check(fo_factory, primary_health_check_id):
    fo = fo_factory({"PRIMARY_HEALTH_CHECK_ID": primary_health_check_id})
    out = fo.handler({"action": "rollback_traffic"}, None)
    assert out["inverted"] is False
    stored = fo.route53.get_health_check(HealthCheckId=primary_health_check_id)["HealthCheck"]
    assert stored["HealthCheckConfig"]["Inverted"] is False


def test_traffic_shift_explicit_payload_value(fo_factory, primary_health_check_id):
    fo = fo_factory({"PRIMARY_HEALTH_CHECK_ID": primary_health_check_id})
    out = fo.handler({"action": "traffic_shift", "inverted": False}, None)
    assert out["inverted"] is False


def test_replication_status_local_replica_writable(fo, monkeypatch):
    monkeypatch.setenv("AWS_DEFAULT_REGION", ENV["SOURCE_REGION"])
    out = fo.handler({"action": "replication_status"}, None)
    assert out["replication_ok"] is True
    assert out["note"] == "local replica writable"
    assert out["elapsed_s"] >= 0
    import boto3
    table = boto3.resource("dynamodb", region_name=ENV["SOURCE_REGION"]).Table(TABLE)
    markers = table.scan(FilterExpression=Attr("pk").begins_with("REPLMARKER#"))["Items"]
    assert markers == []


def test_replication_status_timeout_when_reads_never_match(fo, monkeypatch):
    monkeypatch.setenv("AWS_DEFAULT_REGION", ENV["SOURCE_REGION"])

    class LaggingTable:
        def __init__(self):
            self.sleeps = 0

        def put_item(self, **kwargs):
            return {}

        def get_item(self, **kwargs):
            # marker read back with a stale timestamp -> replication lagging
            return {"Item": {"ts": -1}}

    lagging = LaggingTable()

    class FakeResource:
        def __init__(self, *args, **kwargs):
            pass

        def Table(self, name):
            return lagging

    import time as time_mod

    clock = iter([0.0, 0.0, 31.0])  # started, first check, post-sleep check
    monkeypatch.setattr(fo.boto3, "resource", FakeResource)
    monkeypatch.setattr(time_mod, "perf_counter", lambda: next(clock))
    original_sleep = time_mod.sleep

    def tracked_sleep(seconds):
        lagging.sleeps += 1

    monkeypatch.setattr(time_mod, "sleep", tracked_sleep)
    out = fo.handler({"action": "replication_status"}, None)
    assert out == {"replication_ok": False, "reason": "timeout"}
    assert lagging.sleeps == 1  # backed off between polls
    monkeypatch.setattr(time_mod, "sleep", original_sleep)


def test_replication_status_reports_write_errors(fo, monkeypatch):
    class BrokenTable:
        def put_item(self, **kwargs):
            raise RuntimeError("dynamodb unavailable")

        def get_item(self, **kwargs):
            raise AssertionError("should not be reached")

    class FakeResource:
        def __init__(self, *args, **kwargs):
            pass

        def Table(self, name):
            return BrokenTable()

    monkeypatch.setattr(fo.boto3, "resource", FakeResource)
    out = fo.handler({"action": "replication_status"}, None)
    assert out["replication_ok"] is False
    assert "unavailable" in out["reason"]


def test_unknown_action_lists_allowed_actions(fo):
    out = fo.handler({"action": "detonate"}, None)
    assert out["error"] == "unknown action 'detonate'"
    assert out["allowed"] == ["promote", "replication_status", "rollback_traffic",
                              "traffic_shift"]


def test_handler_logs_dispatched_action_result(fo, capsys):
    fo.handler({"action": "promote"}, None)
    printed = capsys.readouterr().out
    assert '"action": "promote"' in printed
    assert '"promoted": true' in printed
