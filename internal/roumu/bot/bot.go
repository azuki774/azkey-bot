// Package bot coordinates the azkey-roumu-bot application lifecycle.
package bot

import (
	"context"
	"errors"

	"github.com/azuki774/azkey-bot/internal/domain"
)

var (
	errConsumerRequired = errors.New("bot consumer is required")
	errContextRequired  = errors.New("bot context is required")
)

// Consumer is the small lifecycle interface implemented by the active work
// loop.
type Consumer interface {
	Run(context.Context) error
}

// MisskeyClient is the small consumer-side view of the shared Misskey
// client. It keeps HTTP DTOs and transport details out of roumu code.
type MisskeyClient interface {
	Self(context.Context) (domain.User, error)
	ListFollowers(context.Context, string, domain.PageOptions) ([]domain.Following, error)
	ListFollowing(context.Context, string, domain.PageOptions) ([]domain.Following, error)
	ListUserNotes(context.Context, string, domain.NotePageOptions) ([]domain.Note, error)
	CreateFollow(context.Context, string) (domain.User, error)
	CreateReaction(context.Context, string, string) error
}

// Runner delegates lifecycle control to its consumer.
type Runner struct {
	consumer Consumer
}

// NewRunner creates a bot runner with the supplied consumer.
func NewRunner(consumer Consumer) (*Runner, error) {
	if consumer == nil {
		return nil, errConsumerRequired
	}
	return &Runner{consumer: consumer}, nil
}

// Run starts the consumer and treats an explicit context cancellation as a
// graceful stop.
func (r *Runner) Run(ctx context.Context) error {
	if r == nil || r.consumer == nil {
		return errConsumerRequired
	}
	if ctx == nil {
		return errContextRequired
	}

	err := r.consumer.Run(ctx)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
