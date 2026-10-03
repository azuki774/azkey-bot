package bot

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

// ReplyClient contains only the API operations used by the inquiry loop.
type ReplyClient interface {
	Self(context.Context) (domain.User, error)
	ListMentions(context.Context, domain.NotePageOptions) ([]domain.Note, error)
	IsFollowing(context.Context, string) (bool, error)
	CreateReply(context.Context, domain.Note, string) error
}

// ReplySettings bounds inquiry polling independently of follower collection.
type ReplySettings struct {
	PollInterval    time.Duration
	PageLimit       int
	MaxPagesPerTurn int
	RatePerSecond   float64
	BackoffBase     time.Duration
	BackoffMax      time.Duration
	Clock           func() time.Time
	Sleep           func(context.Context, time.Duration) error
	RateLimiter     domain.RequestLimiter
}

func DefaultReplySettings() ReplySettings {
	return ReplySettings{
		PollInterval: time.Minute, PageLimit: 100, MaxPagesPerTurn: 5,
		RatePerSecond: 2, BackoffBase: time.Second, BackoffMax: 5 * time.Minute,
		Clock: time.Now, Sleep: sleepFollowContext,
	}
}

func (s ReplySettings) Validate() error {
	if s.PollInterval <= 0 || s.PageLimit < 1 || s.PageLimit > 100 || s.MaxPagesPerTurn < 1 || s.MaxPagesPerTurn > 10_000 || s.RatePerSecond <= 0 || math.IsNaN(s.RatePerSecond) || math.IsInf(s.RatePerSecond, 0) || s.BackoffBase <= 0 || s.BackoffMax < s.BackoffBase || s.Clock == nil || s.Sleep == nil {
		return errors.New("reply settings are invalid")
	}
	return nil
}

// Replies processes inquiries serially. The cursor advances after every send
// attempt, including an ambiguous failure: notes/create is not idempotent.
// Reads may be retried. Cursors and user data are intentionally volatile.
type Replies struct {
	client      ReplyClient
	repository  UserStateRepository
	settings    ReplySettings
	logger      *slog.Logger
	running     atomic.Bool
	selfID      string
	since       time.Time
	cursor      string
	nextRequest time.Time
	nextReply   time.Time
}

func NewReplies(client ReplyClient, repository UserStateRepository, settings ReplySettings, logger *slog.Logger) (*Replies, error) {
	if client == nil || repository == nil {
		return nil, errors.New("reply client and repository are required")
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}
	return &Replies{client: client, repository: repository, settings: settings, logger: logger}, nil
}

func (r *Replies) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("reply context is required")
	}
	if !r.running.CompareAndSwap(false, true) {
		return errors.New("reply loop is already running")
	}
	defer r.running.Store(false)
	// Starting from the current time avoids replying to historical inquiries,
	// including inquiries whose reply result was lost during a previous run.
	r.since = r.settings.Clock()
	r.cursor = ""
	if err := r.waitRequest(ctx); err != nil {
		return err
	}
	self, err := r.client.Self(ctx)
	if err != nil {
		return err
	}
	if self.ID == "" {
		return errors.New("reply bot ID is missing")
	}
	r.selfID = self.ID
	backoff := r.settings.BackoffBase
	for {
		err := r.poll(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := r.settings.PollInterval
		if err != nil {
			var apiErr *domain.Error
			if errors.As(err, &apiErr) && apiErr.Kind == domain.ErrorKindAuth {
				return err
			}
			r.logFailure("reply polling failed", "")
			delay = backoff
			if backoff < r.settings.BackoffMax/2 {
				backoff *= 2
			} else {
				backoff = r.settings.BackoffMax
			}
		} else {
			backoff = r.settings.BackoffBase
		}
		if err := r.settings.Sleep(ctx, delay); err != nil {
			return err
		}
	}
}

func (r *Replies) poll(ctx context.Context) error {
	for page := 0; page < r.settings.MaxPagesPerTurn; page++ {
		if err := r.waitRequest(ctx); err != nil {
			return err
		}
		options := domain.NotePageOptions{Limit: r.settings.PageLimit, SinceID: r.cursor}
		if r.cursor == "" {
			options.SinceDate = &r.since
		}
		notes, err := r.client.ListMentions(ctx, options)
		if err != nil {
			r.cooldown(err)
			return err
		}
		if len(notes) == 0 {
			return nil
		}
		sort.Slice(notes, func(i, j int) bool { return notes[i].ID < notes[j].ID })
		previous := r.cursor
		for _, note := range notes {
			if note.ID == "" {
				return errors.New("reply note ID is missing")
			}
			if note.ID <= r.cursor {
				continue
			}
			if err := r.handle(ctx, note); err != nil {
				return err
			}
			r.cursor = note.ID
		}
		if previous == r.cursor {
			return errors.New("reply cursor did not advance")
		}
		// Continue even after a short page: server filtering can shorten pages.
	}
	return nil
}

func (r *Replies) isEligibleMention(note domain.Note) bool {
	// ListMentions already restricts notes to mentions of the authenticated bot.
	return note.Visibility == "public" && note.ChannelID == "" &&
		note.UserID != r.selfID && note.User != nil &&
		!note.User.IsBot && note.User.ID == note.UserID
}

func (r *Replies) handle(ctx context.Context, note domain.Note) error {
	if !r.isEligibleMention(note) {
		return nil
	}
	// Space all writes, including failures, below upstream's 300/hour limit.
	if err := r.waitUntil(ctx, r.nextReply); err != nil {
		return err
	}
	if err := r.waitRequest(ctx); err != nil {
		return err
	}
	member, err := r.client.IsFollowing(ctx, note.UserID)
	if err != nil {
		r.cooldown(err)
		return err
	}
	text := "あなたはメンバーではありません"
	if member {
		state, err := r.repository.Get(ctx, note.UserID)
		if err != nil && !errors.Is(err, domain.ErrUserStateNotFound) {
			return err
		}
		text = fmt.Sprintf("連続チェックイン回数: %d連勤、チェックイン回数: %d 日", state.CurrentStreak(r.settings.Clock()), state.CheckInDays)
	}
	if err := r.waitRequest(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	r.nextReply = r.settings.Clock().Add(15 * time.Second)
	err = r.client.CreateReply(ctx, note, text)
	if err != nil {
		r.cooldown(err)
		r.logFailure("reply send failed; not retrying this note", note.ID)
		var apiErr *domain.Error
		if errors.As(err, &apiErr) && apiErr.Kind == domain.ErrorKindAuth {
			return err
		}
	}
	return nil
}

func (r *Replies) waitRequest(ctx context.Context) error {
	if r.settings.RateLimiter != nil {
		return r.settings.RateLimiter.Wait(ctx)
	}
	if err := r.waitUntil(ctx, r.nextRequest); err != nil {
		return err
	}
	r.nextRequest = r.settings.Clock().Add(time.Duration(float64(time.Second) / r.settings.RatePerSecond))
	return nil
}

func (r *Replies) waitUntil(ctx context.Context, until time.Time) error {
	if delay := until.Sub(r.settings.Clock()); delay > 0 {
		return r.settings.Sleep(ctx, delay)
	}
	return ctx.Err()
}

func (r *Replies) cooldown(err error) {
	var apiErr *domain.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindRateLimit {
		return
	}
	delay := r.settings.BackoffMax
	if apiErr.RetryAfter != nil && *apiErr.RetryAfter > delay {
		delay = *apiErr.RetryAfter
	}
	until := r.settings.Clock().Add(delay)
	if r.settings.RateLimiter != nil {
		r.settings.RateLimiter.SetCooldown(until)
	}
	if until.After(r.nextRequest) {
		r.nextRequest = until
	}
}

func (r *Replies) logFailure(message, noteID string) {
	if r.logger != nil {
		r.logger.Warn(message, "note_id", noteID)
	}
}
