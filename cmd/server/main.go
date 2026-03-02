package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Ansh1693/faas/internal/api"
	lambdaruntime "github.com/Ansh1693/faas/internal/lambda"
	"github.com/Ansh1693/faas/internal/service"
	"github.com/Ansh1693/faas/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	if err := run(logger); err != nil {
		logger.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	dbURL := getEnv("DATABASE_URL", "postgres://localhost:5432/evtq_faas?sslmode=disable")
	listenAddr := getEnv("LISTEN_ADDR", ":8081")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect database: %w", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	st := store.New(pool)
	runner := lambdaruntime.NewManager(st)
	runner.Start(ctx)
	defer runner.Shutdown(context.Background())
	svc := service.New(st, runner)
	router := api.NewRouter(svc, logger)

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		cancel()
		sdCtx, sdCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer sdCancel()
		_ = srv.Shutdown(sdCtx)
	}()

	logger.Info("starting faas", "addr", listenAddr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
