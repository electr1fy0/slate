import os


def env_int(name: str, default: int) -> int:
    return int(os.environ.get(name, default))


def env_float(name: str, default: float) -> float:
    return float(os.environ.get(name, default))


class Settings:
    region = os.environ.get("AWS_REGION", "ap-south-1")
    app_role = os.environ.get("APP_ROLE", "active")
    role_file = os.environ.get("CONTINUUM_ROLE_FILE", "/etc/continuum/role")
    table_name = os.environ.get("TABLE_NAME", "continuum")
    ddb_endpoint_mode = os.environ.get("DDB_ENDPOINT_MODE", "")
    deep_probe_interval_s = env_int("DEEP_PROBE_INTERVAL_S", 5)
    error_rate_window = env_int("ERROR_RATE_WINDOW", 100)
    error_rate_threshold = env_float("ERROR_RATE_THRESHOLD", 0.5)
    disk_threshold_pct = env_float("DISK_THRESHOLD_PCT", 90.0)
    chaos_error_pct = env_int("CHAOS_ERROR_PCT", 0)
    chaos_latency_ms = env_int("CHAOS_LATENCY_MS", 0)
    db_stall = os.environ.get("DB_STALL", "0") == "1"
    admin_token = os.environ.get("ADMIN_TOKEN", "continuum-demo-token")
    heartbeat_ttl_s = env_int("HEARTBEAT_TTL_S", 300)
    cors_origins = [origin.strip()
                    for origin in os.environ.get("CORS_ORIGINS",
                                                 "http://localhost:3000,http://127.0.0.1:3000").split(",")]


settings = Settings()
