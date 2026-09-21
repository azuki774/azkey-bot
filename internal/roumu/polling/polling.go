// Package polling owns the cancellable azkey-roumu-bot polling lifecycle.
package polling

import (
	"context"
	"errors"
	"reflect"

	"github.com/azuki774/azkey-bot/internal/roumu/bot"
)

var (
	errClientRequired  = errors.New("misskey client is required")
	errContextRequired = errors.New("polling context is required")
)

// Poller is the lifecycle boundary for future polling work. It currently
// waits for cancellation and performs no requests.
type Poller struct {
	client bot.MisskeyClient
}

// New creates a poller bound to the configured Misskey client.
func New(client bot.MisskeyClient) (*Poller, error) {
	if nilMisskeyClient(client) {
		return nil, errClientRequired
	}
	return &Poller{client: client}, nil
}

func nilMisskeyClient(client bot.MisskeyClient) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

// Run waits until ctx is canceled. Actual polling and endpoint calls are
// intentionally deferred until their behavior is specified.
func (p *Poller) Run(ctx context.Context) error {
	if p == nil {
		return errClientRequired
	}
	if ctx == nil {
		return errContextRequired
	}
	<-ctx.Done()
	return nil
}
