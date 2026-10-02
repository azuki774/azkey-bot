package memory_test

import (
	"testing"

	"github.com/azuki774/azkey-bot/internal/roumu/bot"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/memory"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/repositorytest"
)

func TestUsers(t *testing.T) {
	repositorytest.RunUserStateRepositoryTests(t, func() bot.UserStateRepository {
		return &memory.Users{}
	})
}
