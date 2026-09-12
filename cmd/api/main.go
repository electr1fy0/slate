package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/httpapi"
	"github.com/ayush/slate/internal/store"
)

func main() {
	settings := config.MustLoad()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	backend, err := store.New(ctx, settings)
	if err != nil {
		log.Fatal(err)
	}
	api := httpapi.New(settings, backend)
	if err := api.Seed(ctx); err != nil {
		log.Printf("startup seed skipped: %v", err)
	}
	api.WS = boards.NewHTTPHandler(api.Boards, func(ctx context.Context, token string) (boards.Identity, error) {
		user, err := api.Auth.CurrentUser(ctx, "Bearer "+token)
		if err != nil {
			return boards.Identity{}, boards.ErrInvalidToken
		}
		role := user.Role
		if role != "student" {
			role = "teacher"
		}
		return boards.Identity{Name: user.Name, Role: role}, nil
	}, settings.CORSOrigins, settings.WSMaxMessageBytes)

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
		_ = server.Shutdown(shutdownCtx)
	}()

	log.Printf("continuum API listening on %s", addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
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
