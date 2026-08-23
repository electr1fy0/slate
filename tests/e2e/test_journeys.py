"""End-to-end journeys mirroring docs/demo-script.md T1-T5.

Everything runs locally: the real FastAPI app, the real lambda handlers and
the real ASL graph, with AWS mocked by moto.
"""
import json
import sys
import time as time_mod
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

import main
from config import settings

sys.path.insert(0, str(Path(__file__).parents[1] / "sfn"))
from simulator import Simulator  # noqa: E402

TABLE = "continuum"
VERIFIER = "__VERIFIER_ARN__"
OPS = "__OPS_ARN__"
RECONCILER = "__RECONCILER_ARN__"


def auth():
    return {"x-admin-token": settings.admin_token}


def student_headers(client, email="journey-student@example.com"):
    resp = client.post("/auth/register", json={
        "email": email, "password": "hunter2hunter2", "name": "Journey Student"})
    assert resp.status_code == 201
    return {"Authorization": f"Bearer {resp.json()['access_token']}"}


class LivePrimaryProbes:
    """Verifier probes that hit the real app for the primary and a switchable
    standby-health flag (the single local app plays both nodes)."""

    def __init__(self, client):
        self.client = client
        self.standby_healthy = True
        self.primary_calls = 0
        self.standby_calls = 0

    def __call__(self, target_url=None):
        if target_url and "standby" in str(target_url).lower():
            self.standby_calls += 1
            return {"ok": self.standby_healthy, "latency_ms": 1}
        self.primary_calls += 1
        resp = self.client.get("/health/deep")
        healthy = resp.status_code == 200 and bool(resp.json().get("healthy"))
        return {"ok": healthy,
                "http_status": resp.status_code,
                "latency_ms": 1}

    def ddb(self):
        return {"ok": True, "latency_ms": 1}


@pytest.fixture()
def world(aws, make_table, tmp_path, lambda_loader, monkeypatch):
    make_table(TABLE)
    monkeypatch.setattr("time.sleep", lambda seconds: None)
    # Host-dependent deep-check inputs are normalised so journeys are
    # about failover behaviour, not this machine's disk or load.
    from types import SimpleNamespace
    monkeypatch.setattr(main.shutil, "disk_usage",
                        lambda path: SimpleNamespace(total=100, used=40))
    monkeypatch.setattr(main.os, "getloadavg", lambda: (1.0, 1.0, 1.0))
    settings.role_file = str(tmp_path / "role")

    import boto3

    hc_id = boto3.client("route53").create_health_check(
        CallerReference="e2e-journey",
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

    probes = LivePrimaryProbes(None)
    monkeypatch.setattr(hv, "_probe_http", probes.__call__)
    monkeypatch.setattr(hv, "_probe_ddb_direct", probes.ddb)

    asl = json.loads((Path(__file__).parents[2] / "statemachine" /
                      "failover.asl.json").read_text())

    def run_failover_workflow():
        sim = Simulator(asl,
                        lambdas={VERIFIER: hv.handler, OPS: fo.handler,
                                 RECONCILER: rec.handler},
                        execution_name="e2e-journey")
        return sim.run()

    table = boto3.resource("dynamodb", region_name=settings.region).Table(TABLE)

    with TestClient(main.app) as client:
        probes.client = client
        yield {
            "client": client,
            "table": table,
            "hc_id": hc_id,
            "fo": fo,
            "rec": rec,
            "probes": probes,
            "run_failover": run_failover_workflow,
        }


def inject_lost_writes(table, count=5, exam="cs101-midterm"):
    now = int(time_mod.time() * 1000)
    for i in range(count):
        rid = f"lost-{i}"
        table.put_item(Item={
            "pk": f"JOURNAL#{rid}", "sk": "OUTBOX",
            "target_pk": f"SUBMISSION#{exam}", "target_sk": f"STUDENT#burst{i}",
            "version": 1, "request_id": rid, "created_at": now,
            "answers": {"0": i % 2},
        })


def subjects(result):
    return [p["Subject"] for p in result.sns_publishes]


@pytest.mark.e2e
def test_t2_degraded_primary_fails_over_and_reconciles_in_flight_writes(world):
    client, table = world["client"], world["table"]

    # Baseline: exam served, one clean submission lands.
    headers = student_headers(client)
    assert client.get("/exam/cs101-midterm",
                      headers=headers).status_code == 200
    first = client.post("/exam/cs101-midterm/submit", headers=headers,
                        json={"answers": {"0": 1}}).json()
    assert first["status"] == "committed"

    # Chaos: primary starts failing most requests (T2 degradation).
    # A deterministic roll forces the middleware down every non-exempt call;
    # the resulting 500s fill the sliding error-rate window.
    import random as random_mod
    world["client"].post("/admin/chaos", headers=auth(), json={"error_pct": 100})
    original_randint = random_mod.randint
    random_mod.randint = lambda a, b: 1
    try:
        for i in range(60):
            client.get(f"/exam/cs101-midterm?v={i}", headers=headers)
    finally:
        random_mod.randint = original_randint
    # Stop injecting new errors; the window remembers the carnage.
    client.post("/admin/chaos", headers=auth(), json={"error_pct": 0})

    # A ping-style check stays green...
    assert client.get("/health/live").status_code == 200
    # ...while the deep check catches the error-rate breach.
    deep = client.get("/health/deep")
    assert deep.status_code == 503
    assert deep.json()["checks"]["error_rate"]["ok"] is False

    # Writes accepted around this window may not replicate: seed outbox rows
    # representing in-flight submissions lost at commit time (T4 setup).
    inject_lost_writes(table, count=4)

    # Verified failover through the real workflow.
    result = world["run_failover"]()
    assert result.outcome == "success"
    assert subjects(result) == ["[Continuum] FAILOVER STARTING",
                                "[Continuum] FAILOVER COMPLETE"]
    assert result.context["promote"]["Payload"]["promoted"] is True

    reconciliation = result.context["reconciliation"]["Payload"]
    assert reconciliation["scanned"] == 5  # 4 injected + the baseline journal row
    assert sorted(reconciliation["repaired_ids"]) == [f"lost-{i}" for i in range(4)]
    assert reconciliation["already_replicated"] == 1  # baseline write had replicated

    # Repaired submissions are visible to admins on the promoted node.
    client.post("/admin/chaos/reset", headers=auth())
    listed = client.get("/admin/submissions", headers=auth(),
                        params={"limit": 100}).json()
    sks = {item["sk"] for item in listed}
    assert {f"STUDENT#burst{i}" for i in range(4)} <= sks
    assert any(item["sk"].startswith("STUDENT#") and item.get("student_name")
               == "Journey Student" for item in listed)


@pytest.mark.e2e
def test_t4_reconciliation_is_idempotent(world):
    table = world["table"]
    inject_lost_writes(table, count=3)

    first = world["rec"].handler({}, None)
    assert first["repaired"] == 3

    second = world["rec"].handler({}, None)
    assert second["scanned"] == 3
    assert second["already_replicated"] == 3
    assert second["repaired"] == 0
    assert second["conflicts"] == []


@pytest.mark.e2e
def test_t3_db_stall_kills_platform_while_process_stays_alive(world):
    client = world["client"]

    # Process is alive: pages and liveness respond.
    assert client.get("/health/live").status_code == 200

    # Stall the database layer at the application level.
    client.post("/admin/chaos", headers=auth(), json={"db_stall": True})

    deep = client.get("/health/deep")
    assert deep.status_code == 503
    body = deep.json()
    assert body["checks"]["ddb"] == {"ok": False, "reason": "db_stall chaos flag"}
    # The verifier's live probe bridge sees an unhealthy primary.
    assert world["probes"](target_url=None)["ok"] is False

    # Clearing the stall restores a green deep check.
    client.post("/admin/chaos/reset", headers=auth())
    assert client.get("/health/deep").status_code == 200


@pytest.mark.e2e
def test_t5_transient_blip_is_suppressed_no_failover(world, lambda_loader, monkeypatch):
    """A short outage heals before verification completes -> no failover."""
    hv = lambda_loader("health_verifier", {"TABLE_NAME": TABLE})
    sequence = [False, True]  # down during check1, recovered afterwards
    monkeypatch.setattr(hv, "_probe_http",
                        lambda url=None: {"ok": sequence.pop(0) if len(sequence) > 1
                                          else sequence[0], "latency_ms": 1})
    monkeypatch.setattr(hv, "_probe_ddb_direct", lambda: {"ok": True, "latency_ms": 1})

    asl = json.loads((Path(__file__).parents[2] / "statemachine" /
                      "failover.asl.json").read_text())
    from simulator import Simulator as Sim

    sim = Sim(asl, lambdas={VERIFIER: hv.handler,
                            OPS: world["fo"].handler,
                            RECONCILER: world["rec"].handler}).run()

    assert sim.outcome == "success"
    assert sim.history == ["VerifyCheck1", "StillDownAfterCheck1", "Wait45s",
                           "VerifyCheck2", "StillDownAfterCheck2",
                           "NotifyFalseAlarm"]
    assert subjects(sim) == ["[Continuum] Transient blip suppressed - no failover"]
    # Traffic never shifted: health check remains un-inverted.
    import boto3
    hc = boto3.client("route53").get_health_check(
        HealthCheckId=world["hc_id"])["HealthCheck"]
    assert hc["HealthCheckConfig"]["Inverted"] is False


@pytest.mark.e2e
def test_t1_standby_serves_nothing_until_promoted(world):
    client = world["client"]
    headers = student_headers(client)

    # Standby posture: read-only everywhere that matters.
    Path(settings.role_file).write_text("standby")
    assert client.get("/exam/cs101-midterm", headers=headers).status_code == 503
    assert client.post("/exam/cs101-midterm/submit", headers=headers,
                       json={"answers": {"0": 1}}).status_code == 503
    assert "STANDBY NODE" in client.get("/").text

    # Promotion flips the role file and opens the gates.
    body = client.post("/admin/promote", headers=auth()).json()
    assert body["role"] == "active"
    exam = client.get("/exam/cs101-midterm", headers=headers)
    assert exam.status_code == 200
    submit = client.post("/exam/cs101-midterm/submit", headers=headers,
                         json={"answers": {"2": 1}})
    assert submit.json()["status"] == "committed"
