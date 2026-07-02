package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/98624017/meitu-layering-proxy/internal/auth"
	"github.com/98624017/meitu-layering-proxy/internal/config"
	"github.com/98624017/meitu-layering-proxy/internal/credentials"
	"github.com/98624017/meitu-layering-proxy/internal/httpapi"
	"github.com/98624017/meitu-layering-proxy/internal/meitu"
	"github.com/98624017/meitu-layering-proxy/internal/tasks"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("config_load_failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool := credentials.NewPool(cfg.Credentials)
	meituClient := meitu.NewClient(cfg.MeituBaseURL, &http.Client{Timeout: cfg.MeituHTTPTimeout})
	taskService := tasks.NewService(tasks.ServiceOptions{
		Store:                tasks.NewMemoryStore(),
		Pool:                 pool,
		Client:               meituClient,
		QueueTimeout:         cfg.QueueTimeout,
		UpstreamLeaseTimeout: cfg.UpstreamLeaseTimeout,
		TaskTTL:              cfg.TaskTTL,
		MaxQueuedTasks:       cfg.MaxQueuedTasks,
	})

	go taskService.Start(ctx)

	server := httpapi.NewServer(taskService, auth.NewMiddleware(cfg.ProxyAPIKey))
	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("http_server_shutdown_failed", "error", err)
		}
	}()

	slog.Info("meitu_layering_proxy_starting", "addr", cfg.ListenAddr, "credential_count", pool.Len())
	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("http_server_failed", "error", err)
		os.Exit(1)
	}
	slog.Info("meitu_layering_proxy_stopped")
}
