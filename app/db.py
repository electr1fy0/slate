import time
import uuid
from datetime import datetime, timezone

import boto3
from boto3.dynamodb.conditions import Attr

from config import settings

_ddb = None


class SubmissionConflict(Exception):
    """A different attempt was already recorded for this exam/student pair."""


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


def ensure_exam(exam_id: str, title: str, questions: list[dict],
                window: dict | None = None) -> None:
    t = table()
    meta = {"pk": f"EXAM#{exam_id}", "sk": "META", "title": title,
            "created_at": iso_now(), "question_count": len(questions)}
    if window:
        meta.update(window)  # opens_at / closes_at / duration_minutes
    t.put_item(Item=meta)
    for i, q in enumerate(questions):
        t.put_item(Item={"pk": f"EXAM#{exam_id}", "sk": f"QUESTION#{i:03d}",
                         "question": q["question"], "options": q["options"],
                         "answer": q["answer"]})


def update_exam_window(exam_id: str, title: str | None = None,
                       window: dict | None = None) -> dict | None:
    """Patch mutable exam metadata in place; returns the updated META item."""
    expr_parts: list[str] = []
    values: dict[str, object] = {}
    names: dict[str, str] = {}
    if title is not None:
        expr_parts.append("#title = :title")
        names["#title"] = "title"
        values[":title"] = title
    for field in ("opens_at", "closes_at", "duration_minutes"):
        if window and field in window:
            expr_parts.append(f"#{field} = :{field}")
            names[f"#{field}"] = field
            values[f":{field}"] = window[field]
    if not expr_parts:
        return get_exam_meta(exam_id)
    resp = table().update_item(
        Key={"pk": f"EXAM#{exam_id}", "sk": "META"},
        UpdateExpression="SET " + ", ".join(expr_parts),
        ExpressionAttributeNames=names,
        ExpressionAttributeValues=values,
        ReturnValues="ALL_NEW")
    return resp.get("Attributes")


def delete_exam(exam_id: str) -> int:
    """Remove the exam definition; submissions are retained for audit."""
    items = table().query(KeyConditionExpression="pk = :pk",
                          ExpressionAttributeValues={":pk": f"EXAM#{exam_id}"}).get("Items", [])
    with table().batch_writer() as batch:
        for item in items:
            batch.delete_item(Key={"pk": item["pk"], "sk": item["sk"]})
    return len(items)


def list_exams(limit: int = 50) -> list[dict]:
    resp = table().scan(
        FilterExpression=Attr("pk").begins_with("EXAM#") & Attr("sk").eq("META"),
        Limit=limit)
    items = sorted(resp.get("Items", []), key=lambda item: item["created_at"])
    return [{"id": item["pk"].split("#", 1)[1], "title": item["title"],
             "question_count": item["question_count"], "created_at": item["created_at"],
             **{key: item[key] for key in ("opens_at", "closes_at", "duration_minutes")
                if key in item}}
            for item in items]


def grade_attempt(exam_id: str, answers: dict[str, int]) -> dict:
    keys = answer_key(exam_id)
    score = 0
    for question_index, chosen in answers.items():
        try:
            correct = keys[int(question_index)]
        except (KeyError, ValueError):
            continue
        if chosen == correct:
            score += 1
    return {"score": score, "total": len(keys)}


def answer_key(exam_id: str) -> dict[int, int]:
    keys: dict[int, int] = {}
    for item in table().query(KeyConditionExpression="pk = :pk",
                              ExpressionAttributeValues={":pk": f"EXAM#{exam_id}"}).get("Items", []):
        if item["sk"].startswith("QUESTION#"):
            keys[int(item["sk"].split("#")[1])] = int(item["answer"])
    return keys


def get_exam_meta(exam_id: str) -> dict | None:
    return table().get_item(Key={"pk": f"EXAM#{exam_id}", "sk": "META"}).get("Item")


def get_exam(exam_id: str, include_answers: bool = False) -> dict | None:
    resp = table().query(KeyConditionExpression="pk = :pk",
                         ExpressionAttributeValues={":pk": f"EXAM#{exam_id}"})
    items = resp.get("Items", [])
    if not items:
        return None
    meta = next(i for i in items if i["sk"] == "META")
    questions = []
    for i in sorted((x for x in items if x["sk"].startswith("QUESTION#")),
                    key=lambda x: x["sk"]):
        question = {"index": int(i["sk"].split("#")[1]), "question": i["question"],
                    "options": i["options"]}
        if include_answers:
            question["answer"] = int(i["answer"])
        questions.append(question)
    meta["questions"] = questions
    return meta


def record_attempt_start(exam_id: str, student_id: str) -> dict:
    """Create (or return) the attempt marker recording when time started."""
    key = {"pk": f"ATTEMPT#{exam_id}", "sk": f"STUDENT#{student_id}"}
    existing = table().get_item(Key=key).get("Item")
    if existing:
        return existing
    item = {**key, "started_at": now_ms(), "started_at_iso": iso_now()}
    try:
        table().put_item(Item=item,
                         ConditionExpression="attribute_not_exists(pk)")
        return item
    except table().meta.client.exceptions.ClientError as exc:
        if "ConditionalCheckFailed" in str(exc):
            # A parallel first fetch won the race; read its start time.
            return table().get_item(Key=key).get("Item") or item
        raise


def attempt_deadline(exam_id: str, student_id: str) -> tuple[dict | None, int | None]:
    """Return (attempt, deadline_ms); deadline is None without a duration."""
    meta = get_exam_meta(exam_id)
    duration = meta.get("duration_minutes") if meta else None
    if not duration:
        return None, None
    attempt = table().get_item(
        Key={"pk": f"ATTEMPT#{exam_id}", "sk": f"STUDENT#{student_id}"}).get("Item")
    if not attempt:
        return None, None
    return attempt, attempt["started_at"] + int(duration) * 60_000


def submit_attempt(exam_id: str, student_id: str, student_name: str,
                   answers: dict, request_id: str | None) -> dict:
    t = table()
    request_id = request_id or str(uuid.uuid4())
    ts = now_ms()
    submission_key = {"pk": f"SUBMISSION#{exam_id}", "sk": f"STUDENT#{student_id}"}

    existing = t.get_item(Key=submission_key).get("Item")
    if existing and existing.get("request_id") == request_id:
        return {"status": "duplicate", "request_id": request_id,
                "version": existing["version"], "committed_at": existing["updated_at"],
                "score": existing.get("score", 0), "total": existing.get("total", 0)}
    if existing:
        raise SubmissionConflict(
            f"student '{student_id}' already submitted for exam '{exam_id}'")

    graded = grade_attempt(exam_id, answers)
    version = 1
    item = {**submission_key, "answers": answers, "version": version,
            "request_id": request_id, "updated_at": ts, "updated_at_iso": iso_now(),
            "written_by_region": settings.region, "student_name": student_name,
            "score": graded["score"], "total": graded["total"]}
    journal_item = {"pk": f"JOURNAL#{request_id}", "sk": "OUTBOX",
                    "target_pk": submission_key["pk"], "target_sk": submission_key["sk"],
                    "version": version, "request_id": request_id, "created_at": ts,
                    "answers": answers, "student_name": student_name,
                    "score": graded["score"], "total": graded["total"],
                    "ttl": ts // 1000 + 7 * 24 * 3600}

    t.meta.client.transact_write_items(TransactItems=[
        {"Put": {"TableName": settings.table_name, "Item": item}},
        {"Put": {"TableName": settings.table_name, "Item": journal_item}},
    ])
    return {"status": "committed", "request_id": request_id, "version": version,
            **graded}


def get_submission(exam_id: str, student_id: str) -> dict | None:
    return table().get_item(
        Key={"pk": f"SUBMISSION#{exam_id}", "sk": f"STUDENT#{student_id}"}).get("Item")


def list_outbox(limit: int = 50) -> list[dict]:
    """Pending journal entries, oldest first — ops visibility into replay work."""
    resp = table().scan(FilterExpression=Attr("sk").eq("OUTBOX"), Limit=limit)
    return sorted(resp.get("Items", []), key=lambda item: item["created_at"])


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
