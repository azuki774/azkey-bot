package bot

import (
	"context"
	"errors"
	"testing"

	"github.com/azuki774/azkey-bot/internal/misskey"
)

var _ MisskeyClient = (*misskey.Client)(nil)

type consumerFunc func(context.Context) error

func (f consumerFunc) Run(ctx context.Context) error {
	return f(ctx)
}

func TestRunnerDelegatesAndTreatsCancellationAsGraceful(t *testing.T) {
	started := make(chan struct{})
	consumer := consumerFunc(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	runner, err := NewRunner(consumer)
	if err != nil {
		t.Fatalf("NewRunner returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx)
	}()
	<-started
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestRunnerReturnsConsumerError(t *testing.T) {
	wantErr := errors.New("consumer failed")
	runner, err := NewRunner(consumerFunc(func(context.Context) error {
		return wantErr
	}))
	if err != nil {
		t.Fatalf("NewRunner returned error: %v", err)
	}
	if err := runner.Run(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Run error = %v, want %v", err, wantErr)
	}
}
