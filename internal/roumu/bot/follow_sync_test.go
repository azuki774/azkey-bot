package bot

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

func TestFollowSyncDefaultsAndFourRelationshipSnapshots(t *testing.T) {
	settings := DefaultFollowSyncSettings()
	if settings.MaxWritesPerSync != 10 || settings.WriteInterval != time.Minute {
		t.Fatalf("follow sync defaults = %+v", settings)
	}
	tests := []struct {
		name      string
		followers []string
		following []string
		want      []followCandidate
	}{
		{name: "follower only", followers: []string{"person"}, want: []followCandidate{{id: "person", action: actionFollow}}},
		{name: "following only", following: []string{"person"}, want: []followCandidate{{id: "person", action: actionUnfollow}}},
		{name: "mutual", followers: []string{"person"}, following: []string{"person"}},
		{name: "neither"},
		{name: "self is excluded", followers: []string{"bot"}, following: []string{"bot"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			syncer := newTestFollowSynchronizer(t, nil, nil, nil)
			syncer.UpdateSnapshot("bot", test.followers, test.following)
			if !reflect.DeepEqual(syncer.snapshot.candidates, test.want) {
				t.Fatalf("candidates = %+v, want %+v", syncer.snapshot.candidates, test.want)
			}
		})
	}
}

func TestFollowSynchronizerUsesConfirmedStateAndIsIdempotent(t *testing.T) {
	client := newFakeFollowClient()
	client.relations["follower"] = domain.Relation{ID: "follower", IsFollowed: true}
	client.afterFollow = func(id string) {
		client.setRelation(domain.Relation{ID: id, IsFollowing: true, IsFollowed: true})
	}
	syncer := newTestFollowSynchronizer(t, client, nil, nil)
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, ok := syncer.nextCandidate()
	if !ok {
		t.Fatal("follower candidate is unavailable")
	}
	first := syncer.reconcile(context.Background(), candidate, version)
	if !first.completed || !reflect.DeepEqual(client.followCalls, []string{"follower"}) {
		t.Fatalf("first reconcile = %+v, follows=%v", first, client.followCalls)
	}

	// A new successful snapshot may still contain a follower-only row while
	// the follow action is propagating. Fresh relation state prevents a repeat.
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, ok = syncer.nextCandidate()
	if !ok {
		t.Fatal("second snapshot did not reevaluate the follower")
	}
	second := syncer.reconcile(context.Background(), candidate, version)
	if !second.completed || len(client.followCalls) != 1 {
		t.Fatalf("second reconcile = %+v, follows=%v", second, client.followCalls)
	}
}

func TestUnfollowRequiresExclusiveFollowAndSkipsBlocks(t *testing.T) {
	client := newFakeFollowClient()
	client.relations["outbound"] = domain.Relation{ID: "outbound", IsFollowing: true}
	syncer := newTestFollowSynchronizer(t, client, nil, nil)
	syncer.UpdateSnapshot("bot", nil, []string{"outbound"})
	candidate, version, _, ok := syncer.nextCandidate()
	if !ok || candidate.action != actionUnfollow {
		t.Fatalf("unfollow candidate = %+v, available=%t", candidate, ok)
	}
	outcome := syncer.reconcile(context.Background(), candidate, version)
	if !outcome.completed || !reflect.DeepEqual(client.unfollowCalls, []string{"outbound"}) {
		t.Fatalf("unfollow outcome=%+v calls=%v", outcome, client.unfollowCalls)
	}

	for _, blocked := range []domain.Relation{
		{ID: "outbound", IsFollowing: true, IsBlocking: true},
		{ID: "outbound", IsFollowing: true, IsBlocked: true},
	} {
		client.setRelation(blocked)
		syncer.UpdateSnapshot("bot", nil, []string{"outbound"})
		candidate, version, _, _ = syncer.nextCandidate()
		outcome = syncer.reconcile(context.Background(), candidate, version)
		if !outcome.completed || len(client.unfollowCalls) != 1 {
			t.Fatalf("blocked unfollow outcome=%+v calls=%v", outcome, client.unfollowCalls)
		}
	}
}

func TestPendingAndBlockedFollowWaitForLaterRelationshipSnapshot(t *testing.T) {
	tests := []struct {
		name    string
		state   domain.Relation
		unblock func(domain.Relation) domain.Relation
	}{
		{
			name:  "pending request",
			state: domain.Relation{ID: "follower", IsFollowed: true, HasPendingFollowRequestFromYou: true},
			unblock: func(r domain.Relation) domain.Relation {
				r.HasPendingFollowRequestFromYou = false
				return r
			},
		},
		{
			name:  "target blocks bot",
			state: domain.Relation{ID: "follower", IsFollowed: true, IsBlocked: true},
			unblock: func(r domain.Relation) domain.Relation {
				r.IsBlocked = false
				return r
			},
		},
		{
			name:  "bot blocks target",
			state: domain.Relation{ID: "follower", IsFollowed: true, IsBlocking: true},
			unblock: func(r domain.Relation) domain.Relation {
				r.IsBlocking = false
				return r
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeFollowClient()
			client.relations["follower"] = test.state
			syncer := newTestFollowSynchronizer(t, client, nil, nil)
			syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
			candidate, version, _, ok := syncer.nextCandidate()
			if !ok {
				t.Fatal("follow candidate is unavailable")
			}
			outcome := syncer.reconcile(context.Background(), candidate, version)
			if !outcome.completed || len(client.followCalls) != 0 {
				t.Fatalf("blocked state outcome=%+v follows=%v", outcome, client.followCalls)
			}

			client.setRelation(test.unblock(test.state))
			syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
			candidate, version, _, ok = syncer.nextCandidate()
			if !ok {
				t.Fatal("next snapshot did not reevaluate the candidate")
			}
			outcome = syncer.reconcile(context.Background(), candidate, version)
			if !outcome.completed || !reflect.DeepEqual(client.followCalls, []string{"follower"}) {
				t.Fatalf("unblocked state outcome=%+v follows=%v", outcome, client.followCalls)
			}
		})
	}
}

func TestFinalRelationCheckRunsAfterLimiterWaits(t *testing.T) {
	client := newFakeFollowClient()
	client.relations["follower"] = domain.Relation{ID: "follower", IsFollowed: true}
	limiter := &fakeFollowLimiter{}
	limiter.onWait = func(call int) {
		if call == 2 {
			client.setRelation(domain.Relation{ID: "follower", IsFollowing: true, IsFollowed: true})
		}
	}
	syncer := newTestFollowSynchronizer(t, client, limiter, nil)
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, _ := syncer.nextCandidate()
	outcome := syncer.reconcile(context.Background(), candidate, version)
	if !outcome.completed || len(client.followCalls) != 0 {
		t.Fatalf("reconcile=%+v follows=%v", outcome, client.followCalls)
	}
	if len(client.relationCalls) != 2 || limiter.waitCount() != 2 {
		t.Fatalf("relation calls=%d limiter waits=%d, want two reads each preceded by a permit and no write", len(client.relationCalls), limiter.waitCount())
	}
}

func TestLimiterWaitsImmediatelyBeforeEachRelationshipRequest(t *testing.T) {
	var events []string
	client := newFakeFollowClient()
	client.relationHook = func(_ context.Context, id string, _ int) (domain.Relation, error) {
		events = append(events, "relation")
		return domain.Relation{ID: id, IsFollowed: true}, nil
	}
	client.followError = func(string) error {
		events = append(events, "follow")
		return nil
	}
	limiter := &fakeFollowLimiter{}
	limiter.onWait = func(int) { events = append(events, "wait") }
	syncer := newTestFollowSynchronizer(t, client, limiter, nil)
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, _ := syncer.nextCandidate()
	if outcome := syncer.reconcile(context.Background(), candidate, version); !outcome.completed {
		t.Fatalf("reconcile = %+v, want completed", outcome)
	}
	want := []string{"wait", "relation", "wait", "relation", "wait", "follow"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("request sequence = %v, want %v", events, want)
	}
}

func TestTimeoutRetryRechecksRelationAndDoesNotRepeatAppliedWrite(t *testing.T) {
	client := newFakeFollowClient()
	client.relations["follower"] = domain.Relation{ID: "follower", IsFollowed: true}
	client.followError = func(id string) error {
		client.setRelation(domain.Relation{ID: id, IsFollowing: true, IsFollowed: true})
		return domain.NewContextError(domain.ErrorKindNetworkTimeout, context.DeadlineExceeded)
	}
	syncer := newTestFollowSynchronizer(t, client, nil, nil)
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, _ := syncer.nextCandidate()
	first := syncer.reconcile(context.Background(), candidate, version)
	if first.retryAfter != syncer.settings.BackoffBase || len(client.followCalls) != 1 {
		t.Fatalf("timeout outcome=%+v follows=%v", first, client.followCalls)
	}
	syncer.mu.Lock()
	syncer.scheduleRetryLocked(candidate.id, first.retryAfter)
	retryAt := syncer.retries[candidate.id].at
	syncer.mu.Unlock()
	if delay := retryAt.Sub(syncer.settings.Clock()); delay != syncer.settings.BackoffBase {
		t.Fatalf("timeout retry delay=%s, want %s", delay, syncer.settings.BackoffBase)
	}
	syncer.settings.Clock = func() time.Time { return retryAt }
	candidate, version, _, ok := syncer.nextCandidate()
	if !ok {
		t.Fatal("candidate was not eligible after backoff")
	}
	second := syncer.reconcile(context.Background(), candidate, version)
	if !second.completed || len(client.followCalls) != 1 {
		t.Fatalf("retry outcome=%+v follows=%v", second, client.followCalls)
	}
}

func TestLongLimiterWaitDiscardsStaleUnfollowCheck(t *testing.T) {
	client := newFakeFollowClient()
	client.relations["person"] = domain.Relation{ID: "person", IsFollowing: true}
	clock := &followTestClock{now: time.Now()}
	limiter := &fakeFollowLimiter{}
	limiter.onWait = func(call int) {
		if call == 3 {
			_ = clock.Sleep(context.Background(), maxRelationAge)
			client.setRelation(domain.Relation{ID: "person", IsFollowing: true, IsFollowed: true})
		}
	}
	syncer := newTestFollowSynchronizer(t, client, limiter, clock)
	syncer.UpdateSnapshot("bot", nil, []string{"person"})
	candidate, version, _, _ := syncer.nextCandidate()
	first := syncer.reconcile(context.Background(), candidate, version)
	if first.retryAfter <= 0 || len(client.unfollowCalls) != 0 || syncer.writes != 0 {
		t.Fatalf("stale check outcome=%+v writes=%v budget=%d", first, client.unfollowCalls, syncer.writes)
	}
	second := syncer.reconcile(context.Background(), candidate, version)
	if !second.completed || len(client.unfollowCalls) != 0 {
		t.Fatalf("fresh check outcome=%+v writes=%v", second, client.unfollowCalls)
	}
}

func TestWriteBudgetAndIntervalPersistAcrossSnapshotRefresh(t *testing.T) {
	client := newFakeFollowClient()
	clock := &followTestClock{now: time.Now()}
	var writesAt []time.Time
	client.followError = func(string) error {
		writesAt = append(writesAt, clock.Now())
		return nil
	}
	syncer := newTestFollowSynchronizer(t, client, nil, clock)
	syncer.settings.MaxWritesPerSync = 2
	syncer.settings.WriteInterval = time.Minute
	users := []string{"a", "b", "c"}
	for _, id := range users {
		client.setRelation(domain.Relation{ID: id, IsFollowed: true})
	}
	syncer.UpdateSnapshot("bot", users, nil)
	for _, id := range users[:2] {
		outcome := syncer.reconcile(context.Background(), followCandidate{id: id, action: actionFollow}, syncer.version)
		if !outcome.completed {
			t.Fatalf("reconcile %s: %+v", id, outcome)
		}
	}
	if _, _, _, ok := syncer.nextCandidate(); ok {
		t.Fatal("candidate available after exhausting write budget")
	}
	if len(writesAt) != 2 || writesAt[1].Sub(writesAt[0]) != time.Minute {
		t.Fatalf("write timestamps=%v, want two requests one minute apart", writesAt)
	}
	syncer.InvalidateSnapshot()
	syncer.UpdateSnapshot("bot", users, nil)
	outcome := syncer.reconcile(context.Background(), followCandidate{id: "c", action: actionFollow}, syncer.version)
	if !outcome.completed || len(writesAt) != 3 || writesAt[2].Sub(writesAt[1]) != time.Minute {
		t.Fatalf("refresh reset write spacing: outcome=%+v timestamps=%v", outcome, writesAt)
	}
}

func TestWriteErrorsClassifyAlreadyAppliedAndPermissionFailures(t *testing.T) {
	for _, test := range []struct {
		name      string
		action    followAction
		apiErr    *domain.Error
		completed bool
		auth      bool
	}{
		{"already following", actionFollow, domain.NewError(domain.ErrorKindClient, 400, "ALREADY_FOLLOWING", nil), true, false},
		{"already unfollowed", actionUnfollow, domain.NewError(domain.ErrorKindClient, 400, "NOT_FOLLOWING", nil), true, false},
		{"blocked", actionFollow, domain.NewError(domain.ErrorKindClient, 400, "BLOCKED", nil), true, false},
		{"permission", actionUnfollow, domain.NewError(domain.ErrorKindAuth, 403, "PERMISSION_DENIED", nil), false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := newFakeFollowClient()
			client.relations["person"] = domain.Relation{ID: "person", IsFollowed: test.action == actionFollow, IsFollowing: test.action == actionUnfollow}
			client.followError = func(string) error { return test.apiErr }
			client.unfollowError = func(string) error { return test.apiErr }
			syncer := newTestFollowSynchronizer(t, client, nil, nil)
			syncer.UpdateSnapshot("bot", []string{"person"}, nil)
			outcome := syncer.reconcile(context.Background(), followCandidate{id: "person", action: test.action}, syncer.version)
			if outcome.completed != test.completed || outcome.authFailure != test.auth || outcome.retryAfter != 0 {
				t.Fatalf("outcome=%+v", outcome)
			}
		})
	}
}

func TestRateLimitSetsSharedCooldownAndDefersCandidate(t *testing.T) {
	client := newFakeFollowClient()
	client.relations["follower"] = domain.Relation{ID: "follower", IsFollowed: true}
	retryAfter := 7 * time.Second
	client.followError = func(string) error {
		return domain.NewError(domain.ErrorKindRateLimit, 429, "RATE_LIMIT", &retryAfter)
	}
	limiter := &fakeFollowLimiter{}
	syncer := newTestFollowSynchronizer(t, client, limiter, nil)
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, _ := syncer.nextCandidate()
	outcome := syncer.reconcile(context.Background(), candidate, version)
	if outcome.retryAfter != retryAfter {
		t.Fatalf("retry delay=%s, want %s", outcome.retryAfter, retryAfter)
	}
	if cooldowns := limiter.cooldownValues(); len(cooldowns) != 1 || !cooldowns[0].Equal(syncer.settings.Clock().Add(retryAfter)) {
		t.Fatalf("shared cooldowns=%v", cooldowns)
	}
	syncer.mu.Lock()
	syncer.scheduleRetryLocked(candidate.id, outcome.retryAfter)
	syncer.mu.Unlock()
	if _, _, delay, ok := syncer.nextCandidate(); ok || delay != retryAfter {
		t.Fatalf("candidate scheduled available=%t delay=%s, want deferred for %s", ok, delay, retryAfter)
	}
}

func TestRateLimitWithoutRetryAfterUsesWriteIntervalAndExponentialBackoff(t *testing.T) {
	client := newFakeFollowClient()
	client.relationError = func(string) error {
		return domain.NewError(domain.ErrorKindRateLimit, 429, "RATE_LIMIT", nil)
	}
	limiter := &fakeFollowLimiter{}
	clock := &followTestClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	syncer := newTestFollowSynchronizer(t, client, limiter, clock)
	syncer.settings.WriteInterval = 10 * time.Second
	syncer.settings.BackoffBase = 2 * time.Second
	syncer.UpdateSnapshot("bot", []string{"follower"}, nil)
	candidate, version, _, ok := syncer.nextCandidate()
	if !ok {
		t.Fatal("candidate unavailable")
	}
	first := syncer.reconcile(context.Background(), candidate, version)
	if first.retryAfter != 10*time.Second {
		t.Fatalf("first retry delay = %s, want at least the write interval of 10s", first.retryAfter)
	}
	if syncer.writes != 0 || len(client.followCalls) != 0 {
		t.Fatalf("relation failure consumed a write: budget=%d follows=%v", syncer.writes, client.followCalls)
	}
	if cooldowns := limiter.cooldownValues(); len(cooldowns) != 1 || !cooldowns[0].Equal(clock.Now().Add(10*time.Second)) {
		t.Fatalf("first shared cooldowns = %v", cooldowns)
	}

	syncer.mu.Lock()
	syncer.scheduleRetryLocked(candidate.id, first.retryAfter)
	retryAt := syncer.retries[candidate.id].at
	syncer.mu.Unlock()
	clock.mu.Lock()
	clock.now = retryAt
	clock.mu.Unlock()
	candidate, version, _, ok = syncer.nextCandidate()
	if !ok {
		t.Fatal("candidate unavailable after first backoff")
	}
	second := syncer.reconcile(context.Background(), candidate, version)
	if second.retryAfter != 20*time.Second {
		t.Fatalf("second retry delay = %s, want exponential 20s", second.retryAfter)
	}
	if cooldowns := limiter.cooldownValues(); len(cooldowns) != 2 || !cooldowns[1].Equal(retryAt.Add(20*time.Second)) {
		t.Fatalf("second shared cooldowns = %v", cooldowns)
	}
}

func TestRetryableUserDoesNotStarveLaterCandidate(t *testing.T) {
	client := newFakeFollowClient()
	syncer := newTestFollowSynchronizer(t, client, nil, nil)
	syncer.UpdateSnapshot("bot", []string{"a", "b", "c"}, nil)
	syncer.mu.Lock()
	syncer.retries["a"] = followRetry{delay: time.Minute, at: syncer.settings.Clock().Add(time.Minute)}
	syncer.mu.Unlock()
	candidate, _, _, ok := syncer.nextCandidate()
	if !ok || candidate.id != "b" {
		t.Fatalf("next candidate=%+v available=%t, want b while a backs off", candidate, ok)
	}
}

func TestSnapshotUpdateKeepsCursorAndPrunesObsoleteRetries(t *testing.T) {
	syncer := newTestFollowSynchronizer(t, nil, nil, nil)
	users := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	syncer.UpdateSnapshot("bot", users, nil)
	syncer.mu.Lock()
	syncer.cursor = "j"
	for _, id := range users[:10] {
		syncer.retries[id] = followRetry{delay: time.Minute, at: syncer.settings.Clock().Add(time.Minute)}
	}
	syncer.retries["removed"] = followRetry{delay: time.Minute, at: syncer.settings.Clock().Add(time.Minute)}
	syncer.mu.Unlock()

	syncer.UpdateSnapshot("bot", users, nil)
	if syncer.cursor != "j" {
		t.Fatalf("cursor after snapshot update = %q, want j", syncer.cursor)
	}
	if len(syncer.retries) != 10 {
		t.Fatalf("retry entries after snapshot update = %d, want 10", len(syncer.retries))
	}
	if _, exists := syncer.retries["removed"]; exists {
		t.Fatal("retry entry for a removed candidate was retained")
	}
	candidate, _, _, ok := syncer.nextCandidate()
	if !ok || candidate.id != "k" {
		t.Fatalf("next candidate = %+v available=%t, want k after the retained cursor", candidate, ok)
	}
}

func TestInvalidateSnapshotSuspendsWorkWithoutResettingProgress(t *testing.T) {
	syncer := newTestFollowSynchronizer(t, nil, nil, nil)
	syncer.UpdateSnapshot("bot", []string{"a", "b"}, nil)
	syncer.mu.Lock()
	syncer.cursor = "a"
	retryAt := syncer.settings.Clock().Add(time.Minute)
	syncer.retries["b"] = followRetry{delay: time.Minute, at: retryAt}
	syncer.mu.Unlock()

	syncer.InvalidateSnapshot()
	if _, _, _, ok := syncer.nextCandidate(); ok {
		t.Fatal("candidate remained available while snapshot was invalid")
	}
	if syncer.cursor != "a" || syncer.retries["b"].at != retryAt || len(syncer.snapshot.candidates) != 2 {
		t.Fatalf("invalidation discarded progress or candidates: cursor=%q retries=%v snapshot=%+v", syncer.cursor, syncer.retries, syncer.snapshot)
	}

	syncer.UpdateSnapshot("bot", []string{"a", "b"}, nil)
	if _, _, _, ok := syncer.nextCandidate(); !ok {
		t.Fatal("complete replacement snapshot did not resume relationship work")
	}
	if got := syncer.retries["b"].at; !got.Equal(retryAt) {
		t.Fatalf("replacement snapshot reset retained retry time: got %s want %s", got, retryAt)
	}
}

func TestOneUserFailureDoesNotStopRunAndSnapshotReplacesStaleCandidates(t *testing.T) {
	client := newFakeFollowClient()
	client.relationError = func(id string) error {
		if id == "a" {
			return domain.NewError(domain.ErrorKindClient, 400, "NO_SUCH_USER", nil)
		}
		return nil
	}
	client.relations["b"] = domain.Relation{ID: "b", IsFollowed: true}
	syncer := newTestFollowSynchronizer(t, client, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	client.afterFollow = func(id string) {
		if id == "b" {
			cancel()
		}
	}
	done := make(chan error, 1)
	go func() { done <- syncer.Run(ctx) }()
	syncer.UpdateSnapshot("bot", []string{"a", "b"}, nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Run did not advance from failed user a to b")
	}
	if !reflect.DeepEqual(client.followCalls, []string{"b"}) {
		t.Fatalf("follow calls=%v, want only b", client.followCalls)
	}
}

func TestRunStopsPromptlyWhenCanceledWithoutPendingWork(t *testing.T) {
	syncer := newTestFollowSynchronizer(t, newFakeFollowClient(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- syncer.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestUpdateSnapshotCancelsStaleInFlightCandidate(t *testing.T) {
	client := newFakeFollowClient()
	client.relationHook = func(ctx context.Context, id string, call int) (domain.Relation, error) {
		if id == "old" && call == 1 {
			close(client.entered)
			<-ctx.Done()
			return domain.Relation{}, ctx.Err()
		}
		return domain.Relation{ID: id, IsFollowed: true}, nil
	}
	syncer := newTestFollowSynchronizer(t, client, nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	client.afterFollow = func(id string) {
		if id == "new" {
			cancel()
		}
	}
	done := make(chan error, 1)
	go func() { done <- syncer.Run(ctx) }()
	syncer.UpdateSnapshot("bot", []string{"old"}, nil)
	select {
	case <-client.entered:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("old candidate did not start")
	}
	syncer.UpdateSnapshot("bot", []string{"new"}, nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("Run did not replace stale work and stop")
	}
	if !reflect.DeepEqual(client.followCalls, []string{"new"}) {
		t.Fatalf("follow calls=%v, want only new candidate", client.followCalls)
	}
}

func newTestFollowSynchronizer(t *testing.T, client *fakeFollowClient, limiter *fakeFollowLimiter, clock *followTestClock) *FollowerSynchronizer {
	t.Helper()
	if client == nil {
		client = newFakeFollowClient()
	}
	if limiter == nil {
		limiter = &fakeFollowLimiter{}
	}
	if clock == nil {
		clock = &followTestClock{now: time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)}
	}
	settings := DefaultFollowSyncSettings()
	settings.MaxWritesPerSync = 10
	settings.WriteInterval = time.Second
	settings.BackoffBase = time.Second
	settings.BackoffMax = time.Minute
	settings.Clock = clock.Now
	settings.Sleep = clock.Sleep
	syncer, err := NewFollowerSynchronizer(client, limiter, settings, nil)
	if err != nil {
		t.Fatalf("NewFollowerSynchronizer: %v", err)
	}
	return syncer
}

type fakeFollowClient struct {
	mu            sync.Mutex
	relations     map[string]domain.Relation
	relationCalls []string
	followCalls   []string
	unfollowCalls []string
	relationError func(string) error
	relationHook  func(context.Context, string, int) (domain.Relation, error)
	followError   func(string) error
	unfollowError func(string) error
	afterFollow   func(string)
	entered       chan struct{}
}

func newFakeFollowClient() *fakeFollowClient {
	return &fakeFollowClient{relations: make(map[string]domain.Relation), entered: make(chan struct{})}
}

func (c *fakeFollowClient) GetRelations(ctx context.Context, ids []string) ([]domain.Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(ids) != 1 {
		return nil, errors.New("test expects one relation ID")
	}
	id := ids[0]
	c.mu.Lock()
	c.relationCalls = append(c.relationCalls, id)
	call := len(c.relationCalls)
	hook := c.relationHook
	failure := c.relationError
	relation := c.relations[id]
	c.mu.Unlock()
	if hook != nil {
		returnOne, err := hook(ctx, id, call)
		if err != nil {
			return nil, err
		}
		return []domain.Relation{returnOne}, nil
	}
	if failure != nil {
		if err := failure(id); err != nil {
			return nil, err
		}
	}
	if relation.ID == "" {
		relation.ID = id
	}
	return []domain.Relation{relation}, nil
}

func (c *fakeFollowClient) CreateFollow(ctx context.Context, id string) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	c.mu.Lock()
	c.followCalls = append(c.followCalls, id)
	failure := c.followError
	after := c.afterFollow
	c.mu.Unlock()
	if after != nil {
		after(id)
	}
	if failure != nil {
		if err := failure(id); err != nil {
			return domain.User{}, err
		}
	}
	return domain.User{ID: id}, nil
}

func (c *fakeFollowClient) DeleteFollow(ctx context.Context, id string) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	c.mu.Lock()
	c.unfollowCalls = append(c.unfollowCalls, id)
	failure := c.unfollowError
	c.mu.Unlock()
	if failure != nil {
		if err := failure(id); err != nil {
			return domain.User{}, err
		}
	}
	return domain.User{ID: id}, nil
}

func (c *fakeFollowClient) setRelation(relation domain.Relation) {
	c.mu.Lock()
	c.relations[relation.ID] = relation
	c.mu.Unlock()
}

type fakeFollowLimiter struct {
	mu        sync.Mutex
	waits     int
	cooldowns []time.Time
	onWait    func(int)
}

func (l *fakeFollowLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	l.waits++
	call := l.waits
	hook := l.onWait
	l.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return ctx.Err()
}

func (l *fakeFollowLimiter) SetCooldown(until time.Time) {
	l.mu.Lock()
	l.cooldowns = append(l.cooldowns, until)
	l.mu.Unlock()
}

func (l *fakeFollowLimiter) waitCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.waits
}

func (l *fakeFollowLimiter) cooldownValues() []time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]time.Time(nil), l.cooldowns...)
}

type followTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *followTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *followTestClock) Sleep(ctx context.Context, delay time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.now = c.now.Add(delay)
	c.mu.Unlock()
	return ctx.Err()
}
