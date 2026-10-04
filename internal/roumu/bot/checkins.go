package bot

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

type ReactionClient interface {
	CreateReaction(context.Context, string, string) error
}

// CheckInSettings holds literal, case-sensitive substring rules in code.
type CheckInSettings struct {
	Keywords []string
	Reaction string
}

func DefaultCheckInSettings() CheckInSettings {
	return CheckInSettings{Keywords: domain.DefaultCheckInKeywords(), Reaction: "✅"}
}

// CheckIns consumes follower notes selected by polling, which excludes self
// and checks membership immediately before delivery. Use one shared instance
// for all writers to this repository. Locks are bounded, striped by user ID.
type CheckIns struct {
	client     ReactionClient
	repository UserStateRepository
	limiter    domain.RequestLimiter
	settings   CheckInSettings
	logger     *slog.Logger
	locks      [256]sync.Mutex
}

func NewCheckIns(client ReactionClient, repository UserStateRepository, limiter domain.RequestLimiter, settings CheckInSettings, logger *slog.Logger) (*CheckIns, error) {
	if client == nil || repository == nil || limiter == nil {
		return nil, errors.New("check-in client, repository and limiter are required")
	}
	if len(settings.Keywords) == 0 || strings.TrimSpace(settings.Reaction) == "" {
		return nil, errors.New("check-in keywords and reaction are required")
	}
	for _, keyword := range settings.Keywords {
		if strings.TrimSpace(keyword) == "" {
			return nil, errors.New("check-in keyword must not be empty")
		}
	}
	settings.Keywords = append([]string(nil), settings.Keywords...)
	return &CheckIns{client: client, repository: repository, limiter: limiter, settings: settings, logger: logger}, nil
}

func (c *CheckIns) HandleNote(ctx context.Context, note domain.Note) error {
	if ctx == nil {
		return errors.New("check-in context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Quotes and replies use only their own text; pure renotes have no text.
	if note.Visibility != "public" || note.ChannelID != "" || note.Text == nil ||
		note.User == nil || note.User.IsBot || note.UserID == "" || note.User.ID != note.UserID {
		return nil
	}
	matched := false
	for _, keyword := range c.settings.Keywords {
		if strings.Contains(*note.Text, keyword) {
			matched = true
			break
		}
	}
	if !matched {
		return nil
	}
	if note.ID == "" || note.CreatedAt.IsZero() {
		return errors.New("check-in note ID and creation time are required")
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(note.UserID))
	lock := &c.locks[hash.Sum32()%uint32(len(c.locks))]
	lock.Lock()
	defer lock.Unlock()

	state, err := c.repository.Get(ctx, note.UserID)
	if errors.Is(err, domain.ErrUserStateNotFound) {
		state = domain.UserState{}
	} else if err != nil {
		return err
	}
	day := domain.CheckInDay(note.CreatedAt)
	lastDay := domain.CheckInDay(state.LastCheckInAt)
	// Never rewind state for late or replayed notes, even after dedup eviction.
	if !state.LastCheckInAt.IsZero() && !day.After(lastDay) {
		return nil
	}
	state.CheckInDays++
	if !state.LastCheckInAt.IsZero() && day.Sub(lastDay) == 24*time.Hour {
		state.ConsecutiveDays++
	} else {
		state.ConsecutiveDays = 1
	}
	state.LastCheckInAt = note.CreatedAt
	// Wait before saving: cancellation while awaiting an API slot must not
	// consume the day's check-in without attempting a reaction.
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	if err := c.repository.Put(ctx, note.UserID, state); err != nil {
		// A retry reads back state, including a write whose acknowledgement was
		// lost. No reaction is sent without a confirmed successful save.
		return err
	}
	err = c.client.CreateReaction(ctx, note.ID, c.settings.Reaction)
	if err != nil {
		if c.logger != nil {
			c.logger.Warn("check-in reaction failed; not retrying this day", "note_id", note.ID)
		}
		var apiErr *domain.Error
		if errors.As(err, &apiErr) {
			if apiErr.Kind == domain.ErrorKindRateLimit {
				delay := 5 * time.Minute
				if apiErr.RetryAfter != nil && *apiErr.RetryAfter > delay {
					delay = *apiErr.RetryAfter
				}
				c.limiter.SetCooldown(time.Now().Add(delay))
			}
			if apiErr.Kind == domain.ErrorKindAuth {
				return err
			}
		}
	}
	// A failed/ambiguous send does not roll back the saved day or get retried.
	return nil
}
