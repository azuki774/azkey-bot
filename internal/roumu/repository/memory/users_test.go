package memory_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/bot"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/memory"
)

func TestUsers(t *testing.T) {
	testRepository(t, func() bot.UserStateRepository { return &memory.Users{} })
}

func testRepository(t *testing.T, newRepository func() bot.UserStateRepository) {
	t.Helper()
	r := newRepository()
	ctx := context.Background()
	if _, err := r.Get(ctx, "missing"); !errors.Is(err, domain.ErrUserStateNotFound) {
		t.Fatalf("missing: %v", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := fmt.Sprint(i)
			for days := 1; days <= 10; days++ {
				state := domain.UserState{CheckInDays: days}
				if err := r.Put(ctx, id, state); err != nil {
					t.Error(err)
					return
				}
				state.CheckInDays = -1
				got, err := r.Get(ctx, id)
				if err != nil || got.CheckInDays != days {
					t.Errorf("get: %+v %v", got, err)
					return
				}
				got.CheckInDays = -2
				again, _ := r.Get(ctx, id)
				if again.CheckInDays != days {
					t.Error("returned value shares state")
				}
			}
		}()
	}
	wg.Wait()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.Put(canceled, "missing", domain.UserState{}); !errors.Is(err, context.Canceled) {
		t.Errorf("put cancellation: %v", err)
	}
	if _, err := r.Get(canceled, "missing"); !errors.Is(err, context.Canceled) {
		t.Errorf("get cancellation: %v", err)
	}
	if _, err := r.Get(ctx, "missing"); !errors.Is(err, domain.ErrUserStateNotFound) {
		t.Errorf("canceled put wrote state: %v", err)
	}
}
