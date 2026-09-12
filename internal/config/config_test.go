package config

import "testing"

func TestDefaultsMatchLegacy(t *testing.T) {
	s := Defaults()
	if s.Region != "ap-south-1" || s.AppRole != "active" || s.TableName != "continuum" {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	if s.RefreshTokenTTLS != 14*24*3600 || s.WSMaxMessageBytes != 64*1024 {
		t.Fatalf("unexpected token/ws defaults: %+v", s)
	}
	if len(s.CORSOrigins) != 2 || s.CORSOrigins[0] != "http://localhost:3000" {
		t.Fatalf("unexpected CORS defaults: %#v", s.CORSOrigins)
	}
}

func TestLoadFromParsesAndOverrides(t *testing.T) {
	env := map[string]string{"AWS_REGION": "eu-west-1", "ERROR_RATE_THRESHOLD": ".25", "DB_STALL": "1", "CORS_ORIGINS": " https://one.test,https://two.test "}
	s, err := LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.Region != "eu-west-1" || s.ErrorRateThreshold != .25 || !s.DBStall {
		t.Fatalf("overrides not applied: %+v", s)
	}
	if len(s.CORSOrigins) != 2 || s.CORSOrigins[0] != "https://one.test" || s.CORSOrigins[1] != "https://two.test" {
		t.Fatalf("bad origins %#v", s.CORSOrigins)
	}
}

func TestLoadFromReturnsInvalidNumericError(t *testing.T) {
	_, err := LoadFrom(func(k string) string {
		if k == "LOGIN_RATE_LIMIT" {
			return "oops"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
}
