package auth

import (
	"context"
	"crypto"
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

func TestAuthHelpersAndStoreErrorPaths(t *testing.T) {
	if got := New(nil, config.Settings{}); got.Settings.JWTAlgorithm != "HS256" {
		t.Fatalf("default JWT algorithm = %q", got.Settings.JWTAlgorithm)
	}
	if NormalizeEmail("  Ada@EXAMPLE.COM ") != "ada@example.com" {
		t.Fatal("email was not normalized")
	}
	for email, want := range map[string]bool{
		"ada@example.com": true,
		"bad":             false,
		"a@b.c":           false,
		"a b@example.com": false,
	} {
		if got := ValidEmail(email); got != want {
			t.Fatalf("ValidEmail(%q) = %v, want %v", email, got, want)
		}
	}
	if _, err := HashPassword(strings.Repeat("x", 73)); err == nil {
		t.Fatal("expected bcrypt to reject an overlong password")
	}
	if safeUser(nil) != nil {
		t.Fatal("nil user should stay nil")
	}
	originalRead := readRandom
	readRandom = func([]byte) (int, error) { return 0, errors.New("random unavailable") }
	if got := randomHexID(); len(got) == 0 {
		t.Fatal("random fallback returned an empty id")
	}
	readRandom = originalRead

	users := &stubUserStore{}
	service := &Service{Users: users, Settings: config.Defaults(), Now: func() time.Time { return time.Unix(123, 0) }}
	if got := service.now(); got.Unix() != 123 {
		t.Fatalf("injected clock = %v", got)
	}
	created, err := service.CreateUser(context.Background(), "x@example.com", "hunter2hunter2", strings.Repeat("n", 65), "")
	if err != nil || created.Role != RoleStudent || len([]rune(created.Name)) != 60 || created.CreatedAt != "1970-01-01T00:02:03Z" {
		t.Fatalf("created user = %#v, err = %v", created, err)
	}
	users.createErr = store.ErrEmailRegistered
	if _, err := service.CreateUser(context.Background(), "y@example.com", "hunter2hunter2", "Y", RoleTeacher); !errors.Is(err, ErrEmailAlreadyRegistered) {
		t.Fatalf("duplicate error = %v", err)
	}
	users.createErr = errors.New("write failed")
	if _, err := service.CreateUser(context.Background(), "z@example.com", "hunter2hunter2", "Z", RoleTeacher); !errors.Is(err, users.createErr) {
		t.Fatalf("store error = %v", err)
	}
	users.createErr = nil
	if _, err := service.CreateUser(context.Background(), "long@example.com", strings.Repeat("x", 73), "Long", RoleStudent); err == nil {
		t.Fatal("expected CreateUser to surface bcrypt password length error")
	}
	users.user = &model.User{UserID: "u", Email: "x@example.com", Name: "X", Role: RoleTeacher, PasswordHash: "secret"}
	if got, err := service.GetUser(context.Background(), "u"); err != nil || got.PasswordHash != "" {
		t.Fatalf("GetUser = %#v, %v", got, err)
	}
	users.user = nil
	if got, err := service.GetUser(context.Background(), "u"); err != nil || got != nil {
		t.Fatalf("nil GetUser = %#v, %v", got, err)
	}
	users.getErr = errors.New("read failed")
	if _, err := service.GetUser(context.Background(), "u"); !errors.Is(err, users.getErr) {
		t.Fatalf("GetUser error = %v", err)
	}

	users.getErr = nil
	users.findID = ""
	if got, err := service.Authenticate(context.Background(), " X@EXAMPLE.COM ", "password"); err != nil || got != nil {
		t.Fatalf("empty-id authentication = %#v, %v", got, err)
	}
	users.findErr = errors.New("lookup failed")
	if _, err := service.Authenticate(context.Background(), "x@example.com", "password"); !errors.Is(err, users.findErr) {
		t.Fatalf("lookup error = %v", err)
	}
	users.findErr = nil
	users.findID = "u"
	users.getErr = errors.New("user lookup failed")
	if _, err := service.Authenticate(context.Background(), "x@example.com", "password"); !errors.Is(err, users.getErr) {
		t.Fatalf("user lookup error = %v", err)
	}
	users.getErr = nil
	users.user = nil
	if got, err := service.Authenticate(context.Background(), "x@example.com", "password"); err != nil || got != nil {
		t.Fatalf("nil user authentication = %#v, %v", got, err)
	}

	if (&Service{}).now().IsZero() {
		t.Fatal("nil clock did not fall back to wall time")
	}
}

func TestAuthTokenAndOptionalResolutionEdges(t *testing.T) {
	a, _ := testAuth()
	ctx := context.Background()
	u, err := a.CreateUser(ctx, "edge@example.com", "hunter2hunter2", "Edge", RoleTeacher)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := a.IssueTokens(u)
	if err != nil || tokens["token_type"] != "bearer" {
		t.Fatalf("issued tokens = %#v, %v", tokens, err)
	}
	a.SetSigner(func(*jwt.Token, []byte) (string, error) { return "", errors.New("signing failed") })
	if _, err := a.IssueTokens(u); err == nil {
		t.Fatal("expected access-token signing failure")
	}
	calls := 0
	a.SetSigner(func(token *jwt.Token, key []byte) (string, error) {
		calls++
		if calls == 2 {
			return "", errors.New("refresh signing failed")
		}
		return token.SignedString(key)
	})
	if _, err := a.IssueTokens(u); err == nil || calls != 2 {
		t.Fatalf("expected refresh-token signing failure, calls=%d err=%v", calls, err)
	}
	a.SetSigner(nil)
	if _, err := a.ResolveOptionalUser(ctx, ""); err != nil {
		t.Fatal(err)
	}
	if got, err := a.ResolveOptionalUser(ctx, "Basic token"); err != nil || got != nil {
		t.Fatalf("basic optional auth = %#v, %v", got, err)
	}
	if got, err := a.ResolveOptionalUser(ctx, "Bearer garbage"); err != nil || got != nil {
		t.Fatalf("garbage optional auth = %#v, %v", got, err)
	}
	badSubject, _ := a.EncodeToken(&model.User{UserID: "", Role: RoleStudent}, "access", time.Minute)
	if got, err := a.ResolveOptionalUser(ctx, "Bearer "+badSubject); err != nil || got != nil {
		t.Fatalf("empty subject optional auth = %#v, %v", got, err)
	}
	access, _ := a.EncodeToken(u, "access", time.Minute)
	if got, err := a.ResolveOptionalUser(ctx, "Bearer "+access); err != nil || got == nil {
		t.Fatalf("valid optional auth = %#v, %v", got, err)
	}
	optionalUsers := &stubUserStore{user: u, getErr: errors.New("optional user read failed")}
	a.Users = optionalUsers
	if _, err := a.ResolveOptionalUser(ctx, "Bearer "+access); !errors.Is(err, optionalUsers.getErr) {
		t.Fatalf("optional store error = %v", err)
	}
	optionalUsers.getErr = nil
	optionalUsers.user = nil
	if got, err := a.ResolveOptionalUser(ctx, "Bearer "+access); err != nil || got != nil {
		t.Fatalf("optional deleted user = %#v, %v", got, err)
	}
	if got, err := a.TeacherOrAdmin(ctx, u, "wrong"); err != nil || got != u {
		t.Fatalf("teacher with ignored invalid admin token = %#v, %v", got, err)
	}
	if _, err := a.TeacherOrAdmin(ctx, nil, ""); !errors.Is(err, ErrAuthenticationRequired) {
		t.Fatalf("nil teacher/admin = %v", err)
	}
	if err := RequireTeacher(nil); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("nil RequireTeacher = %v", err)
	}
	if err := RequireTeacher(&model.User{Role: RoleStudent}); !errors.Is(err, ErrTeacherRequired) {
		t.Fatalf("student RequireTeacher = %v", err)
	}
	if err := RequireTeacher(&model.User{Role: RoleTeacher}); err != nil {
		t.Fatal(err)
	}

	if _, err := a.tokenKey(jwt.SigningMethodHS384); err == nil {
		t.Fatal("expected signing-algorithm mismatch")
	}
	if _, err := a.tokenKey(&jwt.SigningMethodHMAC{Name: "HS256", Hash: crypto.SHA256}); err == nil {
		t.Fatal("expected signing-method mismatch")
	}
	users := &stubUserStore{user: u, getErr: errors.New("current user read failed")}
	a.Users = users
	if _, err := a.CurrentUser(ctx, "Bearer "+access); !errors.Is(err, users.getErr) {
		t.Fatalf("CurrentUser store error = %v", err)
	}
	claims, _ := a.DecodeToken(access, "access")
	delete(claims, "sub")
	unsigned := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	unsignedRaw, _ := unsigned.SignedString([]byte(a.Settings.JWTSecret))
	if _, err := a.CurrentUser(ctx, "Bearer "+unsignedRaw); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("missing subject = %v", err)
	}
	if _, err := decodeClaims(&jwt.Token{Claims: jwt.MapClaims{}, Valid: false}, "access"); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("invalid parsed claims = %v", err)
	}
	if claims, err := decodeClaims(&jwt.Token{Claims: jwt.MapClaims{"sub": "u", "typ": "access"}, Valid: true}, "access"); err != nil || claims["sub"] != "u" {
		t.Fatalf("valid parsed claims = %#v, %v", claims, err)
	}

	emptyBootstrap := &Service{Users: users, Settings: config.Defaults()}
	if email, err := emptyBootstrap.SeedBootstrapTeacher(ctx); err != nil || email != "" {
		t.Fatalf("empty bootstrap = %q, %v", email, err)
	}
	users.createErr = errors.New("bootstrap write failed")
	emptyBootstrap.Settings.BootstrapTeacherEmail = "boot-error@example.com"
	emptyBootstrap.Settings.BootstrapTeacherPass = "hunter2hunter2"
	if _, err := emptyBootstrap.SeedBootstrapTeacher(ctx); !errors.Is(err, users.createErr) {
		t.Fatalf("bootstrap error = %v", err)
	}

}

type stubUserStore struct {
	user      *model.User
	findID    string
	getErr    error
	findErr   error
	createErr error
}

func (s *stubUserStore) GetUser(context.Context, string) (*model.User, error) {
	return s.user, s.getErr
}

func (s *stubUserStore) FindUserIDByEmail(context.Context, string) (string, error) {
	return s.findID, s.findErr
}

func (s *stubUserStore) CreateUser(_ context.Context, user *model.User, hash string) error {
	if s.createErr == nil {
		s.user = &model.User{UserID: user.UserID, Email: user.Email, Name: user.Name, Role: user.Role, PasswordHash: hash, CreatedAt: user.CreatedAt}
	}
	return s.createErr
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
