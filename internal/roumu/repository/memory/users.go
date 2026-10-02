package memory

import (
	"context"
	"github.com/azuki774/azkey-bot/internal/domain"
	"sync"
)

// Users is a volatile, concurrency-safe repository. Its zero value is ready to use.
type Users struct {
	mu     sync.RWMutex
	states map[string]domain.UserState
}

func (r *Users) Get(ctx context.Context, id string) (domain.UserState, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return domain.UserState{}, err
	}
	state, ok := r.states[id]
	if !ok {
		return domain.UserState{}, domain.ErrUserStateNotFound
	}
	return state, nil
}

func (r *Users) Put(ctx context.Context, id string, state domain.UserState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.states == nil {
		r.states = make(map[string]domain.UserState)
	}
	r.states[id] = state
	return nil
}
