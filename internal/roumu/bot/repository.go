package bot

import (
	"context"
	"github.com/azuki774/azkey-bot/internal/domain"
)

// UserStateRepository returns ErrUserStateNotFound for an unregistered user.
// Get/Put copy values; callers must serialize read-modify-write operations.
type UserStateRepository interface {
	Get(context.Context, string) (domain.UserState, error)
	Put(context.Context, string, domain.UserState) error
}
