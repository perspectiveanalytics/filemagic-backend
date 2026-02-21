package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/api"
	"github.com/perspectiveanalytics/filemagic-backend/internal/cgroup"
	"github.com/perspectiveanalytics/filemagic-backend/internal/cleanup"
	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/converter"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
	"github.com/perspectiveanalytics/filemagic-backend/internal/stats"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	cfg := config.Load()

	slog.Info("starting filemagic server",
		"addr", cfg.ListenAddr,
		"tmpfsPath", cfg.TmpfsPath,
		"maxFileSize", cfg.MaxFileSize,
		"maxQueueSize", cfg.MaxQueueSize,
	)

	if err := os.MkdirAll(cfg.TmpfsPath, 0755); err != nil {
		slog.Error("failed to create tmpfs directory", "error", err)
		os.Exit(1)
	}

	// Attempt cgroupv2 delegation setup for nsjail resource limits.
	// This is optional — if delegation fails (e.g., not running under systemd
	// with Delegate=yes), nsjail still works with rlimits + namespace isolation.
	cgroupMount, err := cgroup.Setup()
	if err != nil {
		slog.Warn("cgroupv2 delegation not available, nsjail will run without cgroup limits",
			"error", err,
		)
	} else {
		cfg.Cgroupv2Mount = cgroupMount
		slog.Info("cgroupv2 delegation enabled", "mount", cgroupMount)
	}

	registry, err := converter.NewRegistry(cfg)
	if err != nil {
		slog.Error("failed to initialize converter registry", "error", err)
		os.Exit(1)
	}

	processFunc := func(ctx context.Context, job *queue.Job) error {
		return registry.Process(ctx, job)
	}

	q := queue.New(cfg.MaxQueueSize, processFunc)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q.Start(ctx)

	cleaner := cleanup.New(cfg, q)
	go cleaner.Start(ctx)

	statsStore := stats.New(cfg.CFAccountID, cfg.CFNamespaceID, cfg.CFApiToken)

	router := api.NewRouter(ctx, cfg, q, statsStore)

	server := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		slog.Info("server listening", "addr", cfg.ListenAddr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	statsStore.Close()
	cancel()
	slog.Info("server stopped")
}
