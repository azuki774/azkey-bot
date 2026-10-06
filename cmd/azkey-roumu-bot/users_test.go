package main

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/config"
)

func TestRunPersistsCheckInsAndSharesStateWithInquiries(t *testing.T) {
	server := miniredis.RunT(t)
	server.RequireUserAuth("bot", "valkey-test-secret")
	file := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(file, []byte("valkey-test-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://example.test", "MISSKEY_TOKEN": "misskey-test-secret",
		"POLL_INTERVAL": "1ms", "POLL_STARTUP_SPREAD": "0s", "POLL_RATE_PER_SECOND": "10000",
		"KVS_BACKEND": "valkey", "KVS_URL": "redis://" + server.Addr(), "KVS_NAMESPACE": "prod-bot",
		"KVS_USERNAME": "bot", "KVS_PASSWORD_FILE": file,
	}
	cfg, err := config.LoadFromEnv(mainMapLookup(env))
	if err != nil {
		t.Fatal(err)
	}
	users, closeUsers, err := openUsers(context.Background(), cfg.KVS())
	if err != nil {
		t.Fatal(err)
	}
	err = users.Put(context.Background(), "member", domain.UserState{CheckInDays: 4, ConsecutiveDays: 2, LastCheckInAt: time.Now().Add(-24 * time.Hour)})
	closeUsers()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &checkInApplicationClient{t: t, reacted: make(chan struct{}), cancel: cancel}
	capture := newLogCapture()
	err = runWithClientFactory(ctx, mainMapLookup(env), slog.New(slog.NewTextHandler(capture, nil)), func(*url.URL, string) (applicationClient, error) { return client, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"🔥 連続出勤: 3連勤", "📈 累計出勤: 5日"} {
		if !strings.Contains(client.reply, expected) {
			t.Fatalf("reply %q is missing %q", client.reply, expected)
		}
	}
	users, closeUsers, err = openUsers(context.Background(), cfg.KVS())
	if err != nil {
		t.Fatal(err)
	}
	defer closeUsers()
	got, err := users.Get(context.Background(), "member")
	if err != nil || got.CheckInDays != 5 || got.ConsecutiveDays != 3 {
		t.Fatalf("persisted state = %+v, %v", got, err)
	}
	if len(server.Keys()) != 1 {
		t.Fatalf("unexpected progress or metadata keys: %v", server.Keys())
	}
	for _, secret := range []string{"valkey-test-secret", "misskey-test-secret", file} {
		if strings.Contains(capture.String(), secret) {
			t.Fatal("secret appeared in logs")
		}
	}
}

func TestRunRejectsValkeyAuthFailureWithoutMemoryFallback(t *testing.T) {
	server := miniredis.RunT(t)
	server.RequireAuth("correct-password")
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://example.test", "MISSKEY_TOKEN": "misskey-secret",
		"KVS_BACKEND": "valkey", "KVS_URL": "redis://" + server.Addr(),
		"KVS_NAMESPACE": "prod-bot", "KVS_PASSWORD": "wrong-password",
	}
	capture := newLogCapture()
	factory := func(*url.URL, string) (applicationClient, error) { return mainTestClient{}, nil }
	err := runWithClientFactory(context.Background(), mainMapLookup(env), slog.New(slog.NewTextHandler(capture, nil)), factory)
	if err == nil {
		t.Fatal("startup accepted failed authentication")
	}
	if strings.Contains(err.Error()+capture.String(), "wrong-password") || strings.Contains(capture.String(), "azkey-roumu-bot started") {
		t.Fatal("startup leaked the password or fell back to memory")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWithClientFactory(ctx, mainMapLookup(env), nil, factory); err != nil {
		t.Fatalf("startup cancellation: %v", err)
	}
}
