"""Account authentication: bcrypt passwords, HS256 JWTs, DynamoDB users.

Users live in the same single-table design as everything else:

- ``USER#{user_id}`` / ``PROFILE``  — the account record
- ``USEREMAIL#{email}`` / ``LOGIN`` — unique email index pointing at the id

Roles are ``student`` and ``teacher``. Platform operations (chaos, promote,
status) stay behind the static admin token; quiz authoring accepts either a
teacher JWT or the admin token.
"""

import time
import uuid
from datetime import datetime, timezone

import bcrypt
import jwt
from fastapi import Depends, Header, HTTPException

import db
from config import settings

EMAIL_PATTERN = r"^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$"
ROLES = ("student", "teacher")


class EmailAlreadyRegistered(Exception):
    """The login index already holds this email."""


def hash_password(password: str) -> str:
    return bcrypt.hashpw(password.encode("utf-8"), bcrypt.gensalt()).decode("ascii")


def verify_password(password: str, password_hash: str) -> bool:
    try:
        return bcrypt.checkpw(password.encode("utf-8"), password_hash.encode("ascii"))
    except ValueError:
        return False


def _now() -> int:
    return int(time.time())


def get_user(user_id: str) -> dict | None:
    item = db.table().get_item(Key={"pk": f"USER#{user_id}", "sk": "PROFILE"}).get("Item")
    return item


def find_user_id_by_email(email: str) -> str | None:
    normalised = normalise_email(email)
    item = db.table().get_item(Key={"pk": f"USEREMAIL#{normalised}", "sk": "LOGIN"}).get("Item")
    if not item:
        return None
    return item.get("user_id")


def normalise_email(email: str) -> str:
    return email.strip().lower()


def create_user(email: str, password: str, name: str, role: str = "student") -> dict:
    """Create an account atomically; the email index enforces uniqueness."""
    if role not in ROLES:
        raise ValueError(f"unknown role '{role}'")
    normalised = normalise_email(email)
    user_id = uuid.uuid4().hex
    profile = {
        "pk": f"USER#{user_id}",
        "sk": "PROFILE",
        "user_id": user_id,
        "email": normalised,
        "name": (name.strip() or normalised.split("@", 1)[0])[:60],
        "role": role,
        "password_hash": hash_password(password),
        "created_at": datetime.now(timezone.utc).isoformat(),
    }
    login_index = {
        "pk": f"USEREMAIL#{normalised}",
        "sk": "LOGIN",
        "user_id": user_id,
        "ttl": _now() + 10 * 365 * 24 * 3600,
    }
    try:
        db.table().meta.client.transact_write_items(TransactItems=[
            {"Put": {"TableName": settings.table_name, "Item": profile}},
            {"Put": {"TableName": settings.table_name, "Item": login_index,
                     "ConditionExpression": "attribute_not_exists(pk)"}},
        ])
    except db.table().meta.client.exceptions.TransactionCanceledException as exc:
        if any(
            reason.get("Code") == "ConditionalCheckFailed"
            for reason in exc.response.get("CancellationReasons", [])
        ):
            raise EmailAlreadyRegistered(normalised) from exc
        raise
    return {key: value for key, value in profile.items()
            if key not in ("password_hash", "pk", "sk")}


def authenticate(email: str, password: str) -> dict | None:
    user_id = find_user_id_by_email(email)
    if user_id is None:
        # Burn comparable time so unknown emails are indistinguishable from
        # wrong passwords when measuring latency.
        hash_password(password)
        return None
    user = get_user(user_id)
    if user is None or not verify_password(password, user.get("password_hash", "")):
        return None
    return {key: value for key, value in user.items() if key != "password_hash"}


def encode_token(user: dict, typ: str, ttl_s: int) -> str:
    now = _now()
    payload = {
        "sub": user["user_id"],
        "role": user["role"],
        "name": user["name"],
        "typ": typ,
        "iat": now,
        "exp": now + ttl_s,
        "jti": uuid.uuid4().hex,
    }
    return jwt.encode(payload, settings.jwt_secret, algorithm=settings.jwt_algorithm)


def issue_tokens(user: dict) -> dict:
    access = encode_token(user, "access", settings.access_token_ttl_s)
    refresh = encode_token(user, "refresh", settings.refresh_token_ttl_s)
    return {"access_token": access, "refresh_token": refresh, "token_type": "bearer",
            "expires_in": settings.access_token_ttl_s}


def decode_token(token: str, expected_typ: str = "access") -> dict:
    payload = jwt.decode(token, settings.jwt_secret, algorithms=[settings.jwt_algorithm])
    if payload.get("typ") != expected_typ:
        raise jwt.InvalidTokenError(f"expected {expected_typ} token")
    return payload


def current_user(authorization: str = Header(default="")) -> dict:
    """FastAPI dependency resolving the bearer token to a live user record."""
    scheme, _, token = authorization.partition(" ")
    if scheme.lower() != "bearer" or not token:
        raise HTTPException(status_code=401, detail="authentication required")
    try:
        payload = decode_token(token)
    except jwt.PyJWTError:
        raise HTTPException(status_code=401, detail="invalid or expired session")
    user = get_user(payload["sub"])
    if user is None:
        raise HTTPException(status_code=401, detail="account no longer exists")
    return {key: value for key, value in user.items() if key != "password_hash"}


def require_teacher(user: dict = Depends(current_user)) -> dict:
    if user.get("role") != "teacher":
        raise HTTPException(status_code=403, detail="teacher role required")
    return user


def resolve_optional_user(authorization: str = Header(default="")) -> dict | None:
    """Best-effort principal resolution; never raises on bad tokens.

    Used by routes that are public but behave differently once signed in.
    """
    scheme, _, token = authorization.partition(" ")
    if scheme.lower() != "bearer" or not token:
        return None
    try:
        payload = decode_token(token)
    except jwt.PyJWTError:
        return None
    user = get_user(payload["sub"])
    if user is None:
        return None
    return {key: value for key, value in user.items() if key != "password_hash"}


def teacher_or_admin(
    user: dict | None = Depends(resolve_optional_user),
    x_admin_token: str = Header(default=""),
) -> dict:
    """Quiz authoring guard: a teacher JWT or the static admin token."""
    if x_admin_token and x_admin_token == settings.admin_token:
        return {"user_id": "admin", "name": "admin", "role": "admin"}
    if user is not None and user.get("role") == "teacher":
        return user
    if user is None:
        # No valid bearer token was presented at all.
        if x_admin_token:
            raise HTTPException(status_code=403, detail="invalid admin token")
        raise HTTPException(status_code=401, detail="authentication required")
    raise HTTPException(status_code=403, detail="teacher role required")


def seed_bootstrap_teacher() -> str | None:
    """Create the env-configured bootstrap teacher account once."""
    if not settings.bootstrap_teacher_email or not settings.bootstrap_teacher_password:
        return None
    try:
        create_user(settings.bootstrap_teacher_email, settings.bootstrap_teacher_password,
                    "Teacher", role="teacher")
        return settings.bootstrap_teacher_email
    except EmailAlreadyRegistered:
        return None
