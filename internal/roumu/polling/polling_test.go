package polling

import (
	"context"
	"net/url"
	"testing"

	"github.com/azuki774/azkey-bot/internal/misskey"
)

func TestRunReturnsAfterCancellation(t *testing.T) {
	client, err := misskey.NewClient(mustBaseURL(t), "polling-test-token")
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	poller, err := New(client)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func mustBaseURL(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse("https://misskey.example.test")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	return u
}
