package auth

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

type passwordResetMailerStub struct {
	links []string
	err   error
}

func (m *passwordResetMailerStub) SendPasswordReset(_ context.Context, _, _, link string) error {
	m.links = append(m.links, link)
	return m.err
}

func TestPasswordChangeAndResetRevokeSessionsAndEnforceSingleUseExpiry(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	db := store.NewMemory(cfg.Region)
	service := New(db, cfg)
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	service.Now = func() time.Time { return now }
	mailer := &passwordResetMailerStub{}
	service.ResetMailer = mailer
	service.Settings.PublicBaseURL = "https://slate.example.test/"

	user, err := service.CreateUser(ctx, "student@example.com", "current-password", "Student", RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	oldAccess, err := service.EncodeToken(user, "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateName(ctx, user.UserID, "New name"); err != nil {
		t.Fatal(err)
	}
	if current, err := service.CurrentUser(ctx, "Bearer "+oldAccess); err != nil || current.Name != "New name" || current.PasswordHash != "" {
		t.Fatalf("profile patch response/session = %#v, %v", current, err)
	}
	if err := service.ChangePassword(ctx, user.UserID, "wrong-password", "new-password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("wrong current password = %v", err)
	}
	if err := service.ChangePassword(ctx, user.UserID, "current-password", "new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CurrentUser(ctx, "Bearer "+oldAccess); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("password change did not revoke access token: %v", err)
	}
	if authenticated, err := service.Authenticate(ctx, user.Email, "new-password"); err != nil || authenticated == nil {
		t.Fatalf("changed password did not authenticate: %#v, %v", authenticated, err)
	}

	if err := service.RequestPasswordReset(ctx, "unknown@example.com"); err != nil || len(mailer.links) != 0 {
		t.Fatalf("unknown reset request should be silent: links=%v err=%v", mailer.links, err)
	}
	if err := service.RequestPasswordReset(ctx, user.Email); err != nil || len(mailer.links) != 1 {
		t.Fatalf("known reset request: links=%v err=%v", mailer.links, err)
	}
	resetURL, err := url.Parse(mailer.links[0])
	if err != nil || resetURL.Path != "/login" {
		t.Fatalf("reset link does not target the login reset flow: %q (%v)", mailer.links[0], err)
	}
	token := resetURL.Query().Get("token")
	if token == "" {
		t.Fatal("reset link omitted token")
	}
	resetAccess, err := service.EncodeToken(authenticatedUser(t, service, ctx, user.Email, "new-password"), "access", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ResetPassword(ctx, token, "reset-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CurrentUser(ctx, "Bearer "+resetAccess); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("reset did not revoke session: %v", err)
	}
	if err := service.ResetPassword(ctx, token, "another-password"); !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("reset token was reusable: %v", err)
	}
	if authenticated, err := service.Authenticate(ctx, user.Email, "reset-password"); err != nil || authenticated == nil {
		t.Fatalf("reset password did not authenticate: %#v, %v", authenticated, err)
	}

	if err := service.RequestPasswordReset(ctx, user.Email); err != nil || len(mailer.links) != 2 {
		t.Fatalf("second reset request: links=%v err=%v", mailer.links, err)
	}
	expiringURL, err := url.Parse(mailer.links[1])
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour + time.Second)
	if err := service.ResetPassword(ctx, expiringURL.Query().Get("token"), "expired-password"); !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("expired reset token accepted: %v", err)
	}
}

func TestPasswordUpdatesInvalidateEveryOutstandingResetToken(t *testing.T) {
	ctx := context.Background()
	cfg := config.Defaults()
	db := store.NewMemory(cfg.Region)
	service := New(db, cfg)
	service.Now = func() time.Time { return time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC) }
	mailer := &passwordResetMailerStub{}
	service.ResetMailer = mailer
	user, err := service.CreateUser(ctx, "student@example.com", "current-password", "Student", RoleStudent)
	if err != nil {
		t.Fatal(err)
	}
	requestToken := func() string {
		t.Helper()
		if err := service.RequestPasswordReset(ctx, user.Email); err != nil {
			t.Fatal(err)
		}
		resetURL, err := url.Parse(mailer.links[len(mailer.links)-1])
		if err != nil {
			t.Fatal(err)
		}
		token := resetURL.Query().Get("token")
		if token == "" {
			t.Fatal("reset link omitted token")
		}
		return token
	}

	oldTokens := []string{requestToken(), requestToken()}
	if err := service.ChangePassword(ctx, user.UserID, "current-password", "changed-password"); err != nil {
		t.Fatal(err)
	}
	for _, token := range oldTokens {
		if err := service.ResetPassword(ctx, token, "stale-reset-password"); !errors.Is(err, ErrInvalidResetToken) {
			t.Fatalf("reset token from before a password change was accepted: %v", err)
		}
	}
	if authenticated, err := service.Authenticate(ctx, user.Email, "changed-password"); err != nil || authenticated == nil {
		t.Fatalf("stale reset token changed the password: %#v, %v", authenticated, err)
	}

	siblingToken, winningToken := requestToken(), requestToken()
	if err := service.ResetPassword(ctx, winningToken, "reset-won-password"); err != nil {
		t.Fatalf("current reset token failed: %v", err)
	}
	if err := service.ResetPassword(ctx, siblingToken, "stale-sibling-password"); !errors.Is(err, ErrInvalidResetToken) {
		t.Fatalf("sibling reset token survived another reset: %v", err)
	}
	if authenticated, err := service.Authenticate(ctx, user.Email, "reset-won-password"); err != nil || authenticated == nil {
		t.Fatalf("stale sibling reset changed the winning password: %#v, %v", authenticated, err)
	}
}

func authenticatedUser(t *testing.T, service *Service, ctx context.Context, email, password string) *model.User {
	t.Helper()
	user, err := service.Authenticate(ctx, email, password)
	if err != nil || user == nil {
		t.Fatalf("authenticate %s: %#v, %v", email, user, err)
	}
	return user
}
