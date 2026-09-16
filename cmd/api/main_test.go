package main

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/httpapi"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

type failingSeedBackend struct {
	*store.MemoryStore
}

func (b *failingSeedBackend) EnsureExam(context.Context, string, string, []model.Question, map[string]any) error {
	return errors.New("seed failed")
}

func TestRunAndMainBranches(t *testing.T) {
	t.Setenv("STORE_BACKEND", "memory")
	t.Setenv("CONTINUUM_ROLE_FILE", filepath.Join(t.TempDir(), "role"))
	t.Setenv("LISTEN_ADDR", ":0")
	oldListen := listenAndServe
	listenAndServe = func(*http.Server) error { return http.ErrServerClosed }
	t.Cleanup(func() { listenAndServe = oldListen })
	if err := run(context.Background()); err != nil {
		t.Fatalf("memory run = %v", err)
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("second memory run = %v", err)
	}

	oldMemory := newMemoryBackend
	newMemoryBackend = func(region string) httpapi.Backend {
		return &failingSeedBackend{MemoryStore: store.NewMemory(region)}
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("seed-error run = %v", err)
	}
	newMemoryBackend = oldMemory
	t.Setenv("LISTEN_ADDR", "")
	if err := run(context.Background()); err != nil {
		t.Fatalf("default-address run = %v", err)
	}
	listenAndServe = func(*http.Server) error { return errors.New("listen failed") }
	if err := run(context.Background()); err == nil {
		t.Fatal("unexpected listener error should be returned")
	}
	listenAndServe = func(*http.Server) error { return http.ErrServerClosed }

	t.Setenv("STORE_BACKEND", "dynamo")
	oldDynamo := newDynamoBackend
	newDynamoBackend = func(context.Context, config.Settings) (httpapi.Backend, error) {
		return store.NewMemory("test-region"), nil
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("injected Dynamo run = %v", err)
	}
	newDynamoBackend = func(context.Context, config.Settings) (httpapi.Backend, error) {
		return nil, errors.New("Dynamo unavailable")
	}
	if err := run(context.Background()); err == nil {
		t.Fatal("Dynamo construction error should be returned")
	}
	newDynamoBackend = oldDynamo
	awsCtx, cancelAWS := context.WithCancel(context.Background())
	cancelAWS()
	_, _ = oldDynamo(awsCtx, config.Defaults())

	oldExit := exit
	exitCode := 0
	exit = func(code int) { exitCode = code }
	oldFatal := fatal
	fatal = func(err error) { oldFatal(err) }
	fatal(errors.New("default fatal hook"))
	if exitCode != 1 {
		t.Fatalf("default fatal hook exit code = %d, want 1", exitCode)
	}
	exit = oldExit
	fatal = oldFatal

	if err := oldListen(&http.Server{Addr: "invalid address"}); err == nil {
		t.Fatal("default listener should reject an invalid address")
	}

	t.Setenv("ACCESS_TOKEN_TTL_S", "not-an-int")
	if err := run(context.Background()); err == nil {
		t.Fatal("configuration error should be returned")
	}
	t.Setenv("ACCESS_TOKEN_TTL_S", "")

	mainFatal := fatal
	gotFatal := error(nil)
	fatal = func(err error) { gotFatal = err }
	newDynamoBackend = func(context.Context, config.Settings) (httpapi.Backend, error) {
		return nil, errors.New("main startup failed")
	}
	main()
	if gotFatal == nil {
		t.Fatal("main should pass run failures to fatal")
	}
	fatal = mainFatal
	newDynamoBackend = oldDynamo
}

func TestBoardIdentityResolver(t *testing.T) {
	settings := config.Defaults()
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	backend := store.NewMemory(settings.Region)
	api := httpapi.New(settings, backend)
	resolve := boardIdentityResolver(api)
	if _, err := resolve(context.Background(), "not-a-token"); !errors.Is(err, boards.ErrInvalidToken) {
		t.Fatalf("invalid token error = %v", err)
	}

	users := []*model.User{
		{UserID: "student", Email: "student@example.com", Name: "Student", Role: "student"},
		{UserID: "teacher", Email: "teacher@example.com", Name: "Teacher", Role: "teacher"},
	}
	for _, user := range users {
		if err := backend.CreateUser(context.Background(), user, "unused"); err != nil {
			t.Fatalf("create %s: %v", user.Role, err)
		}
		token, err := api.Auth.EncodeToken(user, "access", time.Hour)
		if err != nil {
			t.Fatalf("encode %s token: %v", user.Role, err)
		}
		identity, err := resolve(context.Background(), token)
		if err != nil {
			t.Fatalf("resolve %s: %v", user.Role, err)
		}
		wantRole := user.Role
		if wantRole != "student" {
			wantRole = "teacher"
		}
		if identity != (boards.Identity{Name: user.Name, Role: wantRole}) {
			t.Fatalf("identity = %+v, want name=%q role=%q", identity, user.Name, wantRole)
		}
	}
}

func TestSweepBoardsCancellationAndTick(t *testing.T) {
	settings := config.Defaults()
	settings.WSSweepIntervalS = 0
	settings.WSMaxIdleS = 0
	api := httpapi.New(settings, store.NewMemory(settings.Region))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	sweepBoards(ctx, api, settings)

	settings.WSSweepIntervalS = 1
	ctx, cancel = context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		sweepBoards(ctx, api, settings)
		close(done)
	}()
	select {
	case <-time.After(1500 * time.Millisecond):
		cancel()
	case <-done:
		cancel()
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sweepBoards did not stop after cancellation")
	}
}
