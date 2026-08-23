import json
import os

import boto3

STANDBY_INSTANCE_ID = os.environ["STANDBY_INSTANCE_ID"]
PRIMARY_HEALTH_CHECK_ID = os.environ["PRIMARY_HEALTH_CHECK_ID"]
SOURCE_REGION = os.environ.get("SOURCE_REGION", "ap-south-1")
LOCAL_REGION = os.environ.get("AWS_REGION", "ap-south-1")
STANDBY_REGION = os.environ.get("STANDBY_REGION", "ap-southeast-1")
TABLE_NAME = os.environ["TABLE_NAME"]
PROMOTE_COMMAND = os.environ.get(
    "PROMOTE_COMMAND",
    "echo active > /etc/continuum/role && systemctl restart continuum && sleep 3 && "
    "curl -s http://localhost/health/live")

ssm = boto3.client("ssm", region_name=STANDBY_REGION)
route53 = boto3.client("route53")


def promote(payload):
    resp = ssm.send_command(
        InstanceIds=[STANDBY_INSTANCE_ID],
        DocumentName="AWS-RunShellScript",
        Comment="Continuum: promote standby to active",
        Parameters={"commands": [PROMOTE_COMMAND]})
    return {"promoted": True, "command_id": resp["Command"]["CommandId"],
            "instance": STANDBY_INSTANCE_ID}


def traffic_shift(payload):
    inverted = bool(payload.get("inverted", True))
    route53.update_health_check(HealthCheckId=PRIMARY_HEALTH_CHECK_ID, Inverted=inverted)
    return {"traffic_shifted": True, "health_check": PRIMARY_HEALTH_CHECK_ID,
            "inverted": inverted}


def replication_status(payload):
    import time
    import uuid
    dynamodb = boto3.resource("dynamodb").Table(TABLE_NAME)
    probe_id = str(uuid.uuid4())
    now = int(time.time() * 1000)
    marker_key = {"pk": f"REPLMARKER#{probe_id}", "sk": "X"}
    started = time.perf_counter()
    while time.perf_counter() - started < 30:
        try:
            dynamodb.put_item(Item={**marker_key, "ts": now})
            got = dynamodb.get_item(Key=marker_key).get("Item")
            if got and got.get("ts") == now:
                dynamodb.delete_item(Key=marker_key)
                return {"replication_ok": True, "note": "local replica writable",
                        "elapsed_s": round(time.perf_counter() - started, 1)}
        except Exception as exc:
            return {"replication_ok": False, "reason": str(exc)[:200]}
        time.sleep(2)
    return {"replication_ok": False, "reason": "timeout"}


ACTIONS = {
    "promote": promote,
    "traffic_shift": traffic_shift,
    "rollback_traffic": lambda p: traffic_shift({"inverted": False}),
    "replication_status": replication_status,
}


def handler(event, context):
    action = event.get("action")
    fn = ACTIONS.get(action)
    if not fn:
        return {"error": f"unknown action {action!r}", "allowed": sorted(ACTIONS)}
    result = fn(event)
    print(json.dumps({"action": action, "result": result}))
    return result
