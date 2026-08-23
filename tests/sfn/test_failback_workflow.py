"""Structural checks plus offline simulation of statemachine/failback.asl.json."""
import json
import sys
from pathlib import Path

import pytest

sys.path.insert(0, str(Path(__file__).parents[1] / "sfn"))
from simulator import Simulator  # noqa: E402

from test_workflow_simulator import (  # noqa: E402,F401 - reuse fixtures/helpers
    OPS, RECONCILER, VERIFIER, Probes, env, wire_probes,
)

ASL_PATH = Path(__file__).resolve().parents[2] / "statemachine" / "failback.asl.json"
PLACEHOLDERS = ["__VERIFIER_ARN__", "__OPS_ARN__", "__RECONCILER_ARN__",
                "__SNS_TOPIC_ARN__", "__PRIMARY_URL__"]


@pytest.fixture(scope="module")
def asl():
    return json.loads(ASL_PATH.read_text())


def _targets(state):
    found = []
    if "Next" in state:
        found.append(state["Next"])
    for choice in state.get("Choices", []):
        found.append(choice["Next"])
    if "Default" in state:
        found.append(state["Default"])
    for catch in state.get("Catch", []):
        found.append(catch["Next"])
    return found


def test_document_parses(asl):
    assert "failback" in asl["Comment"].lower()


def test_every_transition_target_exists(asl):
    states = asl["States"]
    for name, state in states.items():
        for target in _targets(state):
            assert target in states, f"{name} -> missing target {target}"


def test_all_states_reachable_from_start(asl):
    states = asl["States"]
    seen = {asl["StartAt"]}
    frontier = [asl["StartAt"]]
    while frontier:
        current = frontier.pop()
        for target in _targets(states[current]):
            if target not in seen:
                seen.add(target)
                frontier.append(target)
    unreachable = set(states) - seen
    assert not unreachable


def test_every_task_has_a_timeout_and_lambda_tasks_have_retry(asl):
    for name, state in asl["States"].items():
        if state["Type"] != "Task":
            continue
        assert state.get("TimeoutSeconds", 0) > 0, f"{name} lacks TimeoutSeconds"
        if state["Resource"].endswith(":lambda:invoke"):
            assert state.get("Retry"), f"{name} lacks Retry policy"


def test_placeholders_present_pre_render():
    raw = ASL_PATH.read_text()
    for placeholder in PLACEHOLDERS:
        assert placeholder in raw


def test_no_automatic_trigger_is_documented_by_manual_only_entry(asl):
    """Failback must start from an operator decision, not an alarm."""
    assert asl["StartAt"] == "NotifyFailbackStarting"


# ------------------------------------------------------------------ simulation

def test_primary_unhealthy_aborts_before_any_destructive_action(env, monkeypatch):
    probes = Probes([False])  # primary never recovers
    wire_probes(env, probes, monkeypatch)
    sim = Simulator(json.loads(ASL_PATH.read_text()),
                    lambdas={VERIFIER: env["hv"].handler,
                             OPS: env["fo"].handler,
                             RECONCILER: env["rec"].handler}).run()

    # Success outcome: the workflow did its job by refusing to act.
    assert sim.outcome == "success"
    actions = [call for call in sim.invocations if call.get("action")]
    assert all(call["action"] != "demote" for call in actions)
    assert "DemoteStandby" not in sim.history
    assert subjects_of(sim) == ["[Continuum] FAILBACK STARTING",
                                "[Continuum] FAILBACK ABORTED - PRIMARY NOT READY"]


def test_happy_path_demotes_standby_restores_traffic_and_completes(env, monkeypatch):
    # Primary healthy on both pre-checks and both post-shift serving checks;
    # replication probe passes via moto.
    probes = Probes([True, True, True, True])
    wire_probes(env, probes, monkeypatch)
    sim = Simulator(json.loads(ASL_PATH.read_text()),
                    lambdas={VERIFIER: env["hv"].handler,
                             OPS: env["fo"].handler,
                             RECONCILER: env["rec"].handler}).run()

    assert sim.outcome == "success"
    assert "DemoteStandby" in sim.history
    assert "RestoreTrafficShift" in sim.history
    assert sim.history[-1] == "NotifyFailbackComplete"
    assert subjects_of(sim)[-1] == "[Continuum] FAILBACK COMPLETE"


def test_failed_post_shift_verification_reinverts_traffic(env, monkeypatch):
    # Primary healthy pre-checks, then fails both post-restore checks.
    probes = Probes([True, True, False, False])
    wire_probes(env, probes, monkeypatch)
    sim = Simulator(json.loads(ASL_PATH.read_text()),
                    lambdas={VERIFIER: env["hv"].handler,
                             OPS: env["fo"].handler,
                             RECONCILER: env["rec"].handler}).run()

    assert sim.outcome == "failed"
    assert sim.failure["error"] == "PrimaryVerificationFailed"
    assert "RollForwardTraffic" in sim.history
    assert subjects_of(sim)[-1] == "[Continuum] FAILBACK ROLLED BACK"


def test_reconciliation_failure_after_failback_requires_manual_action(
        env, monkeypatch):
    class ExplodingReconciler:
        def handler(self, event, context):
            raise RuntimeError("reconciler crashed")

    probes = Probes([True, True, True, True])
    wire_probes(env, probes, monkeypatch)
    sim = Simulator(json.loads(ASL_PATH.read_text()),
                    lambdas={VERIFIER: env["hv"].handler,
                             OPS: env["fo"].handler,
                             RECONCILER: ExplodingReconciler().handler}).run()

    assert sim.outcome == "failed"
    assert sim.failure["error"] == "ReconciliationFailed"
    assert subjects_of(sim)[-1] == \
        "[Continuum] RECONCILIATION FAILED - MANUAL ACTION REQUIRED"


def subjects_of(sim):
    return [p["Subject"] for p in sim.sns_publishes]
