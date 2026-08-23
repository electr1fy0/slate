"""Ops actions driving the failover / failback Step Functions workflows.

Every action either returns a truthful result or raises — the workflows rely
on States.TaskFailed to route failures into their Catch branches, so errors
are never swallowed into success-shaped payloads.
"""

import json
import os
import time
import uuid

import boto3

STANDBY_INSTANCE_ID = os.environ["STANDBY_INSTANCE_ID"]
PRIMARY_HEALTH_CHECK_ID = os.environ["PRIMARY_HEALTH_CHECK_ID"]
SOURCE_REGION = os.environ.get("SOURCE_REGION", "ap-south-1")
LOCAL_REGION = os.environ.get("AWS_REGION", "ap-south-1")
STANDBY_REGION = os.environ.get("STANDBY_REGION", "ap-southeast-1")
TABLE_NAME = os.environ["TABLE_NAME"]
COMMAND_WAIT_TIMEOUT_S = int(os.environ.get("COMMAND_WAIT_TIMEOUT_S", "180"))
COMMAND_POLL_INTERVAL_S = float(os.environ.get("COMMAND_POLL_INTERVAL_S", "5"))
PROMOTE_COMMAND = os.environ.get(
    "PROMOTE_COMMAND",
    "echo active > /etc/continuum/role && systemctl restart continuum && sleep 3 && "
    "curl -s http://localhost/health/live")
DEMOTE_COMMAND = os.environ.get(
    "DEMOTE_COMMAND",
    "echo standby > /etc/continuum/role && systemctl restart continuum && sleep 3 && "
    "curl -s http://localhost/health/live")

ssm = boto3.client("ssm", region_name=STANDBY_REGION)
route53 = boto3.client("route53")

TERMINAL_COMMAND_STATES = ("Success", "Failed", "Cancelled", "TimedOut")


def _run_verified_command(instance_id: str, command: str, comment: str) -> dict:
    """Run an SSM command to completion; raises unless it reports Success."""
    resp = ssm.send_command(
        InstanceIds=[instance_id],
        DocumentName="AWS-RunShellScript",
        Comment=comment,
        Parameters={"commands": [command]})
    command_id = resp["Command"]["CommandId"]
    deadline = time.monotonic() + COMMAND_WAIT_TIMEOUT_S
    while True:
        invocation = ssm.get_command_invocation(CommandId=command_id,
                                                InstanceId=instance_id)
        status = invocation["Status"]
        if status in TERMINAL_COMMAND_STATES:
            break
        if time.monotonic() > deadline:
            raise RuntimeError(
                f"ssm command {command_id} still {status!r} after "
                f"{COMMAND_WAIT_TIMEOUT_S}s")
        time.sleep(COMMAND_POLL_INTERVAL_S)
    if status != "Success":
        raise RuntimeError(
            f"ssm command {command_id} finished as {status!r}: "
            f"{(invocation.get('StandardErrorContent') or '')[:200]}")
    return {"promoted": True, "command_id": command_id, "instance": instance_id,
            "output": (invocation.get("StandardOutputContent") or "")[:500],
            "status": status}


def promote(payload):
    return _run_verified_command(STANDBY_INSTANCE_ID, PROMOTE_COMMAND,
                                 "Continuum: promote standby to active")


def demote(payload):
    result = _run_verified_command(STANDBY_INSTANCE_ID, DEMOTE_COMMAND,
                                   "Continuum: demote standby back to read-only")
    return {**result, "promoted": False, "demoted": True}


def traffic_shift(payload):
    inverted = bool(payload.get("inverted", True))
    response = route53.update_health_check(HealthCheckId=PRIMARY_HEALTH_CHECK_ID,
                                           Inverted=inverted)
    stored = (response.get("HealthCheckConfig") or {}).get("Inverted", inverted)
    if bool(stored) != inverted:
        raise RuntimeError(
            f"route53 did not confirm inversion={inverted} for {PRIMARY_HEALTH_CHECK_ID}")
    return {"traffic_shifted": True, "health_check": PRIMARY_HEALTH_CHECK_ID,
            "inverted": inverted}


def replication_status(payload):
    dynamodb = boto3.resource("dynamodb").Table(TABLE_NAME)
    probe_id = str(uuid.uuid4())
    now = int(time.time() * 1000)
    marker_key = {"pk": f"REPLMARKER#{probe_id}", "sk": "X"}
    started = time.perf_counter()
    while time.perf_counter() - started < 30:
        try:
            dynamodb.put_item(Item={**marker_key, "ts": now,
                                    "ttl": now // 1000 + 3600})
            got = dynamodb.get_item(Key=marker_key).get("Item")
            if got and got.get("ts") == now:
                dynamodb.delete_item(Key=marker_key)
                return {"replication_ok": True, "note": "local replica writable",
                        "elapsed_s": round(time.perf_counter() - started, 1)}
        except Exception as exc:
            raise RuntimeError(f"replication probe failed: {str(exc)[:200]}") from exc
        time.sleep(2)
    raise RuntimeError("replication probe timed out after 30s")


ACTIONS = {
    "promote": promote,
    "demote": demote,
    "traffic_shift": traffic_shift,
    "rollback_traffic": lambda p: traffic_shift({"inverted": False}),
    "replication_status": replication_status,
}


def handler(event, context):
    action = event.get("action")
    fn = ACTIONS.get(action)
    if not fn:
        # Raising (not returning) lets Step Functions Catch route this like
        # any other failure instead of mistaking it for success.
        raise ValueError(f"unknown action {action!r}; expected one of {sorted(ACTIONS)}")
    result = fn(event)
    print(json.dumps({"action": action, "result": result}))
    return result
