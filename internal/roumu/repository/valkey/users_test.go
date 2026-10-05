package valkey

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/bot"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/repositorytest"
	"github.com/redis/go-redis/v9"
)

func openTestUsers(t *testing.T, addr, namespace string) *Users {
	t.Helper()
	r, err := NewUsers(context.Background(), &redis.Options{Addr: addr}, namespace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func TestUsers(t *testing.T) {
	server := miniredis.RunT(t)
	repositorytest.RunUserStateRepositoryTests(t, func() bot.UserStateRepository {
		return openTestUsers(t, server.Addr(), "test")
	})
}

// CI supplies a real Valkey service. Local unit tests need no external server.
func TestValkeyServer(t *testing.T) {
	addr := os.Getenv("VALKEY_TEST_ADDR")
	if addr == "" {
		t.Skip("set VALKEY_TEST_ADDR to test a real Valkey server")
	}
	namespace := fmt.Sprintf("test-%d", time.Now().UnixNano())
	repositorytest.RunUserStateRepositoryTests(t, func() bot.UserStateRepository {
		return openTestUsers(t, addr, namespace)
	})
	testRestartAndIsolation(t, addr, namespace+"-restart")
}

func TestRestartAndIsolation(t *testing.T) {
	server := miniredis.RunT(t)
	testRestartAndIsolation(t, server.Addr(), "production:*")
	for _, key := range server.Keys() {
		if server.TTL(key) != 0 {
			t.Fatal("user state must not expire")
		}
	}
}

func testRestartAndIsolation(t *testing.T, addr, namespace string) {
	t.Helper()
	ctx := context.Background()
	r := openTestUsers(t, addr, namespace)
	state := domain.UserState{CheckInDays: 10, ConsecutiveDays: 3, LastCheckInAt: time.Date(2026, 10, 5, 12, 0, 0, 0, time.FixedZone("JST", 9*3600))}
	if err := r.Put(ctx, "user:[]", state); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := openTestUsers(t, addr, namespace)
	got, err := restarted.Get(ctx, "user:[]")
	if err != nil || got.CheckInDays != 10 || got.ConsecutiveDays != 3 || !got.LastCheckInAt.Equal(state.LastCheckInAt) {
		t.Fatalf("restored state: %+v %v", got, err)
	}
	other := openTestUsers(t, addr, namespace+":other")
	if _, err := other.Get(ctx, "user:[]"); !errors.Is(err, domain.ErrUserStateNotFound) {
		t.Fatalf("namespace collision: %v", err)
	}
	if ttl, err := restarted.client.TTL(ctx, restarted.prefix+encode("user:[]")).Result(); err != nil || ttl != -1 {
		t.Fatalf("TTL = %v, %v", ttl, err)
	}
}

func TestCorruptionAndErrorsAreNotMissingState(t *testing.T) {
	server := miniredis.RunT(t)
	r := openTestUsers(t, server.Addr(), "test")
	ctx := context.Background()
	for _, raw := range []string{`invalid`, `null`, `{}`, `{"version":2,"checkInDays":1,"consecutiveDays":1,"lastCheckInAt":"2026-10-05T00:00:00Z"}`, `{"version":1,"checkInDays":1}`} {
		if err := server.Set(r.prefix+encode("user"), raw); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Get(ctx, "user"); !errors.Is(err, ErrInvalidState) {
			t.Fatalf("corruption returned %v", err)
		}
	}
	server.SetError("ERR sensitive server text")
	if _, err := r.Get(ctx, "missing"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("read failure: %v", err)
	}
	if err := r.Put(ctx, "user", domain.UserState{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("write failure: %v", err)
	}
	server.SetError("")
	// A failed write did not replace the invalid value with an initial state.
	if _, err := r.Get(ctx, "user"); !errors.Is(err, ErrInvalidState) {
		t.Fatal("failed save changed value")
	}
}

func TestAuthenticationAndCancellation(t *testing.T) {
	server := miniredis.RunT(t)
	server.RequireUserAuth("bot", "test-secret")
	ctx := context.Background()
	if _, err := NewUsers(ctx, &redis.Options{Addr: server.Addr(), Username: "bot", Password: "wrong"}, "test"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("auth failure: %v", err)
	}
	r, err := NewUsers(ctx, &redis.Options{Addr: server.Addr(), Username: "bot", Password: "test-secret"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if err := r.Put(ctx, "user", domain.UserState{CheckInDays: 1}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := NewUsers(canceled, &redis.Options{Addr: server.Addr()}, "test"); !errors.Is(err, context.Canceled) {
		t.Fatalf("startup cancellation: %v", err)
	}
}
