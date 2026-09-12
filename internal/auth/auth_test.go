package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
	"github.com/golang-jwt/jwt/v5"
)

func testAuth() (*Service, *store.MemoryStore) {
	cfg := config.Defaults()
	db := store.NewMemory(cfg.Region)
	return New(db, cfg), db
}

func TestPasswordHashIsCrossCompatibleBcrypt(t *testing.T) {
	hash, err := HashPassword("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$2a$") {
		t.Fatalf("unexpected bcrypt format %q", hash)
	}
	if !VerifyPassword("s3cret-password", hash) {
		t.Fatal("generated hash did not verify")
	}
	if !VerifyPassword("s3cret-password", strings.Replace(hash, "$2a$", "$2b$", 1)) {
		t.Fatal("$2b$ bcrypt hash should verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("wrong password verified")
	}
	if VerifyPassword("wrong", "not-a-bcrypt-hash") {
		t.Fatal("malformed hash verified")
	}
}

func TestCreateAndAuthenticateNormalizesEmailAndName(t *testing.T) {
	a, _ := testAuth()
	ctx := context.Background()
	u, err := a.CreateUser(ctx, " Ada@Example.com ", "hunter2hunter2", " ", RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "ada@example.com" || u.Name != "ada" || u.PasswordHash != "" {
		t.Fatalf("bad safe user %#v", u)
	}
	same, err := a.Authenticate(ctx, "ADA@example.com", "hunter2hunter2")
	if err != nil || same == nil || same.UserID != u.UserID {
		t.Fatalf("authenticate got %#v %v", same, err)
	}
	if bad, _ := a.Authenticate(ctx, "ada@example.com", "wrong-password"); bad != nil {
		t.Fatal("wrong password authenticated")
	}
	if missing, _ := a.Authenticate(ctx, "nobody@example.com", "whatever"); missing != nil {
		t.Fatal("unknown user authenticated")
	}
}

func TestCreateUserUniquenessAndRoles(t *testing.T) {
	a, _ := testAuth()
	ctx := context.Background()
	if _, err := a.CreateUser(ctx, "a@example.com", "hunter2hunter2", "A", "wizard"); err == nil {
		t.Fatal("unknown role accepted")
	}
	if _, err := a.CreateUser(ctx, "a@example.com", "hunter2hunter2", "A", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateUser(ctx, "A@example.com", "other-password", "B", RoleStudent); !errors.Is(err, ErrEmailAlreadyRegistered) {
		t.Fatalf("got %v", err)
	}
}

func TestJWTClaimsAndBearerResolution(t *testing.T) {
	a, db := testAuth()
	ctx := context.Background()
	u, err := a.CreateUser(ctx, "u@example.com", "hunter2hunter2", "User", RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := a.EncodeToken(u, "access", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := a.DecodeToken(tok, "access")
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != u.UserID || claims["role"] != RoleStudent || claims["typ"] != "access" {
		t.Fatalf("bad claims %#v", claims)
	}
	got, err := a.CurrentUser(ctx, "Bearer "+tok)
	if err != nil || got.UserID != u.UserID || got.PasswordHash != "" {
		t.Fatalf("current user %#v %v", got, err)
	}
	if _, err := a.CurrentUser(ctx, "Basic "+tok); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("wrong scheme: %v", err)
	}
	if _, err := a.CurrentUser(ctx, "Bearer garbage"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("garbage token: %v", err)
	}
	a.Users = &deletingUsers{UserStore: db, deleted: u.UserID}
	if _, err := a.CurrentUser(ctx, "Bearer "+tok); !errors.Is(err, ErrAccountDeleted) {
		t.Fatalf("deleted account: %v", err)
	}
}

func TestJWTRejectsWrongTypeExpiredAndForeignSecret(t *testing.T) {
	a, _ := testAuth()
	u := &model.User{UserID: "u", Role: RoleStudent, Name: "U"}
	refresh, _ := a.EncodeToken(u, "refresh", time.Minute)
	if _, err := a.DecodeToken(refresh, "access"); err == nil {
		t.Fatal("refresh accepted as access")
	}
	expired, _ := a.EncodeToken(u, "access", -time.Minute)
	if _, err := a.DecodeToken(expired, "access"); err == nil {
		t.Fatal("expired token accepted")
	}
	foreign := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": "u", "typ": "access", "exp": time.Now().Add(time.Minute).Unix()})
	raw, _ := foreign.SignedString([]byte("other-secret"))
	if _, err := a.DecodeToken(raw, "access"); err == nil {
		t.Fatal("foreign signature accepted")
	}
}

func TestTeacherOrAdminAndBootstrap(t *testing.T) {
	a, _ := testAuth()
	ctx := context.Background()
	student := &model.User{UserID: "s", Role: RoleStudent}
	teacher := &model.User{UserID: "t", Role: RoleTeacher}
	if _, err := a.TeacherOrAdmin(ctx, student, ""); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student: %v", err)
	}
	if p, err := a.TeacherOrAdmin(ctx, teacher, ""); err != nil || p != teacher {
		t.Fatalf("teacher: %#v %v", p, err)
	}
	if p, err := a.TeacherOrAdmin(ctx, nil, a.Settings.AdminToken); err != nil || p.Role != "admin" {
		t.Fatalf("admin: %#v %v", p, err)
	}
	if _, err := a.TeacherOrAdmin(ctx, nil, "wrong"); !errors.Is(err, ErrInvalidAdminToken) {
		t.Fatalf("wrong admin: %v", err)
	}
	a.Settings.BootstrapTeacherEmail = "boot@example.com"
	a.Settings.BootstrapTeacherPass = "hunter2hunter2"
	email, err := a.SeedBootstrapTeacher(ctx)
	if err != nil || email != "boot@example.com" {
		t.Fatalf("seed: %q %v", email, err)
	}
	email, err = a.SeedBootstrapTeacher(ctx)
	if err != nil || email != "" {
		t.Fatalf("second seed: %q %v", email, err)
	}
}

type deletingUsers struct {
	UserStore
	deleted string
}

func (d *deletingUsers) GetUser(ctx context.Context, id string) (*model.User, error) {
	if id == d.deleted {
		return nil, nil
	}
	return d.UserStore.GetUser(ctx, id)
}
