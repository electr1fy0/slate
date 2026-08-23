import os
import re
import shutil
import uuid
from contextlib import asynccontextmanager
from datetime import datetime, timezone

import jwt
from fastapi import Depends, FastAPI, Header, HTTPException, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse, PlainTextResponse
from pydantic import BaseModel, Field, field_validator

import auth
import db
from chaos import ChaosMiddleware, chaos_state, error_rate, reset_chaos
from config import settings
from middleware import RequestIdMiddleware, RateLimitMiddleware, auth_rate_limits, log_json
from ws import manager as board_manager
from ws import router as board_router


@asynccontextmanager
async def lifespan(_app: FastAPI):
    try:
        db.ensure_exam(SEED_EXAM["id"], SEED_EXAM["title"], SEED_EXAM["questions"])
        log_json("seed_exam", exam_id=SEED_EXAM["id"])
    except Exception as exc:
        log_json("startup_seed_skipped", reason=str(exc)[:200])
    teacher = auth.seed_bootstrap_teacher()
    if teacher:
        log_json("bootstrap_teacher_seeded", email=teacher)
    yield


app = FastAPI(title="Continuum Exam Platform", lifespan=lifespan)
app.add_middleware(ChaosMiddleware)
app.add_middleware(RequestIdMiddleware)
app.add_middleware(RateLimitMiddleware, limits=auth_rate_limits())
app.add_middleware(CORSMiddleware, allow_origins=settings.cors_origins,
                   allow_methods=["*"], allow_headers=["*"],
                   expose_headers=["x-request-id"])
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


class RegisterPayload(BaseModel):
    email: str = Field(pattern=auth.EMAIL_PATTERN, max_length=120)
    password: str = Field(min_length=8, max_length=128)
    name: str = Field(default="", max_length=60)


class LoginPayload(BaseModel):
    email: str
    password: str


class RefreshPayload(BaseModel):
    refresh_token: str


class AdminCreateUserPayload(BaseModel):
    email: str = Field(pattern=auth.EMAIL_PATTERN, max_length=120)
    password: str = Field(min_length=8, max_length=128)
    name: str = Field(default="", max_length=60)
    role: str = Field(default="student")

    @field_validator("role")
    @classmethod
    def role_known(cls, value: str) -> str:
        if value not in auth.ROLES:
            raise ValueError(f"role must be one of {', '.join(auth.ROLES)}")
        return value


class SubmitPayload(BaseModel):
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
    opens_at: datetime | None = None
    closes_at: datetime | None = None
    duration_minutes: int | None = Field(default=None, gt=0, le=24 * 60)

    @field_validator("closes_at")
    @classmethod
    def window_ordered(cls, closes_at: datetime | None, info):
        opens_at = info.data.get("opens_at")
        if closes_at is not None and opens_at is not None and closes_at <= opens_at:
            raise ValueError("closes_at must be after opens_at")
        return closes_at


class ExamPatchIn(BaseModel):
    title: str | None = None
    opens_at: datetime | None = None
    closes_at: datetime | None = None
    duration_minutes: int | None = Field(default=None, gt=0, le=24 * 60)


def window_payload(payload: ExamIn) -> dict:
    """Serialise scheduling fields for storage; datetimes become ISO strings."""
    window: dict = {}
    if payload.opens_at is not None:
        window["opens_at"] = payload.opens_at.isoformat()
    if payload.closes_at is not None:
        window["closes_at"] = payload.closes_at.isoformat()
    if payload.duration_minutes is not None:
        window["duration_minutes"] = payload.duration_minutes
    return window


class ExamWindowError(Exception):
    def __init__(self, code: str, message: str):
        self.code = code
        self.message = message


def enforce_exam_window(meta: dict, user: dict) -> None:
    """Students may only fetch exams inside the window; staff bypass."""
    if user.get("role") != "student":
        return
    now = datetime.now(timezone.utc)
    for field_name, code, message in (
            ("opens_at", "EXAM_NOT_OPEN", "exam is not open yet"),
            ("closes_at", "EXAM_CLOSED", "exam window has closed")):
        raw = meta.get(field_name)
        if not raw:
            continue
        try:
            boundary = datetime.fromisoformat(str(raw))
        except ValueError:
            continue
        if field_name == "opens_at" and now < boundary:
            raise ExamWindowError(code, message)
        if field_name == "closes_at" and now > boundary:
            raise ExamWindowError(code, message)


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

    load1 = round(os.getloadavg()[0], 2)
    checks["load"] = {"loadavg": load1, "ok": load1 < settings.load_threshold}
    if not checks["load"]["ok"]:
        healthy = False

    body = {"healthy": healthy, "region": settings.region, "role": role, "checks": checks,
            "chaos": {"error_pct": chaos_state.error_pct, "latency_ms": chaos_state.latency_ms,
                      "db_stall": chaos_state.db_stall}}
    return JSONResponse(body, status_code=200 if healthy else 503)


# ---------------------------------------------------------------------- auth

@app.post("/auth/register", status_code=201)
def register(payload: RegisterPayload):
    try:
        user = auth.create_user(payload.email, payload.password, payload.name)
    except auth.EmailAlreadyRegistered:
        raise HTTPException(status_code=409, detail="email already registered")
    except Exception:
        raise HTTPException(status_code=503, detail="data layer unavailable")
    return {"user": user, **auth.issue_tokens(user)}


@app.post("/auth/login")
def login(payload: LoginPayload):
    try:
        user = auth.authenticate(payload.email, payload.password)
    except Exception:
        raise HTTPException(status_code=503, detail="data layer unavailable")
    if user is None:
        raise HTTPException(status_code=401, detail="invalid email or password")
    return {"user": user, **auth.issue_tokens(user)}


@app.post("/auth/refresh")
def refresh_tokens(payload: RefreshPayload):
    try:
        claims = auth.decode_token(payload.refresh_token, expected_typ="refresh")
    except jwt.PyJWTError:
        raise HTTPException(status_code=401, detail="invalid or expired refresh token")
    user = auth.get_user(claims["sub"])
    if user is None:
        raise HTTPException(status_code=401, detail="account no longer exists")
    safe_user = {key: value for key, value in user.items() if key != "password_hash"}
    return {"user": safe_user, **auth.issue_tokens(safe_user)}


@app.get("/auth/me")
def me(user: dict = Depends(auth.current_user)):
    return {"user": user}


@app.post("/admin/users", status_code=201)
def admin_create_user(payload: AdminCreateUserPayload,
                      x_admin_token: str = Header(default="")):
    require_admin(x_admin_token)
    try:
        user = auth.create_user(payload.email, payload.password, payload.name,
                                role=payload.role)
    except auth.EmailAlreadyRegistered:
        raise HTTPException(status_code=409, detail="email already registered")
    return {"user": user}


# ---------------------------------------------------------------------- exams

@app.get("/exam/{exam_id}")
def get_exam(exam_id: str, user: dict = Depends(auth.current_user)):
    require_active()
    try:
        exam = db.get_exam(exam_id)
    except Exception:
        raise HTTPException(status_code=503, detail="data layer unavailable")
    if not exam:
        raise HTTPException(status_code=404, detail="exam not found")
    try:
        enforce_exam_window(exam, user)
    except ExamWindowError as exc:
        _reject_window(exc)
    for q in exam["questions"]:
        q.pop("answer", None)
    if user["role"] == "student":
        # Start (or resume) the attempt clock for timed exams.
        attempt = db.record_attempt_start(exam_id, user["user_id"])
        exam["attempt_started_at"] = attempt["started_at"]
        if exam.get("duration_minutes"):
            exam["deadline_ms"] = attempt["started_at"] + int(exam["duration_minutes"]) * 60_000
    return exam


def _reject_window(exc: ExamWindowError):
    raise HTTPException(status_code=403,
                        detail={"code": exc.code, "message": exc.message})


@app.post("/exam/{exam_id}/submit")
def submit(exam_id: str, payload: SubmitPayload,
           user: dict = Depends(auth.current_user)):
    require_active()
    student_id = user["user_id"]
    student_name = user.get("name") or student_id
    try:
        meta = db.get_exam_meta(exam_id)
        if meta is None:
            raise HTTPException(status_code=404, detail="exam not found")
        enforce_exam_window(meta, user)
        if user["role"] == "student" and meta.get("duration_minutes"):
            _attempt, deadline = db.attempt_deadline(exam_id, student_id)
            now_ms = datetime.now(timezone.utc).timestamp() * 1000
            if deadline is not None and now_ms > deadline + 5_000:
                # Small grace window for in-flight network submissions.
                raise ExamWindowError("ATTEMPT_EXPIRED", "time is up for this attempt")
        result = db.submit_attempt(exam_id, student_id, student_name,
                                   payload.answers, payload.request_id)
    except ExamWindowError as exc:
        _reject_window(exc)
    except HTTPException:
        raise
    except db.SubmissionConflict:
        raise HTTPException(status_code=409,
                            detail="this student already submitted for this exam")
    except Exception:
        raise HTTPException(status_code=503, detail="data layer unavailable")
    return result


@app.get("/exam/{exam_id}/result")
def my_result(exam_id: str, user: dict = Depends(auth.current_user)):
    require_active()
    submission = db.get_submission(exam_id, user["user_id"])
    if submission is None:
        raise HTTPException(status_code=404, detail="no submission for this exam")
    try:
        exam = db.get_exam(exam_id, include_answers=True)
    except Exception:
        exam = None
    review = []
    for question in (exam or {}).get("questions", []):
        chosen = submission.get("answers", {}).get(str(question["index"]))
        correct = question.get("answer")
        review.append({
            "index": question["index"],
            "question": question["question"],
            "options": question["options"],
            "chosen": chosen,
            "correct_answer": correct,
            "was_correct": chosen is not None and chosen == correct,
        })
    return {"status": "submitted", "score": submission.get("score", 0),
            "total": submission.get("total", 0),
            "answers": submission.get("answers", {}),
            "submitted_at": submission.get("updated_at_iso"),
            "request_id": submission.get("request_id"),
            "review": review}


@app.get("/exams")
def list_exams():
    return db.list_exams()


@app.post("/exam", status_code=201)
def create_exam(payload: ExamIn, principal: dict = Depends(auth.teacher_or_admin)):
    del principal
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
    window = window_payload(payload)
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
    db.ensure_exam(exam_id, payload.title.strip(), questions, window=window)
    return {"id": exam_id, "title": payload.title.strip(),
            "question_count": len(questions), **window}


@app.patch("/exam/{exam_id}")
def patch_exam(exam_id: str, payload: ExamPatchIn,
               principal: dict = Depends(auth.teacher_or_admin)):
    del principal
    require_active()
    if db.get_exam_meta(exam_id) is None:
        raise HTTPException(status_code=404, detail="exam not found")
    fields = payload.model_dump(exclude_none=True)
    title = fields.pop("title", None)
    window = {key: value.isoformat() if isinstance(value, datetime) else value
              for key, value in fields.items()}
    updated = db.update_exam_window(exam_id, title=title, window=window)
    return {"id": exam_id,
            **{k: v for k, v in (updated or {}).items()
               if k in ("title", "opens_at", "closes_at", "duration_minutes")}}


@app.post("/exam/{exam_id}/close")
def close_exam(exam_id: str, principal: dict = Depends(auth.teacher_or_admin)):
    del principal
    require_active()
    if db.get_exam_meta(exam_id) is None:
        raise HTTPException(status_code=404, detail="exam not found")
    closed_at = datetime.now(timezone.utc)
    db.update_exam_window(exam_id, window={"closes_at": closed_at.isoformat()})
    return {"id": exam_id, "closed_at": closed_at.isoformat()}


@app.delete("/exam/{exam_id}", status_code=204)
def delete_exam(exam_id: str, principal: dict = Depends(auth.teacher_or_admin)):
    del principal
    require_active()
    removed = db.delete_exam(exam_id)
    if removed == 0:
        raise HTTPException(status_code=404, detail="exam not found")


@app.get("/admin/exams/{exam_id}/results")
def exam_results(exam_id: str, principal: dict = Depends(auth.teacher_or_admin)):
    del principal
    rows = db.list_submissions(exam_id=exam_id, limit=1000)
    scored = [(row.get("score"), row.get("total")) for row in rows if row.get("total")]
    avg_pct = round(sum(s / t * 100 for s, t in scored) / len(scored), 1) if scored else None
    return {"exam_id": exam_id, "count": len(rows), "average_pct": avg_pct,
            "submissions": [{
                "student_id": row["sk"].replace("STUDENT#", ""),
                "student_name": row.get("student_name", ""),
                "answers": row.get("answers", {}),
                "score": row.get("score", 0), "total": row.get("total", 0),
                "submitted_at": row.get("updated_at_iso"),
            } for row in rows]}


@app.get("/admin/outbox")
def outbox(x_admin_token: str = Header(default=""), limit: int = 50):
    """Journal entries visible for reconciliation monitoring."""
    require_admin(x_admin_token)
    return db.list_outbox(limit=min(max(limit, 1), 200))


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
