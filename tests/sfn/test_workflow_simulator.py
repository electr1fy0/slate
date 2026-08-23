import json
import sys
import time as time_mod
from pathlib import Path

import pytest
from boto3.dynamodb.conditions import Attr

sys.path.insert(0, str(Path(__file__).parent))
from simulator import Simulator  # noqa: E402

TABLE = "continuum"
VERIFIER = "__VERIFIER_ARN__"
OPS = "__OPS_ARN__"
RECONCILER = "__RECONCILER_ARN__"


class Probes:
    """Controls what the health verifier sees; last value sticks."""

    def __init__(self, results):
        self.results = list(results)
        self.http_calls = 0

    def _next(self):
        if len(self.results) > 1:
            return self.results.pop(0)
        return self.results[0]

    def http(self, target_url=None):
        self.http_calls += 1
        return {"ok": bool(self._next()), "latency_ms": 1}

    def ddb(self):
        return {"ok": True, "latency_ms": 1}


@pytest.fixture()
def env(aws, make_table, lambda_loader, monkeypatch):
    # Waits and verifier backoff sleeps are instant in simulation mode.
    monkeypatch.setattr("time.sleep", lambda seconds: None)
    # Lambdas run with AWS_REGION injected by the runtime; failover_ops
    # re-reads it when building clients inside handler invocations.
    monkeypatch.setenv("AWS_DEFAULT_REGION", "ap-south-1")
    monkeypatch.setenv("AWS_REGION", "ap-south-1")
    make_table(TABLE)

    import boto3

    hc_id = boto3.client("route53").create_health_check(
        CallerReference="sfn-sim",
        HealthCheckConfig={"Type": "HTTP", "FullyQualifiedDomainName": "primary.example.com",
                           "Port": 80, "ResourcePath": "/health/live"},
    )["HealthCheck"]["Id"]

    hv = lambda_loader("health_verifier", {"TABLE_NAME": TABLE})
    fo = lambda_loader("failover_ops", {
        "TABLE_NAME": TABLE,
        "STANDBY_INSTANCE_ID": "i-0standby000000000",
        "PRIMARY_HEALTH_CHECK_ID": hc_id,
        "STANDBY_REGION": "ap-southeast-1",
    })
    rec = lambda_loader("reconciler", {"TABLE_NAME": TABLE})
    return {"hv": hv, "fo": fo, "rec": rec, "hc_id": hc_id}


def wire_probes(env, probes, monkeypatch):
    monkeypatch.setattr(env["hv"], "_probe_http", probes.http)
    monkeypatch.setattr(env["hv"], "_probe_ddb_direct", probes.ddb)


def build_sim(env, execution_name="sim"):
    return Simulator(
        json.loads((Path(__file__).parents[2] / "statemachine" / "failover.asl.json")
                   .read_text()),
        lambdas={VERIFIER: env["hv"].handler,
                 OPS: env["fo"].handler,
                 RECONCILER: env["rec"].handler},
        execution_name=execution_name)


def subjects(sim):
    return [p["Subject"] for p in sim.sns_publishes]


def test_transient_blip_suppressed_when_primary_recovers(env, monkeypatch):
    probes = Probes([True])
    wire_probes(env, probes, monkeypatch)
    sim = build_sim(env).run()
    assert sim.outcome == "success"
    assert sim.history == ["VerifyCheck1", "StillDownAfterCheck1", "NotifyFalseAlarm"]
    assert subjects(sim) == ["[Continuum] Transient blip suppressed - no failover"]
    assert "NOT confirmed" in sim.sns_publishes[0]["Message"]


def test_full_failover_promotes_shifts_and_reconciles(env, monkeypatch):
    # three primary checks unhealthy (check 3 re-probes once) -> fail over;
    # two standby checks healthy
    probes = Probes([False, False, False, False, True, True])
    wire_probes(env, probes, monkeypatch)

    table = __import__("boto3").resource("dynamodb", region_name="ap-south-1").Table(TABLE)
    lost_write_ts = int(time_mod.time() * 1000)
    table.put_item(Item={
        "pk": "JOURNAL#lost-1", "sk": "OUTBOX",
        "target_pk": "SUBMISSION#cs101-midterm", "target_sk": "STUDENT#burst7",
        "version": 1, "request_id": "lost-1",
        "created_at": lost_write_ts, "answers": {"4": 1},
    })

    sim = build_sim(env, "failover-42").run()

    assert sim.outcome == "success"
    assert sim.context["promote"]["Payload"]["promoted"] is True
    assert sim.context["shift"]["Payload"]["inverted"] is True
    assert subjects(sim) == ["[Continuum] FAILOVER STARTING", "[Continuum] FAILOVER COMPLETE"]

    stored = sim.context["replication"]["Payload"]  # replication check ran before promote
    assert stored["replication_ok"] is True

    reconciliation = sim.context["reconciliation"]["Payload"]
    assert reconciliation["scanned"] == 1
    assert reconciliation["repaired"] == 1
    assert reconciliation["repaired_ids"] == ["lost-1"]

    replayed = table.get_item(Key={"pk": "SUBMISSION#cs101-midterm",
                                   "sk": "STUDENT#burst7"})["Item"]
    assert replayed["request_id"] == "lost-1"
    assert replayed["reconciled"] is True

    hc = __import__("boto3").client("route53").get_health_check(
        HealthCheckId=env["hc_id"])["HealthCheck"]
    assert hc["HealthCheckConfig"]["Inverted"] is True


def test_standby_verification_failure_triggers_rollback_and_execution_fail(env, monkeypatch):
    probes = Probes([False])  # everything stays unhealthy
    wire_probes(env, probes, monkeypatch)

    sim = build_sim(env).run()

    assert sim.outcome == "failed"
    assert sim.failure["error"] == "StandbyVerificationFailed"
    assert sim.history[-2:] == ["NotifyRolledBack", "FailExecution"]
    assert subjects(sim)[-1] == "[Continuum] FAILOVER ROLLED BACK"

    hc = __import__("boto3").client("route53").get_health_check(
        HealthCheckId=env["hc_id"])["HealthCheck"]
    assert hc["HealthCheckConfig"]["Inverted"] is False


def test_promote_failure_after_retries_routes_to_catch_notify_failed(env, monkeypatch):
    probes = Probes([False])  # everything stays unhealthy
    wire_probes(env, probes, monkeypatch)
    attempts = []
    fo_handler = env["fo"].handler

    def flaky_ops(payload, context=None):
        if payload.get("action") == "promote":
            attempts.append(payload["action"])
            raise RuntimeError("ssm unreachable")
        return fo_handler(payload, context)

    sim_json = json.loads((Path(__file__).parents[2] / "statemachine" /
                           "failover.asl.json").read_text())
    sim = Simulator(sim_json, lambdas={VERIFIER: env["hv"].handler,
                                       OPS: flaky_ops,
                                       RECONCILER: env["rec"].handler}).run()

    assert attempts.count("promote") == 3  # initial + MaxAttempts 2 retries
    assert sim.outcome == "success"  # workflow completed via Catch branch
    assert sim.context["promote_error"] == {"Error": "States.TaskFailed",
                                            "Cause": "ssm unreachable"}
    assert sim.history[-2:] == ["PromoteStandby", "NotifyFailed"]
    assert subjects(sim) == ["[Continuum] FAILOVER STARTING",
                             "[Continuum] FAILOVER WORKFLOW FAILED"]


def test_reconciler_failure_is_caught_and_does_not_block_success_notification(env, monkeypatch):
    class ExplodingReconciler:
        def handler(self, event, context):
            raise RuntimeError("reconciler crashed")

    # primary must fail all checks (incl. the check-3 re-probe) so the flow
    # reaches reconciliation with a healthy standby
    probes = Probes([False, False, False, False, True, True])
    wire_probes(env, probes, monkeypatch)
    sim_json = json.loads((Path(__file__).parents[2] / "statemachine" /
                           "failover.asl.json").read_text())
    sim = Simulator(sim_json, lambdas={VERIFIER: env["hv"].handler,
                                       OPS: env["fo"].handler,
                                       RECONCILER: ExplodingReconciler().handler}).run()
    assert sim.outcome == "success"
    assert sim.context["reconciliation_error"]["Error"] == "States.TaskFailed"
    assert sim.history[-2:] == ["ReconcileInFlightWrites", "NotifySuccess"]
