// Package bot coordinates the azkey-roumu-bot application lifecycle.
package bot

import (
	"context"
	"errors"
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
