import time

import jwt as pyjwt
import pytest
from fastapi import HTTPException

import auth


@pytest.fixture()
def table(make_table):
    return make_table("continuum")


# ------------------------------------------------------------------ passwords

def test_password_hash_roundtrip():
    digest = auth.hash_password("s3cret-password")
    assert digest != "s3cret-password"
    assert auth.verify_password("s3cret-password", digest) is True


def test_password_verify_rejects_wrong_password():
    digest = auth.hash_password("s3cret-password")
    assert auth.verify_password("nope", digest) is False


def test_password_verify_survives_malformed_hash():
    assert auth.verify_password("nope", "not-a-bcrypt-hash") is False


# ---------------------------------------------------------------------- users

def test_create_user_stores_profile_and_login_index(table):
    user = auth.create_user("Ada@Example.com ", "hunter2hunter2", "Ada")
    assert user["email"] == "ada@example.com"
    assert "password_hash" not in user
    assert user["role"] == "student"
    stored = table.get_item(Key={"pk": f"USER#{user['user_id']}", "sk": "PROFILE"})["Item"]
    assert stored["name"] == "Ada"
    index = table.get_item(Key={"pk": "USEREMAIL#ada@example.com", "sk": "LOGIN"})["Item"]
    assert index["user_id"] == user["user_id"]


def test_create_user_name_defaults_to_email_prefix_and_clamps_length(table):
    user = auth.create_user("grace@example.com", "hunter2hunter2", "")
    assert user["name"] == "grace"
    clamped = auth.create_user("kay@example.com", "hunter2hunter2", "x" * 100)
    assert len(clamped["name"]) == 60


def test_create_user_rejects_unknown_role(table):
    with pytest.raises(ValueError, match="unknown role"):
        auth.create_user("x@example.com", "hunter2hunter2", "X", role="wizard")


def test_create_user_enforces_unique_email(table):
    auth.create_user("dupe@example.com", "hunter2hunter2", "First")
    with pytest.raises(auth.EmailAlreadyRegistered):
        auth.create_user("dupe@example.com", "other-password", "Second")


def test_create_user_reraises_non_email_transaction_failures(table, monkeypatch):
    import db

    def throttled(**kwargs):
        reasons = [{"Code": "ThrottlingError", "Message": "slow down"}]
        exc = db.table().meta.client.exceptions.TransactionCanceledException(
            {"CancellationReasons": reasons}, "TransactWriteItems")
        raise exc

    monkeypatch.setattr(db.table().meta.client, "transact_write_items", throttled)
    with pytest.raises(db.table().meta.client.exceptions.TransactionCanceledException):
        auth.create_user("throttled@example.com", "hunter2hunter2", "T")


def test_get_user_and_email_lookup_misses(table):
    assert auth.get_user("ghost") is None
    assert auth.find_user_id_by_email("ghost@example.com") is None


def test_authenticate_success_and_failures(table):
    auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    user = auth.authenticate("ada@example.com", "hunter2hunter2")
    assert user is not None and "password_hash" not in user
    assert auth.authenticate("ada@example.com", "wrong-password") is None
    assert auth.authenticate("nobody@example.com", "whatever-pass") is None


def test_authenticate_tolerates_missing_profile_row(table):
    auth.create_user("orphan@example.com", "hunter2hunter2", "Orphan")
    user_id = auth.find_user_id_by_email("orphan@example.com")
    table.delete_item(Key={"pk": f"USER#{user_id}", "sk": "PROFILE"})
    assert auth.authenticate("orphan@example.com", "hunter2hunter2") is None


# --------------------------------------------------------------------- tokens

def test_encode_decode_token_roundtrip(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    claims = auth.decode_token(auth.encode_token(user, "access", 60))
    assert claims["sub"] == user["user_id"]
    assert claims["role"] == "student"


def test_decode_rejects_wrong_token_type(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    refresh = auth.encode_token(user, "refresh", 60)
    with pytest.raises(pyjwt.PyJWTError):
        auth.decode_token(refresh, expected_typ="access")


def test_decode_rejects_expired_token(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    expired = auth.encode_token(user, "access", -10)
    with pytest.raises(pyjwt.PyJWTError):
        auth.decode_token(expired)


def test_decode_rejects_foreign_signature(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    forged = pyjwt.encode(
        {"sub": user["user_id"], "typ": "access", "exp": int(time.time()) + 60},
        "some-other-secret", algorithm="HS256")
    with pytest.raises(pyjwt.PyJWTError):
        auth.decode_token(forged)


def test_issue_tokens_shape(table):
    from config import settings

    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    tokens = auth.issue_tokens(user)
    assert tokens["token_type"] == "bearer"
    assert tokens["expires_in"] == settings.access_token_ttl_s
    refresh_claims = auth.decode_token(tokens["refresh_token"], expected_typ="refresh")
    assert refresh_claims["sub"] == user["user_id"]


# --------------------------------------------------------------- dependencies

def test_current_user_requires_bearer_scheme(table):
    del table
    with pytest.raises(HTTPException) as missing:
        auth.current_user("")
    assert missing.value.status_code == 401
    with pytest.raises(HTTPException) as wrong_scheme:
        auth.current_user("Basic dXNlcjpwYXNz")
    assert wrong_scheme.value.status_code == 401


def test_current_user_rejects_garbage_token():
    with pytest.raises(HTTPException) as exc:
        auth.current_user("Bearer not.a.jwt")
    assert exc.value.status_code == 401
    assert "invalid or expired" in exc.value.detail


def test_current_user_rejects_deleted_account(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    token = auth.encode_token(user, "access", 60)
    table.delete_item(Key={"pk": f"USER#{user['user_id']}", "sk": "PROFILE"})
    with pytest.raises(HTTPException) as exc:
        auth.current_user(f"Bearer {token}")
    assert exc.value.status_code == 401
    assert "no longer exists" in exc.value.detail


def test_current_user_happy_path(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    resolved = auth.current_user(f"Bearer {auth.encode_token(user, 'access', 60)}")
    assert resolved["user_id"] == user["user_id"]
    assert "password_hash" not in resolved


def test_require_teacher_gate(table):
    teacher = auth.create_user("t@example.com", "hunter2hunter2", "T", role="teacher")
    student = auth.create_user("s@example.com", "hunter2hunter2", "S")
    assert auth.require_teacher(teacher)["role"] == "teacher"
    with pytest.raises(HTTPException) as exc:
        auth.require_teacher(student)
    assert exc.value.status_code == 403


def test_teacher_or_admin_accepts_admin_token():
    from config import settings

    principal = auth.teacher_or_admin(user=None, x_admin_token=settings.admin_token)
    assert principal["role"] == "admin"


def test_teacher_or_admin_accepts_teacher_jwt(table):
    teacher = auth.create_user("t@example.com", "hunter2hunter2", "T", role="teacher")
    principal = auth.teacher_or_admin(user=teacher, x_admin_token="")
    assert principal["role"] == "teacher"


def test_teacher_or_admin_rejects_anonymous_without_admin_token():
    with pytest.raises(HTTPException) as exc:
        auth.teacher_or_admin(user=None, x_admin_token="")
    assert exc.value.status_code == 401


def test_teacher_or_admin_rejects_wrong_admin_token_only():
    with pytest.raises(HTTPException) as exc:
        auth.teacher_or_admin(user=None, x_admin_token="wrong-token")
    assert exc.value.status_code == 403
    assert "invalid admin token" in exc.value.detail


def test_teacher_or_admin_rejects_student(table):
    student = auth.create_user("s@example.com", "hunter2hunter2", "S")
    with pytest.raises(HTTPException) as bare:
        auth.teacher_or_admin(user=student, x_admin_token="")
    assert bare.value.status_code == 403
    assert "teacher role required" in bare.value.detail


# ------------------------------------------------------------------ optional

def test_resolve_optional_user_returns_none_when_absent(table):
    del table
    assert auth.resolve_optional_user("") is None
    assert auth.resolve_optional_user("Bearer junk") is None


def test_resolve_optional_user_resolves_and_tolerates_deleted(table):
    user = auth.create_user("ada@example.com", "hunter2hunter2", "Ada")
    token = auth.encode_token(user, "access", 60)
    assert auth.resolve_optional_user(f"Bearer {token}")["user_id"] == user["user_id"]
    table.delete_item(Key={"pk": f"USER#{user['user_id']}", "sk": "PROFILE"})
    assert auth.resolve_optional_user(f"Bearer {token}") is None


# ------------------------------------------------------------------ bootstrap

def test_seed_bootstrap_teacher_skips_when_unset(monkeypatch):
    monkeypatch.setattr("config.settings.bootstrap_teacher_email", "")
    assert auth.seed_bootstrap_teacher() is None


def test_seed_bootstrap_teacher_creates_once(table):
    import config

    original = (config.settings.bootstrap_teacher_email,
                config.settings.bootstrap_teacher_password)
    config.settings.bootstrap_teacher_email = "bootstrap.teacher@example.com"
    config.settings.bootstrap_teacher_password = "hunter2hunter2"
    try:
        assert auth.seed_bootstrap_teacher() == "bootstrap.teacher@example.com"
        user = auth.authenticate("bootstrap.teacher@example.com", "hunter2hunter2")
        assert user["role"] == "teacher"
        assert auth.seed_bootstrap_teacher() is None  # already registered
    finally:
        config.settings.bootstrap_teacher_email, config.settings.bootstrap_teacher_password \
            = original
