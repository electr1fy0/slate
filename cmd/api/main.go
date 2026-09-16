package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/httpapi"
	"github.com/ayush/slate/internal/store"
)

func main() {
	if err := run(context.Background()); err != nil {
		fatal(err)
	}
}

var (
	newMemoryBackend = func(region string) httpapi.Backend { return store.NewMemory(region) }
	newDynamoBackend = func(ctx context.Context, settings config.Settings) (httpapi.Backend, error) {
		return store.New(ctx, settings)
	}
	listenAndServe = func(server *http.Server) error { return server.ListenAndServe() }
	exit           = os.Exit
	fatal          = func(err error) { log.Print(err); exit(1) }
)

func run(parent context.Context) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var backend httpapi.Backend
	if strings.EqualFold(strings.TrimSpace(os.Getenv("STORE_BACKEND")), "memory") {
		backend = newMemoryBackend(settings.Region)
		log.Printf("using in-memory data store")
	} else {
		ddb, err := newDynamoBackend(ctx, settings)
		if err != nil {
			return err
		}
		backend = ddb
	}
	api := httpapi.New(settings, backend)
	if err := api.Seed(ctx); err != nil {
		log.Printf("startup seed skipped: %v", err)
	}
	api.WS = boards.NewHTTPHandler(api.Boards, boardIdentityResolver(api), settings.CORSOrigins, settings.WSMaxMessageBytes)

	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = ":80"
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go sweepBoards(ctx, api, settings)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = api.Boards.FlushAll(shutdownCtx)
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("continuum API listening on %s", addr)
	if err := listenAndServe(server); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func boardIdentityResolver(api *httpapi.Server) func(context.Context, string) (boards.Identity, error) {
	return func(ctx context.Context, token string) (boards.Identity, error) {
		user, err := api.Auth.CurrentUser(ctx, "Bearer "+token)
		if err != nil {
			return boards.Identity{}, boards.ErrInvalidToken
		}
		role := user.Role
		if role != "student" {
			role = "teacher"
		}
		return boards.Identity{Name: user.Name, Role: role}, nil
	}
}

func sweepBoards(ctx context.Context, api *httpapi.Server, settings config.Settings) {
	interval := time.Duration(settings.WSSweepIntervalS) * time.Second
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			api.Boards.EvictStale(ctx, time.Duration(settings.WSMaxIdleS)*time.Second)
		}
	}
}
