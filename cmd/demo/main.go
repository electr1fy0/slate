package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/continuum"
)

func main() {
	if err := run(context.Background()); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(parent context.Context) error {
	settings, err := config.Load()
	if err != nil {
		return err
	}
	settings.Region = envOr("DEMO_REGION", "local")
	interval := 5000
	if raw := os.Getenv("DEMO_PROBE_INTERVAL_MS"); raw != "" {
		interval, err = strconv.Atoi(raw)
		if err != nil || interval <= 0 {
			return fmt.Errorf("DEMO_PROBE_INTERVAL_MS must be a positive integer")
		}
	}
	ctx, stop := signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	demo, err := continuum.StartLocalDemo(ctx, settings, continuum.LocalDemoConfig{
		FrontendAddr: envOr("DEMO_LISTEN_ADDR", "127.0.0.1:8080"),
		BackendAAddr: envOr("BACKEND_A_LISTEN_ADDR", "127.0.0.1:8001"),
		BackendBAddr: envOr("BACKEND_B_LISTEN_ADDR", "127.0.0.1:8002"),
		ProbeMillis:  interval,
		AdminToken:   settings.AdminToken,
	})
	if err != nil {
		return err
	}
	log.Printf("Continuum local process demo listening at %s (backend A=%s, B=%s)", demo.PublicURL, demo.BackendURLs()["A"], demo.BackendURLs()["B"])
	log.Printf("This demo uses one shared in-memory store and control pointer; all data resets when the process exits.")
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return demo.Close(shutdownCtx)
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
