// Package auth implements the account, bcrypt, and HS256 JWT contract used by
// the HTTP service. It deliberately has no HTTP framework coupling.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	RoleStudent = "student"
	RoleTeacher = "teacher"
)

var (
	EmailPattern              = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}$`)
	ErrEmailAlreadyRegistered = errors.New("email already registered")
	ErrAuthenticationRequired = errors.New("authentication required")
	ErrInvalidSession         = errors.New("invalid or expired session")
	ErrAccountDeleted         = errors.New("account no longer exists")
	ErrTeacherRequired        = errors.New("teacher role required")
	ErrInvalidAdminToken      = errors.New("invalid admin token")
)

var readRandom = rand.Read

// UserStore is the persistence needed by authentication. store.Store and
// store.MemoryStore both implement it.
type UserStore interface {
	GetUser(context.Context, string) (*model.User, error)
	FindUserIDByEmail(context.Context, string) (string, error)
	CreateUser(context.Context, *model.User, string) error
}

type Service struct {
	Users    UserStore
	Settings config.Settings
	Now      func() time.Time
	sign     func(*jwt.Token, []byte) (string, error)
}

func New(users UserStore, settings config.Settings) *Service {
	if settings.JWTAlgorithm == "" {
		settings.JWTAlgorithm = "HS256"
	}
	return &Service{Users: users, Settings: settings, Now: time.Now}
}

// SetSigner replaces the token signer. Applications normally leave this nil;
// the hook is useful to embedders that need to supply a signing service and to
// deterministic transport tests that exercise token-issuance failures.
func (s *Service) SetSigner(signer func(*jwt.Token, []byte) (string, error)) {
	s.sign = signer
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }
func ValidEmail(email string) bool       { return EmailPattern.MatchString(email) }

// HashPassword emits the standard modular bcrypt format accepted by the
// bcrypt package (and by other bcrypt implementations).
func HashPassword(password string) (string, error) {
	// The deployed service uses bcrypt's 12-round default.
	// Keep that work factor when accounts are created by Go.
	digest, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return "", err
	}
	return string(digest), nil
}

func VerifyPassword(password, digest string) bool {
	return bcrypt.CompareHashAndPassword([]byte(digest), []byte(password)) == nil
}

func randomHexID() string {
	var b [16]byte
	if _, err := readRandom(b[:]); err != nil {
		return fmt.Sprintf("%032x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func safeUser(user *model.User) *model.User {
	if user == nil {
		return nil
	}
	v := *user
	v.PasswordHash = ""
	return &v
}

func (s *Service) CreateUser(ctx context.Context, email, password, name, role string) (*model.User, error) {
	if role == "" {
		role = RoleStudent
	}
	if role != RoleStudent && role != RoleTeacher {
		return nil, fmt.Errorf("unknown role %q", role)
	}
	normalized := NormalizeEmail(email)
	if name = strings.TrimSpace(name); name == "" {
		if parts := strings.SplitN(normalized, "@", 2); len(parts) > 0 {
			name = parts[0]
		}
	}
	if nameRunes := []rune(name); len(nameRunes) > 60 {
		name = string(nameRunes[:60])
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	user := &model.User{UserID: randomHexID(), Email: normalized, Name: name, Role: role, CreatedAt: s.now().UTC().Format(time.RFC3339Nano)}
	if err := s.Users.CreateUser(ctx, user, hash); err != nil {
		if errors.Is(err, store.ErrEmailRegistered) {
			return nil, ErrEmailAlreadyRegistered
		}
		return nil, err
	}
	return safeUser(user), nil
}

func (s *Service) GetUser(ctx context.Context, id string) (*model.User, error) {
	u, err := s.Users.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	return safeUser(u), nil
}

func (s *Service) FindUserIDByEmail(ctx context.Context, email string) (string, error) {
	return s.Users.FindUserIDByEmail(ctx, NormalizeEmail(email))
}

func (s *Service) Authenticate(ctx context.Context, email, password string) (*model.User, error) {
	id, err := s.FindUserIDByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	if id == "" {
		_, _ = HashPassword(password)
		return nil, nil
	}
	user, err := s.Users.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil || !VerifyPassword(password, user.PasswordHash) {
		return nil, nil
	}
	return safeUser(user), nil
}

// Claims is the decoded JWT claims set. Numeric timestamps are represented as
// int64 in newly issued tokens and are accepted by standard JSON decoders.
type Claims struct {
	Subject   string `json:"sub"`
	Role      string `json:"role"`
	Name      string `json:"name"`
	Type      string `json:"typ"`
	IssuedAt  int64  `json:"iat"`
	ExpiresAt int64  `json:"exp"`
	JWTID     string `json:"jti"`
}

func (s *Service) EncodeToken(user *model.User, typ string, ttl time.Duration) (string, error) {
	now := s.now().Unix()
	claims := jwt.MapClaims{"sub": user.UserID, "role": user.Role, "name": user.Name, "typ": typ, "iat": now, "exp": now + int64(ttl/time.Second), "jti": randomHexID()}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	if s.sign != nil {
		return s.sign(token, []byte(s.Settings.JWTSecret))
	}
	return token.SignedString([]byte(s.Settings.JWTSecret))
}

func (s *Service) tokenKey(method jwt.SigningMethod) (any, error) {
	if method.Alg() != s.Settings.JWTAlgorithm {
		return nil, fmt.Errorf("unexpected signing algorithm")
	}
	if method != jwt.SigningMethodHS256 {
		return nil, fmt.Errorf("unexpected signing method")
	}
	return []byte(s.Settings.JWTSecret), nil
}

func (s *Service) IssueTokens(user *model.User) (map[string]any, error) {
	access, err := s.EncodeToken(user, "access", time.Duration(s.Settings.AccessTokenTTLS)*time.Second)
	if err != nil {
		return nil, err
	}
	refresh, err := s.EncodeToken(user, "refresh", time.Duration(s.Settings.RefreshTokenTTLS)*time.Second)
	if err != nil {
		return nil, err
	}
	return map[string]any{"access_token": access, "refresh_token": refresh, "token_type": "bearer", "expires_in": s.Settings.AccessTokenTTLS}, nil
}

func (s *Service) DecodeToken(raw, expectedType string) (jwt.MapClaims, error) {
	parsed, err := jwt.ParseWithClaims(raw, jwt.MapClaims{}, func(t *jwt.Token) (any, error) {
		return s.tokenKey(t.Method)
	}, jwt.WithValidMethods([]string{s.Settings.JWTAlgorithm}))
	if err != nil {
		return nil, err
	}
	return decodeClaims(parsed, expectedType)
}

func decodeClaims(parsed *jwt.Token, expectedType string) (jwt.MapClaims, error) {
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || !parsed.Valid {
		return nil, ErrInvalidSession
	}
	typ, ok := claims["typ"].(string)
	if !ok || typ != expectedType {
		return nil, fmt.Errorf("expected %s token", expectedType)
	}
	return claims, nil
}

func claimSubject(claims jwt.MapClaims) (string, bool) {
	sub, ok := claims["sub"].(string)
	return sub, ok && sub != ""
}

func (s *Service) CurrentUser(ctx context.Context, authorization string) (*model.User, error) {
	scheme, token, ok := ParseBearer(authorization)
	if !ok || strings.ToLower(scheme) != "bearer" {
		return nil, ErrAuthenticationRequired
	}
	claims, err := s.DecodeToken(token, "access")
	if err != nil {
		return nil, ErrInvalidSession
	}
	id, ok := claimSubject(claims)
	if !ok {
		return nil, ErrInvalidSession
	}
	user, err := s.Users.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrAccountDeleted
	}
	return safeUser(user), nil
}

func ParseBearer(header string) (scheme, token string, ok bool) {
	parts := strings.SplitN(strings.TrimSpace(header), " ", 2)
	if len(parts) != 2 || parts[0] == "" || strings.TrimSpace(parts[1]) == "" {
		return "", "", false
	}
	return parts[0], strings.TrimSpace(parts[1]), true
}

func (s *Service) ResolveOptionalUser(ctx context.Context, authorization string) (*model.User, error) {
	scheme, token, ok := ParseBearer(authorization)
	if !ok || strings.ToLower(scheme) != "bearer" {
		return nil, nil
	}
	claims, err := s.DecodeToken(token, "access")
	if err != nil {
		return nil, nil
	}
	id, ok := claimSubject(claims)
	if !ok {
		return nil, nil
	}
	user, err := s.Users.GetUser(ctx, id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}
	return safeUser(user), nil
}

func RequireTeacher(user *model.User) error {
	if user == nil || user.Role != RoleTeacher {
		return ErrTeacherRequired
	}
	return nil
}

func (s *Service) TeacherOrAdmin(ctx context.Context, user *model.User, adminToken string) (*model.User, error) {
	if adminToken != "" {
		if adminToken == s.Settings.AdminToken {
			return &model.User{UserID: "admin", Name: "admin", Role: "admin"}, nil
		}
		if user == nil {
			return nil, ErrInvalidAdminToken
		}
	}
	if user == nil {
		return nil, ErrAuthenticationRequired
	}
	if user.Role != RoleTeacher {
		return nil, ErrTeacherRequired
	}
	return user, nil
}

func (s *Service) SeedBootstrapTeacher(ctx context.Context) (string, error) {
	if s.Settings.BootstrapTeacherEmail == "" || s.Settings.BootstrapTeacherPass == "" {
		return "", nil
	}
	_, err := s.CreateUser(ctx, s.Settings.BootstrapTeacherEmail, s.Settings.BootstrapTeacherPass, "Teacher", RoleTeacher)
	if errors.Is(err, ErrEmailAlreadyRegistered) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s.Settings.BootstrapTeacherEmail, nil
}
