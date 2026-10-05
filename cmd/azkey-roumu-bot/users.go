package main

import (
	"context"
	"crypto/tls"

	"github.com/azuki774/azkey-bot/internal/roumu/bot"
	"github.com/azuki774/azkey-bot/internal/roumu/config"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/memory"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/valkey"
	"github.com/redis/go-redis/v9"
)

func openUsers(ctx context.Context, settings config.KVSSettings) (bot.UserStateRepository, func(), error) {
	if settings.Backend == "memory" {
		return &memory.Users{}, func() {}, nil
	}
	options := &redis.Options{Addr: settings.Address, Username: settings.Username, Password: settings.Password(), DB: settings.DB}
	if settings.TLS {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	users, err := valkey.NewUsers(ctx, options, settings.Namespace)
	if err != nil {
		return nil, nil, err
	}
	return users, func() { _ = users.Close() }, nil
}
