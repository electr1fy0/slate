package auth

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/golang-jwt/jwt/v5"
)

func TestQualityEmailBoundaries(t *testing.T) {
	for _, email := range []string{".name@example.com", "name.@example.com", "a..b@example.com", "a@example..com", "a@-example.com", "a@example-.com", "a@" + strings.Repeat("a", 64) + ".com", strings.Repeat("a", 121) + "@example.com"} {
		t.Run(email, func(t *testing.T) {
			if ValidEmail(email) {
				t.Fatal("accepted invalid email")
			}
		})
	}
	if !ValidEmail("name+tag@sub.example.com") {
		t.Fatal("rejected valid email")
	}
}

func TestQualityJWTBoundaries(t *testing.T) {
	settings := config.Defaults()
	service := New(nil, settings)
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return now }
	user := &model.User{UserID: "user", Role: RoleStudent}
	for _, claims := range []jwt.MapClaims{
		{"sub": "user", "typ": "access"},
		{"sub": "user", "typ": "access", "exp": now.Add(time.Hour).Unix(), "iat": now.Add(time.Minute).Unix()},
	} {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(settings.JWTSecret))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.DecodeToken(raw, "access"); err == nil {
			t.Fatal("accepted invalid lifetime")
		}
	}
	if _, err := service.EncodeToken(nil, "access", time.Hour); err == nil {
		t.Fatal("accepted nil user")
	}
	if _, err := service.EncodeToken(user, "other", time.Hour); err == nil {
		t.Fatal("accepted unknown type")
	}
	if _, err := service.EncodeToken(user, "access", time.Millisecond); err == nil {
		t.Fatal("accepted already-expired lifetime")
	}
	if _, err := decodeClaims(nil, "access"); err == nil {
		t.Fatal("accepted nil token")
	}
	if _, ok := claimSubject(jwt.MapClaims{"sub": " "}); ok {
		t.Fatal("accepted blank subject")
	}
	for _, version := range []float64{-1, 0.5, math.Inf(1), math.NaN(), math.Exp2(63)} {
		if SessionValid(jwt.MapClaims{"sv": version}, user) {
			t.Fatalf("accepted version %v", version)
		}
	}
	if !SessionValid(jwt.MapClaims{"sv": float64(0)}, user) {
		t.Fatal("rejected valid counter")
	}
	scheme, token, ok := ParseBearer("Bearer\t token")
	if !ok || scheme != "Bearer" || token != "token" {
		t.Fatal("did not parse whitespace-delimited bearer")
	}
}

func TestQualityAdminTokensRequireAnExactNonemptySecret(t *testing.T) {
	if MatchAdminToken("", "") || MatchAdminToken("wrong", "secret") || !MatchAdminToken("secret", "secret") {
		t.Fatal("invalid admin-token match")
	}
}
