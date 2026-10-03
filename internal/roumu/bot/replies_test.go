package bot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/repository/memory"
)

type replyFake struct {
	notes            []domain.Note
	texts            []string
	sent             []string
	options          []domain.NotePageOptions
	member           bool
	readErr, sendErr error
	relations        int
}

func (f *replyFake) Self(context.Context) (domain.User, error) { return domain.User{ID: "bot"}, nil }
func (f *replyFake) ListMentions(_ context.Context, o domain.NotePageOptions) ([]domain.Note, error) {
	f.options = append(f.options, o)
	var result []domain.Note
	for _, n := range f.notes {
		if n.ID > o.SinceID {
			result = append(result, n)
		}
	}
	if len(result) > o.Limit {
		result = result[:o.Limit]
	}
	return result, nil
}
func (f *replyFake) IsFollowing(context.Context, string) (bool, error) {
	f.relations++
	return f.member, f.readErr
}
func (f *replyFake) CreateReply(_ context.Context, n domain.Note, text string) error {
	f.sent = append(f.sent, n.ID)
	f.texts = append(f.texts, text)
	return f.sendErr
}

func inquiry(id string) domain.Note {
	return domain.Note{ID: id, UserID: "user", User: &domain.User{ID: "user"}, Visibility: "public", ReplyID: "parent", ReplyUserID: "bot"}
}
func replyFixture(t *testing.T, f *replyFake, repo UserStateRepository) *Replies {
	t.Helper()
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	s := DefaultReplySettings()
	s.Clock = func() time.Time { return now }
	s.Sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return ctx.Err() }
	s.PageLimit = 1
	r, err := NewReplies(f, repo, s, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.selfID = "bot"
	r.since = now
	return r
}

func TestRepliesMembershipAndReadOnlyState(t *testing.T) {
	for _, member := range []bool{false, true} {
		f := &replyFake{member: member}
		repo := &memory.Users{}
		r := replyFixture(t, f, repo)
		state := domain.UserState{CheckInDays: 12, ConsecutiveDays: 3, LastCheckInAt: r.settings.Clock()}
		if err := repo.Put(context.Background(), "user", state); err != nil {
			t.Fatal(err)
		}
		if err := r.handle(context.Background(), inquiry("01")); err != nil {
			t.Fatal(err)
		}
		want := "あなたはメンバーではありません"
		if member {
			want = "連続チェックイン回数: 3連勤、チェックイン回数: 12 日"
		}
		if len(f.texts) != 1 || f.texts[0] != want {
			t.Fatalf("reply: %v", f.texts)
		}
		got, _ := repo.Get(context.Background(), "user")
		if got != state {
			t.Fatal("inquiry changed state")
		}
	}
}

func TestRepliesMissingStateAndEligibility(t *testing.T) {
	f := &replyFake{member: true}
	r := replyFixture(t, f, &memory.Users{})
	for _, mutate := range []func(*domain.Note){
		func(n *domain.Note) { n.Visibility = "specified" },
		func(n *domain.Note) { n.Visibility = "home" },
		func(n *domain.Note) { n.Visibility = "followers" },
		func(n *domain.Note) { n.UserID = "bot" },
		func(n *domain.Note) { n.User.IsBot = true },
		func(n *domain.Note) { n.User = nil },
		func(n *domain.Note) { n.ChannelID = "channel" },
	} {
		n := inquiry("01")
		mutate(&n)
		if err := r.handle(context.Background(), n); err != nil {
			t.Fatal(err)
		}
	}
	if f.relations != 0 || len(f.sent) != 0 {
		t.Fatal("ineligible notes caused API calls")
	}
	if err := r.handle(context.Background(), inquiry("02")); err != nil {
		t.Fatal(err)
	}
	if f.texts[0] != "連続チェックイン回数: 0連勤、チェックイン回数: 0 日" {
		t.Fatal(f.texts)
	}
}

func TestRepliesAnswerMentionsRegardlessOfReplyTarget(t *testing.T) {
	for _, tc := range []struct {
		name, replyID, replyUserID string
	}{
		{name: "new post"},
		{name: "reply to bot", replyID: "parent", replyUserID: "bot"},
		{name: "reply to another user", replyID: "parent", replyUserID: "someone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			note := inquiry("01")
			note.ReplyID, note.ReplyUserID = tc.replyID, tc.replyUserID
			f := &replyFake{notes: []domain.Note{note}}
			r := replyFixture(t, f, &memory.Users{})
			if err := r.poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(f.sent) != 1 || f.sent[0] != note.ID || f.texts[0] != "あなたはメンバーではありません" {
				t.Fatalf("sent=%v texts=%v", f.sent, f.texts)
			}
			if err := r.poll(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(f.sent) != 1 {
				t.Fatal("mention answered more than once")
			}
		})
	}
}

func TestRepliesPaginationReadRetryAndAmbiguousWrite(t *testing.T) {
	f := &replyFake{notes: []domain.Note{inquiry("01"), inquiry("02"), inquiry("03")}, readErr: errors.New("read failed")}
	r := replyFixture(t, f, &memory.Users{})
	if err := r.poll(context.Background()); err == nil || r.cursor != "" {
		t.Fatal("read failure advanced cursor")
	}
	f.readErr = nil
	f.sendErr = domain.NewError(domain.ErrorKindNetworkTimeout, 0, "", nil)
	start := r.settings.Clock()
	if err := r.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r.cursor != "03" || len(f.sent) != 3 {
		t.Fatalf("cursor=%s sent=%v", r.cursor, f.sent)
	}
	if r.settings.Clock().Sub(start) < 30*time.Second {
		t.Fatal("writes were not rate limited")
	}
	if err := r.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.sent) != 3 {
		t.Fatal("ambiguous sends retried")
	}
	if f.options[0].SinceDate == nil {
		t.Fatal("startup lacks time baseline")
	}
}

type failingRepository struct{}

func (failingRepository) Get(context.Context, string) (domain.UserState, error) {
	return domain.UserState{}, errors.New("storage failed")
}
func (failingRepository) Put(context.Context, string, domain.UserState) error { return nil }

func TestRepliesStorageFailureAndCooldown(t *testing.T) {
	f := &replyFake{member: true, notes: []domain.Note{inquiry("01")}}
	r := replyFixture(t, f, failingRepository{})
	if err := r.poll(context.Background()); err == nil || len(f.sent) != 0 || r.cursor != "" {
		t.Fatal("storage failure not retained for retry")
	}
	delay := 10 * time.Minute
	f.readErr = domain.NewError(domain.ErrorKindRateLimit, 429, "", &delay)
	if err := r.poll(context.Background()); err == nil {
		t.Fatal("rate limit ignored")
	}
	if r.nextRequest.Sub(r.settings.Clock()) < delay {
		t.Fatal("Retry-After ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.waitRequest(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

type replyLimiter struct {
	waits    int
	cooldown time.Time
	err      error
}

func (l *replyLimiter) Wait(ctx context.Context) error {
	l.waits++
	if err := ctx.Err(); err != nil {
		return err
	}
	return l.err
}

func (l *replyLimiter) SetCooldown(until time.Time) { l.cooldown = until }

func TestRepliesUseSharedLimiterForReadsWritesAndCooldown(t *testing.T) {
	f := &replyFake{member: true, notes: []domain.Note{inquiry("01")}}
	r := replyFixture(t, f, &memory.Users{})
	limiter := &replyLimiter{}
	r.settings.RateLimiter = limiter
	delay := 10 * time.Minute
	f.sendErr = domain.NewError(domain.ErrorKindRateLimit, 429, "", &delay)
	if err := r.poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Mentions page, relation read, reply write, and the final empty page.
	if limiter.waits != 4 {
		t.Fatalf("limiter waits = %d", limiter.waits)
	}
	if limiter.cooldown.Sub(r.settings.Clock()) != delay {
		t.Fatal("reply cooldown was not shared")
	}
	blocked := errors.New("limiter unavailable")
	limiter.err = blocked
	if err := r.poll(context.Background()); !errors.Is(err, blocked) {
		t.Fatalf("limiter failure: %v", err)
	}
	if len(f.sent) != 1 {
		t.Fatal("blocked request sent a reply")
	}
}
