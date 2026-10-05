// Package valkey persists check-in state in a Redis-compatible Valkey server.
package valkey

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/bot"
	"github.com/redis/go-redis/v9"
)

var (
	ErrUnavailable  = errors.New("valkey user state operation failed")
	ErrInvalidState = errors.New("valkey user state is invalid or has an unsupported version")
)

// Users implements atomic whole-value writes, not distributed read-modify-write.
// One bot process must own a namespace. Keys have no expiration.
type Users struct {
	client *redis.Client
	prefix string
}

var _ bot.UserStateRepository = (*Users)(nil)

// NewUsers connects and authenticates before returning. The namespace must be
// unique to the environment, Misskey instance and bot, and stable across restarts.
// Close must be called after all users of the repository have stopped.
func NewUsers(ctx context.Context, options *redis.Options, namespace string) (*Users, error) {
	if ctx == nil || options == nil || strings.TrimSpace(namespace) == "" {
		return nil, errors.New("valkey context, options and namespace are required")
	}
	opts := *options
	opts.Protocol = 2
	opts.DisableIdentity = true
	opts.MaxRetries = -1 // The handler retries by reading state, including ambiguous writes.
	opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout = 5*time.Second, 5*time.Second, 5*time.Second
	opts.ContextTimeoutEnabled = true
	client := redis.NewClient(&opts)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, storageError(ctx)
	}
	return &Users{client: client, prefix: "roumu:v1:" + encode(namespace) + ":users:"}, nil
}

func (r *Users) Close() error    { return r.client.Close() }
func encode(value string) string { return base64.RawURLEncoding.EncodeToString([]byte(value)) }

// Pointers distinguish missing/null fields from valid zero-valued user state.
type storedState struct {
	Version         int        `json:"version"`
	CheckInDays     *int       `json:"checkInDays"`
	ConsecutiveDays *int       `json:"consecutiveDays"`
	LastCheckInAt   *time.Time `json:"lastCheckInAt"`
}

func (r *Users) Get(ctx context.Context, id string) (domain.UserState, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := r.client.Get(ctx, r.prefix+encode(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return domain.UserState{}, domain.ErrUserStateNotFound
	}
	if err != nil {
		return domain.UserState{}, storageError(ctx)
	}
	var state storedState
	if json.Unmarshal(data, &state) != nil || state.Version != 1 || state.CheckInDays == nil || state.ConsecutiveDays == nil || state.LastCheckInAt == nil {
		return domain.UserState{}, ErrInvalidState
	}
	return domain.UserState{CheckInDays: *state.CheckInDays, ConsecutiveDays: *state.ConsecutiveDays, LastCheckInAt: *state.LastCheckInAt}, nil
}

func (r *Users) Put(ctx context.Context, id string, state domain.UserState) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(storedState{Version: 1, CheckInDays: &state.CheckInDays, ConsecutiveDays: &state.ConsecutiveDays, LastCheckInAt: &state.LastCheckInAt})
	if err != nil {
		return ErrInvalidState
	}
	if err := r.client.Set(ctx, r.prefix+encode(id), data, 0).Err(); err != nil {
		return storageError(ctx)
	}
	return nil
}

// Raw server errors may contain sensitive data; expose only a stable error.
func storageError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return ErrUnavailable
}
