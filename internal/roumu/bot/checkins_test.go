package bot

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/memory"
)

type checkInReactionFunc func(context.Context, string, string) error

func (f checkInReactionFunc) CreateReaction(ctx context.Context, id, reaction string) error {
	return f(ctx, id, reaction)
}

type checkInLimiter struct {
	err      error
	cooldown time.Time
}

func (l *checkInLimiter) Wait(ctx context.Context) error {
	if l.err != nil {
		return l.err
	}
	return ctx.Err()
}
func (l *checkInLimiter) SetCooldown(at time.Time) { l.cooldown = at }

func checkInNote() domain.Note {
	text := "今日も出勤！"
	return domain.Note{ID: "note", UserID: "user", User: &domain.User{ID: "user"}, Visibility: "public", Text: &text,
		CreatedAt: time.Date(2026, 10, 3, 4, 59, 0, 0, time.FixedZone("JST", 9*60*60))}
}

func newTestCheckIns(t *testing.T, repository UserStateRepository, client ReactionClient, limiter *checkInLimiter) *CheckIns {
	t.Helper()
	c, err := NewCheckIns(client, repository, limiter, DefaultCheckInSettings(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCheckInRules(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*domain.Note)
		want int
	}{
		{"normal", func(n *domain.Note) {}, 1},
		{"login", func(n *domain.Note) { s := "ログインボーナスください"; n.Text = &s }, 1},
		{"multiple", func(n *domain.Note) { s := "ログボ 出勤 ログボ"; n.Text = &s }, 1},
		{"reply", func(n *domain.Note) { n.ReplyID = "parent" }, 1},
		{"pure renote", func(n *domain.Note) { n.Text = nil }, 0},
		{"unmatched quote text", func(n *domain.Note) { s := "引用します"; n.Text = &s }, 0},
		{"cw only", func(n *domain.Note) { n.CW = n.Text; s := "hello"; n.Text = &s }, 0},
		{"bot", func(n *domain.Note) { n.User.IsBot = true }, 0},
		{"missing user", func(n *domain.Note) { n.User = nil }, 0},
		{"wrong user", func(n *domain.Note) { n.User.ID = "other" }, 0},
		{"private", func(n *domain.Note) { n.Visibility = "followers" }, 0},
		{"channel", func(n *domain.Note) { n.ChannelID = "channel" }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &memory.Users{}
			calls := 0
			c := newTestCheckIns(t, repo, checkInReactionFunc(func(ctx context.Context, id, reaction string) error {
				calls++
				state, err := repo.Get(ctx, "user")
				if err != nil || state.CheckInDays != 1 || id != "note" || reaction != "✅" {
					t.Fatalf("reaction before save or incorrect: %+v %v %s %s", state, err, id, reaction)
				}
				return nil
			}), &checkInLimiter{})
			n := checkInNote()
			tc.edit(&n)
			if err := c.HandleNote(context.Background(), n); err != nil {
				t.Fatal(err)
			}
			if calls != tc.want {
				t.Fatalf("calls = %d, want %d", calls, tc.want)
			}
			if tc.want == 0 {
				if _, err := repo.Get(context.Background(), "user"); !errors.Is(err, domain.ErrUserStateNotFound) {
					t.Fatalf("unexpected state: %v", err)
				}
			}
		})
	}
}

func TestCheckInDaysAndReplay(t *testing.T) {
	repo := &memory.Users{}
	calls := 0
	c := newTestCheckIns(t, repo, checkInReactionFunc(func(context.Context, string, string) error { calls++; return nil }), &checkInLimiter{})
	n := checkInNote()
	for i, tc := range []struct {
		offset       time.Duration
		days, streak int
	}{
		{0, 1, 1}, {0, 1, 1}, {time.Minute, 2, 2}, {time.Hour, 2, 2},
		{48*time.Hour + time.Minute, 3, 1}, {0, 3, 1},
	} {
		n.ID = fmt.Sprint(i)
		n.CreatedAt = checkInNote().CreatedAt.Add(tc.offset)
		if err := c.HandleNote(context.Background(), n); err != nil {
			t.Fatal(err)
		}
		s, _ := repo.Get(context.Background(), n.UserID)
		if s.CheckInDays != tc.days || s.ConsecutiveDays != tc.streak || calls != tc.days {
			t.Fatalf("step %d: %+v, calls %d", i, s, calls)
		}
	}
}

type failingCheckInRepository struct {
	memory.Users
	getErr, putErr error
	writeOnError   bool
}

func (r *failingCheckInRepository) Get(ctx context.Context, id string) (domain.UserState, error) {
	if r.getErr != nil {
		return domain.UserState{}, r.getErr
	}
	return r.Users.Get(ctx, id)
}
func (r *failingCheckInRepository) Put(ctx context.Context, id string, state domain.UserState) error {
	if r.putErr == nil || r.writeOnError {
		if err := r.Users.Put(ctx, id, state); err != nil {
			return err
		}
	}
	return r.putErr
}

func TestCheckInRepositoryFailures(t *testing.T) {
	for _, phase := range []string{"read", "save", "save acknowledgement"} {
		t.Run(phase, func(t *testing.T) {
			failure := errors.New("storage unavailable")
			repo := &failingCheckInRepository{}
			if phase == "read" {
				repo.getErr = failure
			} else {
				repo.putErr = failure
				repo.writeOnError = phase == "save acknowledgement"
			}
			calls := 0
			c := newTestCheckIns(t, repo, checkInReactionFunc(func(context.Context, string, string) error { calls++; return nil }), &checkInLimiter{})
			if err := c.HandleNote(context.Background(), checkInNote()); !errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if calls != 0 {
				t.Fatal("sent on repository failure")
			}
			repo.getErr, repo.putErr = nil, nil
			for range 2 {
				if err := c.HandleNote(context.Background(), checkInNote()); err != nil {
					t.Fatal(err)
				}
			}
			s, _ := repo.Get(context.Background(), "user")
			wantCalls := 1
			if repo.writeOnError {
				wantCalls = 0
			}
			if s.CheckInDays != 1 || calls != wantCalls {
				t.Fatalf("state %+v, calls %d", s, calls)
			}
		})
	}
}

func TestCheckInReactionFailures(t *testing.T) {
	for _, kind := range []domain.ErrorKind{domain.ErrorKindClient, domain.ErrorKindServer, domain.ErrorKindNetworkTimeout, domain.ErrorKindNetwork, domain.ErrorKindAuth, domain.ErrorKindRateLimit, domain.ErrorKindCanceled} {
		t.Run(string(kind), func(t *testing.T) {
			repo := &memory.Users{}
			limiter := &checkInLimiter{}
			calls := 0
			failure := domain.NewError(kind, 0, "", nil)
			c := newTestCheckIns(t, repo, checkInReactionFunc(func(context.Context, string, string) error { calls++; return failure }), limiter)
			err := c.HandleNote(context.Background(), checkInNote())
			if (kind == domain.ErrorKindAuth) != errors.Is(err, failure) {
				t.Fatalf("error = %v", err)
			}
			if err := c.HandleNote(context.Background(), checkInNote()); err != nil {
				t.Fatal(err)
			}
			s, _ := repo.Get(context.Background(), "user")
			if calls != 1 || s.CheckInDays != 1 {
				t.Fatalf("calls %d, state %+v", calls, s)
			}
			if kind == domain.ErrorKindRateLimit && limiter.cooldown.IsZero() {
				t.Fatal("missing shared cooldown")
			}
		})
	}
}

func TestCheckInCanceledWaitDoesNotSave(t *testing.T) {
	repo := &memory.Users{}
	c := newTestCheckIns(t, repo, checkInReactionFunc(func(context.Context, string, string) error { t.Fatal("unexpected send"); return nil }), &checkInLimiter{err: context.Canceled})
	if err := c.HandleNote(context.Background(), checkInNote()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := repo.Get(context.Background(), "user"); !errors.Is(err, domain.ErrUserStateNotFound) {
		t.Fatal(err)
	}
}

func TestCheckInConcurrentNotes(t *testing.T) {
	repo := &memory.Users{}
	calls := 0
	c := newTestCheckIns(t, repo, checkInReactionFunc(func(context.Context, string, string) error { calls++; return nil }), &checkInLimiter{})
	var wg sync.WaitGroup
	for i := range 100 {
		wg.Go(func() {
			n := checkInNote()
			n.ID = fmt.Sprint(i)
			if err := c.HandleNote(context.Background(), n); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	s, _ := repo.Get(context.Background(), "user")
	if calls != 1 || s.CheckInDays != 1 {
		t.Fatalf("calls %d, state %+v", calls, s)
	}
}
