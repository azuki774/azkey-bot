// Command azkey-roumu-bot answers check-in inquiries.
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
	"github.com/azuki774/azkey-bot/internal/roumu/repository/memory"
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
	return runWithClientFactory(ctx, getenv, logger, func(baseURL *url.URL, token string) (applicationClient, error) {
		return misskey.NewClient(baseURL, token)
	})
}

type applicationClient interface {
	bot.ReactionClient
	bot.ReplyClient
	bot.FollowClient
	polling.Client
}

type clientFactory func(*url.URL, string) (applicationClient, error)

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
	limiter, err := polling.NewRateLimiter(settings.RatePerSecond, settings.RateBurst)
	if err != nil {
		return err
	}
	settings.RateLimiter = limiter
	followSyncSettings := bot.DefaultFollowSyncSettings()
	followSyncSettings.MaxWritesPerSync = configured.FollowMaxWritesPerSync
	followSyncSettings.WriteInterval = configured.FollowWriteInterval
	followSyncSettings.BackoffBase = configured.BackoffBase
	followSyncSettings.BackoffMax = configured.BackoffMax
	followSynchronizer, err := bot.NewFollowerSynchronizer(client, limiter, followSyncSettings, logger)
	if err != nil {
		return err
	}
	settings.FollowerSynchronizer = followSynchronizer
	users := &memory.Users{}
	checkins, err := bot.NewCheckIns(client, users, limiter, bot.DefaultCheckInSettings(), logger)
	if err != nil {
		return err
	}
	poller, err := polling.New(client, checkins, polling.WithSettings(settings), polling.WithLogger(logger))
	if err != nil {
		return err
	}
	replySettings := bot.DefaultReplySettings()
	replySettings.PollInterval = configured.PollInterval
	replySettings.PageLimit = configured.PageLimit
	replySettings.MaxPagesPerTurn = configured.MaxPagesPerTurn
	replySettings.RatePerSecond = configured.RatePerSecond
	replySettings.BackoffBase = configured.BackoffBase
	replySettings.BackoffMax = configured.BackoffMax
	replySettings.RateLimiter = limiter
	replies, err := bot.NewReplies(client, users, replySettings, logger)
	if err != nil {
		return err
	}
	runner, err := bot.NewRunner(consumerGroup{poller, replies})
	if err != nil {
		return err
	}

	if logger != nil {
		logger.Info("azkey-roumu-bot started")
	}
	if err := runner.Run(ctx); err != nil {
		return err
	}
	if logger != nil {
		logger.Info("azkey-roumu-bot stopped")
	}
	return nil
}

// consumerGroup cancels sibling loops when one stops and joins all of them.
type consumerGroup []bot.Consumer

func (consumers consumerGroup) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan error, len(consumers))
	for _, consumer := range consumers {
		go func() { results <- consumer.Run(ctx) }()
	}
	var result error
	for range consumers {
		err := <-results
		if err != nil && !errors.Is(err, context.Canceled) && result == nil {
			result = err
		}
		cancel()
	}
	return result
}
