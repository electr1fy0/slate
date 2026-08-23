import json
from pathlib import Path

import pytest

ASL_PATH = Path(__file__).resolve().parents[2] / "statemachine" / "failover.asl.json"

PLACEHOLDERS = ["__VERIFIER_ARN__", "__OPS_ARN__", "__RECONCILER_ARN__",
                "__SNS_TOPIC_ARN__", "__STANDBY_URL__"]


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


def test_document_parses_with_comment(asl):
    assert "failover" in asl["Comment"].lower()


def test_start_state_exists(asl):
    assert asl["StartAt"] in asl["States"]


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
    assert not unreachable, f"unreachable states: {sorted(unreachable)}"


def test_terminal_states(asl):
    endings = {name for name, s in asl["States"].items()
               if s.get("End") or s["Type"] == "Fail"}
    assert endings == {"NotifyFalseAlarm", "NotifySuccess",
                       "FailExecution", "FailExecutionReconcile",
                       "FailExecutionWorkflow"}


def test_every_task_has_a_timeout(asl):
    for name, state in asl["States"].items():
        if state["Type"] != "Task":
            continue
        timeout = state.get("TimeoutSeconds")
        assert timeout and timeout > 0, f"{name} lacks TimeoutSeconds"


def test_verify_tasks_catch_infrastructure_failure_into_notify_failed(asl):
    states = asl["States"]
    for name in ("VerifyCheck1", "VerifyCheck2", "VerifyCheck3"):
        catches = states[name].get("Catch", [])
        assert any(c["Next"] == "NotifyFailed" for c in catches), \
            f"{name} cannot route verifier crashes to a human"


def test_failed_reconciliation_does_not_announce_success(asl):
    catch = next(c for c in asl["States"]["ReconcileInFlightWrites"]["Catch"])
    assert catch["Next"] == "NotifyReconciliationFailed"
    fail = asl["States"]["FailExecutionReconcile"]
    assert fail["Type"] == "Fail"
    assert "reconciliation" in fail["Error"].lower() or \
        "ReconciliationFailed" == fail["Error"]


def test_workflow_failure_path_ends_in_fail_state_not_success(asl):
    # NotifyFailed must not End:true — that would report SUCCEEDED.
    notify = asl["States"]["NotifyFailed"]
    assert not notify.get("End")
    assert notify["Next"] == "FailExecutionWorkflow"
    fail = asl["States"]["FailExecutionWorkflow"]
    assert fail["Type"] == "Fail"
    assert "rolled back or left untouched" in fail["Cause"] or \
        "FailoverWorkflowFailed" == fail["Error"]


def test_fail_state_carries_error_and_cause(asl):
    fail = asl["States"]["FailExecution"]
    assert fail["Error"] == "StandbyVerificationFailed"
    assert "rolled back" in fail["Cause"]


def test_placeholders_still_present_pre_render(asl):
    raw = ASL_PATH.read_text()
    for placeholder in PLACEHOLDERS:
        assert placeholder in raw


def test_lambda_tasks_define_retry_policy(asl):
    for name, state in asl["States"].items():
        if state["Type"] != "Task":
            continue
        resource = state["Resource"]
        if resource.endswith(":lambda:invoke"):
            assert state.get("Retry"), f"{name} lacks Retry policy"
        else:
            assert resource == "arn:aws:states:::sns:publish"


def test_verification_gates_use_three_primary_checks_then_two_standby_checks(asl):
    states = asl["States"]
    primary_payloads = [states[f"VerifyCheck{i}"]["Parameters"]["Payload"]
                        for i in (1, 2, 3)]
    assert all(p["target"] == "primary" and p["mode"] == "force"
               for p in primary_payloads)
    assert primary_payloads[2]["retry_once"] is True
    assert all("retry_once" not in p for p in primary_payloads[:2])

    standby_payloads = [states["VerifyStandby1"]["Parameters"]["Payload"],
                        states["VerifyStandby2"]["Parameters"]["Payload"]]
    assert all(p["target"] == "standby" and p["publish_metric"] is False
               for p in standby_payloads)


def test_wait_durations_match_runbook(asl):
    expected = {"Wait45s": 45, "Wait30s": 30, "Wait15s": 15,
                "WaitPromotion": 25, "WaitDnsPropagation": 60}
    for name, seconds in expected.items():
        assert asl["States"][name] == {"Type": "Wait", "Seconds": seconds,
                                       "Next": asl["States"][name]["Next"]}
        assert seconds > 0
