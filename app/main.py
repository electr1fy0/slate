import os
import re
import shutil
import uuid

from fastapi import FastAPI, Header, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, PlainTextResponse
from pydantic import BaseModel

import db
from chaos import ChaosMiddleware, chaos_state, error_rate, reset_chaos
from config import settings
from ws import manager as board_manager
from ws import router as board_router

app = FastAPI(title="Continuum Exam Platform")
app.add_middleware(ChaosMiddleware)
app.add_middleware(CORSMiddleware, allow_origins=settings.cors_origins,
                   allow_methods=["*"], allow_headers=["*"])
app.include_router(board_router)


def slugify(text: str) -> str:
    slug = re.sub(r"[^a-z0-9]+", "-", text.lower()).strip("-")
    return slug or f"quiz-{uuid.uuid4().hex[:8]}"

SEED_EXAM = {
    "id": "cs101-midterm",
    "title": "CS101 Midterm Examination",
    "questions": [
        {"question": "Which AWS service provides cross-region asynchronous table replication?",
         "options": ["Aurora Global Database", "DynamoDB Global Tables", "S3 CRR", "EBS Snapshots"], "answer": 1},
        {"question": "What does RPO measure?",
         "options": ["Maximum tolerable data loss window", "Maximum tolerable downtime",
                     "Replication throughput", "Recovery cost"], "answer": 0},
        {"question": "Route 53 failover routing policy requires at least how many record sets?",
         "options": ["1", "2", "3", "4"], "answer": 1},
        {"question": "Step Functions rollback branches are modelled using which ASL field?",
         "options": ["Retry", "Catch", "TimeoutSeconds", "HeartbeatSeconds"], "answer": 1},
        {"question": "An outbox pattern primarily addresses which failure mode?",
         "options": ["DNS propagation delay", "In-flight write loss during failover",
                     "Certificate expiry", "Clock skew"], "answer": 1},
    ],
}

ROLE_LABELS = {"active": "ACTIVE (serving exam)", "standby": "STANDBY (read-only)"}


def current_role() -> str:
    if os.path.exists(settings.role_file):
        with open(settings.role_file) as f:
            return f.read().strip() or settings.app_role
    return settings.app_role


def require_active():
    if current_role() != "active":
        raise HTTPException(status_code=503, detail="standby node is read-only; promote to serve traffic")


def require_admin(x_admin_token: str = Header(default="")):
    if x_admin_token != settings.admin_token:
        raise HTTPException(status_code=403, detail="invalid admin token")


@app.on_event("startup")
def seed():
    try:
        db.ensure_exam(SEED_EXAM["id"], SEED_EXAM["title"], SEED_EXAM["questions"])
    except Exception as exc:
        print(f"startup seed skipped: {exc}")


class SubmitPayload(BaseModel):
    student_id: str
    answers: dict[str, int]
    request_id: str | None = None


class ChaosPayload(BaseModel):
    error_pct: int | None = None
    latency_ms: int | None = None
    db_stall: bool | None = None


class QuestionIn(BaseModel):
    question: str
    options: list[str]
    answer: int


class ExamIn(BaseModel):
    title: str
    questions: list[QuestionIn]
    id: str | None = None


@app.get("/", response_class=PlainTextResponse)
def index():
    role = current_role()
    if role == "standby":
        return (f"CONTINUUM STANDBY NODE [{settings.region}]\n"
                "This node is in standby. Exam delivery unavailable until promoted.\n")
    return f"CONTINUUM EXAM PLATFORM [{settings.region}] role={role.upper()} — visit /exam/cs101-midterm\n"


@app.get("/health/live")
def live():
    return {"status": "alive", "region": settings.region, "role": current_role()}


@app.get("/health/deep")
def deep():
    role = current_role()
    checks: dict = {}
    healthy = True

    if chaos_state.db_stall:
        checks["ddb"] = {"ok": False, "reason": "db_stall chaos flag"}
        healthy = False
    else:
        try:
            checks["ddb"] = db.heartbeat_probe(settings.region)
            if not checks["ddb"]["ok"]:
                healthy = False
        except Exception as exc:
            checks["ddb"] = {"ok": False, "reason": str(exc)[:200]}
            healthy = False

    err = error_rate()
    checks["error_rate"] = {"value": round(err, 3), "threshold": settings.error_rate_threshold,
                            "ok": err < settings.error_rate_threshold}
    if not checks["error_rate"]["ok"]:
        healthy = False

    du = shutil.disk_usage("/")
    disk_pct = round(du.used / du.total * 100, 1)
    checks["disk"] = {"used_pct": disk_pct, "ok": disk_pct < settings.disk_threshold_pct}
    if not checks["disk"]["ok"]:
        healthy = False

    checks["load"] = {"loadavg": round(os.getloadavg()[0], 2), "ok": os.getloadavg()[0] < 8.0}
    if not checks["load"]["ok"]:
        healthy = False

    body = {"healthy": healthy, "region": settings.region, "role": role, "checks": checks,
            "chaos": {"error_pct": chaos_state.error_pct, "latency_ms": chaos_state.latency_ms,
                      "db_stall": chaos_state.db_stall}}
    return JSONResponse(body, status_code=200 if healthy else 503)


@app.get("/exam/{exam_id}")
def get_exam(exam_id: str):
    require_active()
    try:
        exam = db.get_exam(exam_id)
    except Exception:
        raise HTTPException(status_code=503, detail="data layer unavailable")
    if not exam:
        raise HTTPException(status_code=404, detail="exam not found")
    for q in exam["questions"]:
        q.pop("answer", None)
    return exam


@app.post("/exam/{exam_id}/submit")
def submit(exam_id: str, payload: SubmitPayload):
    require_active()
    try:
        result = db.submit_attempt(exam_id, payload.student_id, payload.answers,
                                   payload.request_id)
        result.update(db.grade_attempt(exam_id, payload.answers))
    except Exception as exc:
        detail = "duplicate submission" if "ConditionalCheckFailed" in type(exc).__name__ else "data layer unavailable"
        raise HTTPException(status_code=503, detail=detail)
    return result


@app.get("/exams")
def list_exams():
    return db.list_exams()


@app.post("/exam", status_code=201)
def create_exam(payload: ExamIn, x_admin_token: str = Header(default="")):
    require_admin(x_admin_token)
    require_active()
    if not payload.title.strip():
        raise HTTPException(status_code=400, detail="title must not be empty")
    if not payload.questions:
        raise HTTPException(status_code=400, detail="at least one question is required")
    for position, question in enumerate(payload.questions):
        if not question.question.strip() or len(question.options) < 2 \
                or any(not option.strip() for option in question.options):
            raise HTTPException(status_code=400,
                                detail=f"question {position} needs text and at least two non-empty options")
        if not 0 <= question.answer < len(question.options):
            raise HTTPException(status_code=400,
                                detail=f"question {position} answer index out of range")

    questions = [q.model_dump() for q in payload.questions]
    if payload.id is not None:
        exam_id = slugify(payload.id)
        if db.get_exam(exam_id) is not None:
            raise HTTPException(status_code=409, detail=f"exam id '{exam_id}' already exists")
    else:
        exam_id = slugify(payload.title)
        suffix = 1
        while db.get_exam(exam_id) is not None:
            suffix += 1
            exam_id = f"{slugify(payload.title)}-{suffix}"
            if suffix > 50:
                exam_id = f"quiz-{uuid.uuid4().hex[:8]}"
                break
    db.ensure_exam(exam_id, payload.title.strip(), questions)
    return {"id": exam_id, "title": payload.title.strip(),
            "question_count": len(questions)}


@app.get("/admin/submissions")
def submissions(x_admin_token: str = Header(default=""), limit: int = 50):
    require_admin(x_admin_token)
    return db.list_submissions(limit=limit)


@app.get("/admin/status")
def admin_status(x_admin_token: str = Header(default="")):
    require_admin(x_admin_token)
    return {"region": settings.region, "role": current_role(),
            "boards": {"active_rooms": len(board_manager.rooms)},
            "chaos": {"error_pct": chaos_state.error_pct, "latency_ms": chaos_state.latency_ms,
                      "db_stall": chaos_state.db_stall}}


@app.post("/admin/chaos")
def set_chaos(payload: ChaosPayload, x_admin_token: str = Header(default="")):
    require_admin(x_admin_token)
    if payload.error_pct is not None:
        chaos_state.error_pct = max(0, min(100, payload.error_pct))
    if payload.latency_ms is not None:
        chaos_state.latency_ms = max(0, payload.latency_ms)
    if payload.db_stall is not None:
        chaos_state.db_stall = payload.db_stall
    return {"chaos": {"error_pct": chaos_state.error_pct, "latency_ms": chaos_state.latency_ms,
                      "db_stall": chaos_state.db_stall}}


@app.post("/admin/chaos/reset")
def clear_chaos(x_admin_token: str = Header(default="")):
    require_admin(x_admin_token)
    reset_chaos()
    return {"chaos": {"error_pct": 0, "latency_ms": 0, "db_stall": False}}


@app.post("/admin/promote")
def promote(x_admin_token: str = Header(default="")):
    require_admin(x_admin_token)
    if current_role() == "standby":
        if os.path.exists(os.path.dirname(settings.role_file)):
            with open(settings.role_file, "w") as f:
                f.write("active")
        settings.app_role = "active"
    return {"region": settings.region, "role": current_role()}
