// Command azkey-bot starts the azkey bot scaffold.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/azuki774/azkey-bot/internal/bot"
	"github.com/azuki774/azkey-bot/internal/config"
	"github.com/azuki774/azkey-bot/internal/misskey"
	"github.com/azuki774/azkey-bot/internal/polling"
)

func main() {
	os.Exit(runMain())
}

func runMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	if err := run(ctx, os.Getenv, logger); err != nil {
		logger.Error("azkey-bot stopped with an error", "error", err)
		return 1
	}
	return 0
}

// run loads configuration, assembles the application, and waits for its
// cancellable lifecycle to finish. It intentionally accepts its context and
// environment lookup so the lifecycle can be tested without sending signals
// or changing the process environment.
func run(ctx context.Context, getenv func(string) string, logger *slog.Logger) error {
	if ctx == nil {
		return errors.New("application context is required")
	}

	cfg, err := config.LoadFromEnv(getenv)
	if err != nil {
		return err
	}

	client, err := misskey.NewClient(cfg.BaseURL(), cfg.Token())
	if err != nil {
		return err
	}
	poller, err := polling.New(client)
	if err != nil {
		return err
	}
	runner, err := bot.NewRunner(poller)
	if err != nil {
		return err
	}

	if logger != nil {
		logger.Info("azkey-bot started")
	}
	if err := runner.Run(ctx); err != nil {
		return err
	}
	if logger != nil {
		logger.Info("azkey-bot stopped")
	}
	return nil
}
