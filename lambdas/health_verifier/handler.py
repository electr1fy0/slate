import json
import os
import time
import urllib.request

import boto3

TABLE_NAME = os.environ["TABLE_NAME"]
TARGET_URL = os.environ.get("TARGET_URL", "")
ROLE_LABEL = os.environ.get("ROLE_LABEL", "primary")
METRIC_NAMESPACE = os.environ.get("METRIC_NAMESPACE", "Continuum")
PROBE_TIMEOUT_S = float(os.environ.get("PROBE_TIMEOUT_S", "8"))

cloudwatch = boto3.client("cloudwatch")
dynamodb = boto3.resource("dynamodb").Table(TABLE_NAME)


def _probe_http(target_url: str | None = None) -> dict:
    url = (target_url or TARGET_URL).rstrip("/")
    if not url:
        return {"ok": False, "reason": "no target url"}
    url = f"{url}/health/deep"
    started = time.perf_counter()
    try:
        with urllib.request.urlopen(url, timeout=PROBE_TIMEOUT_S) as resp:
            body = json.loads(resp.read().decode())
            latency = round((time.perf_counter() - started) * 1000, 1)
            ok = resp.status == 200 and body.get("healthy") is True
            return {"ok": ok, "http_status": resp.status, "latency_ms": latency,
                    "detail": body.get("checks", {})}
    except Exception as exc:
        return {"ok": False, "reason": str(exc)[:200]}


def _probe_ddb_direct() -> dict:
    import uuid
    probe_id = str(uuid.uuid4())
    now = int(time.time() * 1000)
    key = {"pk": "HEARTBEAT", "sk": f"PROBE#lambda-{ROLE_LABEL}-{probe_id}"}
    started = time.perf_counter()
    try:
        dynamodb.put_item(Item={**key, "ts": now,
                                "ttl": now // 1000 + 300})
        got = dynamodb.get_item(Key=key, ConsistentRead=True).get("Item")
        latency = round((time.perf_counter() - started) * 1000, 1)
        dynamodb.delete_item(Key=key)
        return {"ok": bool(got), "latency_ms": latency}
    except Exception as exc:
        return {"ok": False, "reason": str(exc)[:200]}


def _publish(ok: bool, enabled: bool):
    if not enabled:
        return
    try:
        cloudwatch.put_metric_data(
            Namespace=METRIC_NAMESPACE,
            MetricData=[{"MetricName": "Health",
                         "Dimensions": [{"Name": "Role", "Value": ROLE_LABEL}],
                         "Value": 1 if ok else 0}])
    except Exception as exc:
        # A CloudWatch outage must never flip the probe result itself.
        print(json.dumps({"warning": "metric publish failed", "reason": str(exc)[:200]}))


def handler(event, context):
    mode = event.get("mode", "scheduled")
    target_url = event.get("target_url")
    publish = bool(event.get("publish_metric", target_url is None))

    http_result = _probe_http(target_url)
    ddb_result = _probe_ddb_direct()
    healthy = http_result["ok"] and ddb_result["ok"]
    _publish(healthy, publish)

    result = {"healthy": healthy, "target": event.get("target", ROLE_LABEL), "mode": mode,
              "region_probe": ddb_result, "app_probe": http_result}

    if mode == "scheduled":
        print(json.dumps(result))
        return {"statusCode": 200, "body": json.dumps(result)}

    if not healthy and event.get("retry_once"):
        time.sleep(event.get("retry_delay_s", 10))
        http_result = _probe_http(target_url)
        ddb_result = _probe_ddb_direct()
        healthy = http_result["ok"] and ddb_result["ok"]
        result.update({"healthy": healthy,
                       "app_probe_retry": http_result, "ddb_probe_retry": ddb_result})
    return result