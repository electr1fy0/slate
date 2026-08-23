import importlib.util
import os
import sys
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parent.parent
APP_DIR = ROOT / "app"
REGION = "ap-south-1"

# Auth rate limits would otherwise trip across the dozens of logins the
# integration/e2e suites perform inside one process.
os.environ.setdefault("LOGIN_RATE_LIMIT", "10000")
os.environ.setdefault("REGISTER_RATE_LIMIT", "10000")

if str(APP_DIR) not in sys.path:
    sys.path.insert(0, str(APP_DIR))

import boto3  # noqa: E402
from moto import mock_aws  # noqa: E402

import chaos as app_chaos  # noqa: E402
import db as app_db  # noqa: E402
from config import settings  # noqa: E402

LAMBDA_PATHS = {
    "health_verifier": ROOT / "lambdas" / "health_verifier" / "handler.py",
    "failover_ops": ROOT / "lambdas" / "failover_ops" / "handler.py",
    "reconciler": ROOT / "lambdas" / "reconciler" / "handler.py",
}

LAMBDA_REGION_ENV = {
    "AWS_DEFAULT_REGION": REGION,
    "AWS_REGION": REGION,
}

LAMBDA_ENV_DEFAULTS = {
    "health_verifier": {"TABLE_NAME": "continuum"},
    "failover_ops": {
        "TABLE_NAME": "continuum",
        "STANDBY_INSTANCE_ID": "i-0standby1234567890",
        "PRIMARY_HEALTH_CHECK_ID": "hc-primary-00000000",
    },
    "reconciler": {"TABLE_NAME": "continuum"},
}


def load_lambda(name: str, env: dict | None = None):
    """Load a lambda handler module with its import-time env satisfied.

    Must be called inside an active ``mock_aws`` context so the clients the
    module builds at import time are bound to moto.
    """
    modname = f"_lambda_{name}"
    sys.modules.pop(modname, None)
    saved = os.environ.copy()
    try:
        merged = {**LAMBDA_REGION_ENV, **LAMBDA_ENV_DEFAULTS[name], **(env or {})}
        os.environ.update(merged)
        spec = importlib.util.spec_from_file_location(modname, LAMBDA_PATHS[name])
        mod = importlib.util.module_from_spec(spec)
        sys.modules[modname] = mod
        spec.loader.exec_module(mod)
        return mod
    finally:
        os.environ.clear()
        os.environ.update(saved)


@pytest.fixture(autouse=True)
def _isolate():
    saved_env = os.environ.copy()
    saved_settings = dict(settings.__dict__)
    yield
    os.environ.clear()
    os.environ.update(saved_env)
    settings.__dict__.clear()
    settings.__dict__.update(saved_settings)
    app_db._ddb = None
    app_chaos.reset_chaos()
    app_chaos.chaos_state.status_codes.clear()


@pytest.fixture()
def aws():
    with mock_aws():
        yield


@pytest.fixture()
def make_table(aws):
    created: list = []

    def _make(name: str = "continuum"):
        client = boto3.client("dynamodb", region_name=REGION)
        client.create_table(
            TableName=name,
            KeySchema=[
                {"AttributeName": "pk", "KeyType": "HASH"},
                {"AttributeName": "sk", "KeyType": "RANGE"},
            ],
            AttributeDefinitions=[
                {"AttributeName": "pk", "AttributeType": "S"},
                {"AttributeName": "sk", "AttributeType": "S"},
            ],
            BillingMode="PAY_PER_REQUEST",
        )
        table = boto3.resource("dynamodb", region_name=REGION).Table(name)
        created.append(table)
        return table

    return _make


@pytest.fixture()
def lambda_loader(aws):
    return load_lambda
