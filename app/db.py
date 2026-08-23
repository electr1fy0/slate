import time
import uuid
from datetime import datetime, timezone

import boto3
from boto3.dynamodb.conditions import Attr

from config import settings

_ddb = None


def table():
    global _ddb
    if _ddb is None:
        kwargs = {"region_name": settings.region}
        if settings.ddb_endpoint_mode:
            kwargs["endpoint_url"] = settings.ddb_endpoint_mode.replace("{region}", settings.region)
        resource = boto3.resource("dynamodb", **kwargs)
        _ddb = resource.Table(settings.table_name)
    return _ddb


def now_ms() -> int:
    return int(time.time() * 1000)


def iso_now() -> str:
    return datetime.now(timezone.utc).isoformat()


def ensure_exam(exam_id: str, title: str, questions: list[dict]) -> None:
    t = table()
    t.put_item(Item={"pk": f"EXAM#{exam_id}", "sk": "META", "title": title,
                     "created_at": iso_now(), "question_count": len(questions)})
    for i, q in enumerate(questions):
        t.put_item(Item={"pk": f"EXAM#{exam_id}", "sk": f"QUESTION#{i:03d}",
                         "question": q["question"], "options": q["options"],
                         "answer": q["answer"]})


def list_exams(limit: int = 50) -> list[dict]:
    resp = table().scan(
        FilterExpression=Attr("pk").begins_with("EXAM#") & Attr("sk").eq("META"),
        Limit=limit)
    items = sorted(resp.get("Items", []), key=lambda item: item["created_at"])
    return [{"id": item["pk"].split("#", 1)[1], "title": item["title"],
             "question_count": item["question_count"], "created_at": item["created_at"]}
            for item in items]


def grade_attempt(exam_id: str, answers: dict[str, int]) -> dict:
    keys: dict[int, int] = {}
    for item in table().query(KeyConditionExpression="pk = :pk",
                              ExpressionAttributeValues={":pk": f"EXAM#{exam_id}"}).get("Items", []):
        if item["sk"].startswith("QUESTION#"):
            keys[int(item["sk"].split("#")[1])] = int(item["answer"])
    score = 0
    for question_index, chosen in answers.items():
        try:
            correct = keys[int(question_index)]
        except (KeyError, ValueError):
            continue
        if chosen == correct:
            score += 1
    return {"score": score, "total": len(keys)}


def get_exam(exam_id: str) -> dict | None:
    resp = table().query(KeyConditionExpression="pk = :pk",
                         ExpressionAttributeValues={":pk": f"EXAM#{exam_id}"})
    items = resp.get("Items", [])
    if not items:
        return None
    meta = next(i for i in items if i["sk"] == "META")
    meta["questions"] = [
        {"index": int(i["sk"].split("#")[1]), "question": i["question"], "options": i["options"]}
        for i in sorted((x for x in items if x["sk"].startswith("QUESTION#")), key=lambda x: x["sk"])
    ]
    return meta


def submit_attempt(exam_id: str, student_id: str, answers: dict, request_id: str | None) -> dict:
    t = table()
    request_id = request_id or str(uuid.uuid4())
    ts = now_ms()
    submission_key = {"pk": f"SUBMISSION#{exam_id}", "sk": f"STUDENT#{student_id}"}

    existing = t.get_item(Key=submission_key).get("Item")
    if existing and existing.get("request_id") == request_id:
        return {"status": "duplicate", "request_id": request_id,
                "version": existing["version"], "committed_at": existing["updated_at"]}

    version = (existing.get("version", 0) + 1) if existing else 1
    item = {**submission_key, "answers": answers, "version": version,
            "request_id": request_id, "updated_at": ts, "updated_at_iso": iso_now(),
            "written_by_region": settings.region}
    journal_item = {"pk": f"JOURNAL#{request_id}", "sk": "OUTBOX",
                    "target_pk": submission_key["pk"], "target_sk": submission_key["sk"],
                    "version": version, "request_id": request_id, "created_at": ts,
                    "answers": answers,
                    "ttl": ts // 1000 + 7 * 24 * 3600}

    t.meta.client.transact_write_items(TransactItems=[
        {"Put": {"TableName": settings.table_name, "Item": item}},
        {"Put": {"TableName": settings.table_name, "Item": journal_item}},
    ])
    return {"status": "committed", "request_id": request_id, "version": version}


def heartbeat_probe(region_label: str) -> dict:
    t = table()
    probe_id = str(uuid.uuid4())
    ts = now_ms()
    started = time.perf_counter()
    t.put_item(Item={"pk": "HEARTBEAT", "sk": f"PROBE#{region_label}#{probe_id}",
                     "ts": ts, "region": region_label,
                     "ttl": ts // 1000 + settings.heartbeat_ttl_s})
    read_back = t.get_item(Key={"pk": "HEARTBEAT", "sk": f"PROBE#{region_label}#{probe_id}"}).get("Item")
    latency_ms = round((time.perf_counter() - started) * 1000, 1)
    ok = bool(read_back)
    if ok:
        t.delete_item(Key={"pk": "HEARTBEAT", "sk": f"PROBE#{region_label}#{probe_id}"})
    return {"ok": ok, "latency_ms": latency_ms}


def latest_replicated_ts(source_region: str) -> int:
    resp = table().query(
        KeyConditionExpression="pk = :pk AND begins_with(sk, :prefix)",
        ExpressionAttributeValues={":pk": "HEARTBEAT", ":prefix": f"PROBE#{source_region}"},
        ScanIndexForward=False, Limit=1)
    items = resp.get("Items", [])
    return items[0]["ts"] if items else 0


def list_submissions(exam_id: str | None = None, limit: int = 50) -> list[dict]:
    t = table()
    if exam_id:
        resp = t.query(KeyConditionExpression="pk = :pk",
                       ExpressionAttributeValues={":pk": f"SUBMISSION#{exam_id}"},
                       ScanIndexForward=False, Limit=limit)
        return resp.get("Items", [])
    resp = t.scan(FilterExpression=Attr("pk").begins_with("SUBMISSION#"), Limit=limit)
    return resp.get("Items", [])
