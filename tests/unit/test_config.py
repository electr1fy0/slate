from config import Settings, env_float, env_int


def test_env_int_uses_default_when_missing(monkeypatch):
    monkeypatch.delenv("X_INT", raising=False)
    assert env_int("X_INT", 7) == 7


def test_env_int_parses_present_value(monkeypatch):
    monkeypatch.setenv("X_INT", "42")
    assert env_int("X_INT", 0) == 42


def test_env_float_uses_default_when_missing(monkeypatch):
    monkeypatch.delenv("X_FLOAT", raising=False)
    assert env_float("X_FLOAT", 1.5) == 1.5


def test_env_float_parses_present_value(monkeypatch):
    monkeypatch.setenv("X_FLOAT", "2.25")
    assert env_float("X_FLOAT", 0.0) == 2.25


def test_settings_defaults_are_coherent():
    s = Settings()
    assert s.region and s.table_name
    assert isinstance(s.error_rate_threshold, float)
    assert isinstance(s.load_threshold, float) and s.load_threshold > 0
    assert s.admin_token
