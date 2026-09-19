// Package polling owns the cancellable azkey-roumu-bot polling lifecycle.
package polling

import (
	"context"
	"errors"

	"github.com/azuki774/azkey-bot/internal/misskey"
)

var (
	errClientRequired  = errors.New("misskey client is required")
	errContextRequired = errors.New("polling context is required")
)

// Poller is the lifecycle boundary for future polling work. It currently
// waits for cancellation and performs no requests.
type Poller struct {
	client *misskey.Client
}

// New creates a poller bound to the configured Misskey client.
func New(client *misskey.Client) (*Poller, error) {
	if client == nil {
		return nil, errClientRequired
	}
	return &Poller{client: client}, nil
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
