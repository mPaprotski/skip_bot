package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/poseshaemost/skip-bot/internal/config"
	"github.com/poseshaemost/skip-bot/internal/service"
	"github.com/poseshaemost/skip-bot/internal/storage/sqlite"
	"github.com/poseshaemost/skip-bot/internal/telegram"
	"github.com/poseshaemost/skip-bot/migrations"
	"github.com/poseshaemost/skip-bot/pkg/migrate"
)

func main() {
	if e := run(); e != nil {
		slog.Error("startup or shutdown failed", "error", e)
		os.Exit(1)
	}
}
func run() error {
	cfg, e := config.Load()
	if e != nil {
		return e
	}
	level := slog.LevelInfo
	_ = level.UnmarshalText([]byte(cfg.LogLevel))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	store, e := sqlite.New(cfg.DatabasePath)
	if e != nil {
		return fmt.Errorf("SQLite: %w", e)
	}
	defer store.Close()
	list, e := migrate.LoadMigrationsFromFS(migrations.Files, ".")
	if e != nil {
		return e
	}
	if e = migrate.NewMigrator(store.DB, list).Migrate(context.Background()); e != nil {
		return e
	}
	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	apiCtx, stopAPI := context.WithCancel(context.Background())
	defer stopAPI()
	bot, e := telegram.NewBot(apiCtx, cfg, service.NewServices(store, cfg.OwnerTelegramID, cfg.GroupTimezone), logger)
	if e != nil {
		return e
	}
	srv := &http.Server{Addr: cfg.HTTPListenAddr, Handler: bot.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	listener, e := net.Listen("tcp", cfg.HTTPListenAddr)
	if e != nil {
		return errors.New("не удалось открыть HTTP_LISTEN_ADDR")
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- srv.Serve(listener) }()
	defer srv.Close()
	probeCtx, stopProbe := context.WithTimeout(apiCtx, 10*time.Second)
	probeErr := telegram.CheckPublicURL(probeCtx, cfg.WebhookURL, http.DefaultClient)
	stopProbe()
	if probeErr != nil {
		return probeErr
	}
	if e = bot.RegisterWebhook(); e != nil {
		return e
	}
	workerDone := make(chan struct{})
	go func() { defer close(workerDone); bot.Run(workerCtx) }()
	signals, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger.Info("bot started", "listen_addr", cfg.HTTPListenAddr)
	select {
	case <-signals.Done():
	case e = <-serverDone:
		if !errors.Is(e, http.ErrServerClosed) {
			logger.Error("HTTP server stopped")
		}
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		_ = srv.Close()
	}
	stopWorker()
	select {
	case <-workerDone:
	case <-time.After(25 * time.Second):
		stopAPI()
		<-workerDone
	}
	if e != nil && !errors.Is(e, http.ErrServerClosed) {
		return e
	}
	return shutdownErr
}
