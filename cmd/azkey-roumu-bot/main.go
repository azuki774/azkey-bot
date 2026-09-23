// Command azkey-roumu-bot observes public notes from mutual targets through read-only polling.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/azuki774/azkey-bot/internal/misskey"
	"github.com/azuki774/azkey-bot/internal/roumu/bot"
	"github.com/azuki774/azkey-bot/internal/roumu/config"
	"github.com/azuki774/azkey-bot/internal/roumu/polling"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logLevel, levelErr := config.ParseLogLevel(os.Getenv("LOG_LEVEL"))
	if levelErr != nil {
		logLevel = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLevel,
	}))
	if levelErr != nil {
		logger.Error("azkey-roumu-bot stopped with an error", "error", levelErr)
		return 1
	}
	if err := run(ctx, os.Getenv, logger); err != nil {
		logger.Error("azkey-roumu-bot stopped with an error", "error", err)
		return 1
	}
	return 0
}

// run loads configuration, assembles the application, and waits for its
// cancellable lifecycle to finish. It intentionally accepts its context and
// environment lookup so the lifecycle can be tested without sending signals
// or changing the process environment.
func run(ctx context.Context, getenv func(string) string, logger *slog.Logger) error {
	return runWithClientFactory(ctx, getenv, logger, func(baseURL *url.URL, token string) (polling.Client, error) {
		return misskey.NewClient(baseURL, token)
	})
}

type clientFactory func(*url.URL, string) (polling.Client, error)

func runWithClientFactory(ctx context.Context, getenv func(string) string, logger *slog.Logger, makeClient clientFactory) error {
	if ctx == nil {
		return errors.New("application context is required")
	}
	if makeClient == nil {
		return errors.New("misskey client factory is required")
	}

	cfg, err := config.LoadFromEnv(getenv)
	if err != nil {
		return err
	}

	client, err := makeClient(cfg.BaseURL(), cfg.Token())
	if err != nil {
		return err
	}
	settings := polling.DefaultSettings()
	configured := cfg.Polling()
	settings.PollInterval = configured.PollInterval
	settings.FollowerSyncInterval = configured.FollowerSyncInterval
	settings.Concurrency = configured.Concurrency
	settings.RatePerSecond = configured.RatePerSecond
	settings.RateBurst = configured.RateBurst
	settings.PageLimit = configured.PageLimit
	settings.MaxPagesPerTurn = configured.MaxPagesPerTurn
	settings.DedupLimit = configured.DedupLimit
	settings.DedupTTL = configured.DedupTTL
	settings.StartupSpread = configured.StartupSpread
	settings.BackoffBase = configured.BackoffBase
	settings.BackoffMax = configured.BackoffMax
	poller, err := polling.New(client, polling.ObservationHandler{Logger: logger}, polling.WithSettings(settings), polling.WithLogger(logger))
	if err != nil {
		return err
	}
	runner, err := bot.NewRunner(poller)
	if err != nil {
		return err
	}

	if logger != nil {
		logger.Info("azkey-roumu-bot started", "polling_mode", configured.Mode)
	}
	if err := runner.Run(ctx); err != nil {
		return err
	}
	if logger != nil {
		logger.Info("azkey-roumu-bot stopped")
	}
	return nil
}
