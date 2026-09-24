package bot

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

var errFollowSyncClientRequired = errors.New("follow sync client is required")

// A shared limiter may be held by other requests or a server cooldown. Do not
// mutate a relationship based on a check made before a prolonged wait.
const maxRelationAge = 5 * time.Second

// FollowClient is the Misskey API surface used to reconcile follower state.
type FollowClient interface {
	GetRelations(context.Context, []string) ([]domain.Relation, error)
	CreateFollow(context.Context, string) (domain.User, error)
	DeleteFollow(context.Context, string) (domain.User, error)
}

// FollowSyncSettings bounds relationship writes and their retry schedule.
type FollowSyncSettings struct {
	MaxWritesPerSync int
	WriteInterval    time.Duration
	BackoffBase      time.Duration
	BackoffMax       time.Duration
	Clock            func() time.Time
	Sleep            func(context.Context, time.Duration) error
}

// DefaultFollowSyncSettings uses the conservative upstream write interval and
// limits each complete relationship refresh to ten write requests.
func DefaultFollowSyncSettings() FollowSyncSettings {
	return FollowSyncSettings{
		MaxWritesPerSync: 10,
		WriteInterval:    time.Minute,
		BackoffBase:      time.Second,
		BackoffMax:       5 * time.Minute,
		Clock:            time.Now,
		Sleep:            sleepFollowContext,
	}
}

// FollowerSynchronizer receives complete relationship snapshots from polling
// and reconciles them on its own cancellable loop, separate from note workers.
type FollowerSynchronizer struct {
	client   FollowClient
	limiter  domain.RequestLimiter
	settings FollowSyncSettings
	logger   *slog.Logger

	mu             sync.Mutex
	snapshot       followSnapshot
	snapshotValid  bool
	version        uint64
	writes         int
	nextWriteAt    time.Time
	completed      map[string]struct{}
	blockedVersion uint64
	cursor         string
	retries        map[string]followRetry
	activeCancel   context.CancelFunc
	wake           chan struct{}
}

type followAction uint8

const (
	actionFollow followAction = iota + 1
	actionUnfollow
)

type followCandidate struct {
	id     string
	action followAction
}

type followSnapshot struct {
	selfID     string
	followers  map[string]struct{}
	following  map[string]struct{}
	candidates []followCandidate
}

type followRetry struct {
	delay time.Duration
	at    time.Time
}

// NewFollowerSynchronizer creates the stateful relationship reconciler.
func NewFollowerSynchronizer(client FollowClient, limiter domain.RequestLimiter, settings FollowSyncSettings, logger *slog.Logger) (*FollowerSynchronizer, error) {
	if nilFollowClient(client) {
		return nil, errFollowSyncClientRequired
	}
	if nilFollowLimiter(limiter) {
		return nil, errors.New("follow sync request limiter is required")
	}
	defaults := DefaultFollowSyncSettings()
	if settings.MaxWritesPerSync == 0 {
		settings.MaxWritesPerSync = defaults.MaxWritesPerSync
	}
	if settings.WriteInterval == 0 {
		settings.WriteInterval = defaults.WriteInterval
	}
	if settings.BackoffBase == 0 {
		settings.BackoffBase = defaults.BackoffBase
	}
	if settings.BackoffMax == 0 {
		settings.BackoffMax = defaults.BackoffMax
	}
	if settings.Clock == nil {
		settings.Clock = defaults.Clock
	}
	if settings.Sleep == nil {
		settings.Sleep = defaults.Sleep
	}
	if settings.MaxWritesPerSync < 1 || settings.MaxWritesPerSync > 10 || settings.WriteInterval < 0 || settings.BackoffBase <= 0 || settings.BackoffMax < settings.BackoffBase {
		return nil, errors.New("follow sync settings are invalid")
	}
	return &FollowerSynchronizer{
		client:    client,
		limiter:   limiter,
		settings:  settings,
		logger:    logger,
		completed: make(map[string]struct{}),
		retries:   make(map[string]followRetry),
		wake:      make(chan struct{}, 1),
	}, nil
}

// UpdateSnapshot replaces pending work only after polling has fetched both
// relationship lists completely. New data cancels any in-progress stale work.
func (s *FollowerSynchronizer) UpdateSnapshot(selfID string, followerIDs, followingIDs []string) {
	if s == nil {
		return
	}
	followers := validIDSet(followerIDs, selfID)
	following := validIDSet(followingIDs, selfID)
	snapshot := followSnapshot{selfID: selfID, followers: followers, following: following}
	for id := range followers {
		if _, alreadyFollows := following[id]; !alreadyFollows {
			snapshot.candidates = append(snapshot.candidates, followCandidate{id: id, action: actionFollow})
		}
	}
	for id := range following {
		if _, followsBot := followers[id]; !followsBot {
			snapshot.candidates = append(snapshot.candidates, followCandidate{id: id, action: actionUnfollow})
		}
	}
	sort.Slice(snapshot.candidates, func(i, j int) bool { return snapshot.candidates[i].id < snapshot.candidates[j].id })

	s.mu.Lock()
	s.version++
	s.snapshot = snapshot
	s.snapshotValid = true
	s.writes = 0
	s.completed = make(map[string]struct{})
	s.blockedVersion = 0
	currentCandidates := make(map[string]struct{}, len(snapshot.candidates))
	for _, candidate := range snapshot.candidates {
		currentCandidates[candidate.id] = struct{}{}
	}
	for id := range s.retries {
		if _, present := currentCandidates[id]; !present {
			delete(s.retries, id)
		}
	}
	if s.activeCancel != nil {
		s.activeCancel()
		s.activeCancel = nil
	}
	s.mu.Unlock()
	s.notify()
}

// InvalidateSnapshot stops relationship work until a complete replacement
// snapshot is available. The last candidate set, retry state, and cursor are
// retained so an unsuccessful polling refresh does not lose progress.
func (s *FollowerSynchronizer) InvalidateSnapshot() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.version++
	s.snapshotValid = false
	if s.activeCancel != nil {
		s.activeCancel()
		s.activeCancel = nil
	}
	s.mu.Unlock()
	s.notify()
}

// Run performs scheduled relationship work until ctx is canceled. It never
// sleeps on a polling worker and returns only when its lifecycle ends.
func (s *FollowerSynchronizer) Run(ctx context.Context) error {
	if s == nil || nilFollowClient(s.client) || nilFollowLimiter(s.limiter) {
		return errFollowSyncClientRequired
	}
	if ctx == nil {
		return errors.New("follow sync context is required")
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		candidate, version, delay, available := s.nextCandidate()
		if !available {
			if err := s.waitForWork(ctx, delay); err != nil {
				return nil
			}
			continue
		}

		workCtx, cancel := context.WithCancel(ctx)
		s.mu.Lock()
		if version != s.version {
			s.mu.Unlock()
			cancel()
			continue
		}
		s.activeCancel = cancel
		s.cursor = candidate.id
		s.mu.Unlock()

		outcome := s.reconcile(workCtx, candidate, version)
		cancel()
		s.mu.Lock()
		if s.activeCancel != nil {
			s.activeCancel = nil
		}
		if version == s.version && outcome.completed {
			s.completed[candidate.id] = struct{}{}
			delete(s.retries, candidate.id)
		}
		if version == s.version && outcome.authFailure {
			s.blockedVersion = version
		}
		if version == s.version && outcome.retryAfter > 0 {
			s.scheduleRetryLocked(candidate.id, outcome.retryAfter)
		}
		s.mu.Unlock()
		if outcome.err != nil && !errors.Is(outcome.err, context.Canceled) {
			s.logFailure(candidate.id, outcome.err)
		}
	}
}

type followOutcome struct {
	completed   bool
	authFailure bool
	retryAfter  time.Duration
	err         error
}

func (s *FollowerSynchronizer) reconcile(ctx context.Context, candidate followCandidate, version uint64) followOutcome {
	// The first lookup cheaply skips stale candidates. For a real mutation, the
	// write interval is allowed to elapse before a fresh lookup is made.
	relation, err := s.getRelation(ctx, candidate.id)
	if err != nil {
		return s.classifyFailure(candidate.id, version, err)
	}
	if !s.shouldAct(candidate.action, relation) {
		return followOutcome{completed: true}
	}

	if err := s.waitForWriteSlot(ctx); err != nil {
		return followOutcome{err: err}
	}
	if err := s.limiter.Wait(ctx); err != nil {
		return followOutcome{err: err}
	}
	if !s.currentVersion(version) {
		return followOutcome{err: context.Canceled}
	}
	checkedAt := s.settings.Clock()
	relation, err = s.readRelation(ctx, candidate.id)
	if err != nil {
		return s.classifyFailure(candidate.id, version, err)
	}
	if !s.shouldAct(candidate.action, relation) {
		return followOutcome{completed: true}
	}
	// Acquire a limiter permit for the mutation itself instead of reserving
	// permits ahead of the final read. This keeps burst-one limiters strict for
	// actual requests while the fresh relation check still follows the minimum
	// write-interval wait.
	remaining := maxRelationAge - s.settings.Clock().Sub(checkedAt)
	if remaining <= 0 {
		return followOutcome{retryAfter: s.settings.BackoffBase}
	}
	permitCtx, cancelPermit := context.WithTimeout(ctx, remaining)
	err = s.limiter.Wait(permitCtx)
	cancelPermit()
	if ctx.Err() != nil {
		return followOutcome{err: ctx.Err()}
	}
	if err != nil {
		return followOutcome{retryAfter: s.settings.BackoffBase, err: err}
	}
	if s.settings.Clock().Sub(checkedAt) >= maxRelationAge {
		return followOutcome{retryAfter: s.settings.BackoffBase}
	}
	if !s.currentVersion(version) {
		return followOutcome{err: context.Canceled}
	}
	if !s.beginWrite(version) {
		return followOutcome{completed: true}
	}

	var writeErr error
	switch candidate.action {
	case actionFollow:
		_, writeErr = s.client.CreateFollow(ctx, candidate.id)
	case actionUnfollow:
		_, writeErr = s.client.DeleteFollow(ctx, candidate.id)
	default:
		return followOutcome{completed: true}
	}
	if writeErr != nil {
		if apiErr, ok := asFollowDomainError(writeErr); ok {
			switch apiErr.Code {
			case "ALREADY_FOLLOWING":
				if candidate.action == actionFollow {
					return followOutcome{completed: true}
				}
			case "NOT_FOLLOWING":
				if candidate.action == actionUnfollow {
					return followOutcome{completed: true}
				}
			}
		}
		return s.classifyFailure(candidate.id, version, writeErr)
	}
	return followOutcome{completed: true}
}

func (s *FollowerSynchronizer) getRelation(ctx context.Context, id string) (domain.Relation, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return domain.Relation{}, err
	}
	return s.readRelation(ctx, id)
}

func (s *FollowerSynchronizer) readRelation(ctx context.Context, id string) (domain.Relation, error) {
	relations, err := s.client.GetRelations(ctx, []string{id})
	if err != nil {
		return domain.Relation{}, err
	}
	if len(relations) != 1 || relations[0].ID != id {
		return domain.Relation{}, domain.NewError(domain.ErrorKindInvalidResponse, 0, "INVALID_RESPONSE", nil)
	}
	return relations[0], nil
}

func (s *FollowerSynchronizer) shouldAct(action followAction, relation domain.Relation) bool {
	switch action {
	case actionFollow:
		return relation.IsFollowed && !relation.IsFollowing && !relation.HasPendingFollowRequestFromYou && !relation.IsBlocking && !relation.IsBlocked
	case actionUnfollow:
		return relation.IsFollowing && !relation.IsFollowed && !relation.IsBlocking && !relation.IsBlocked
	default:
		return false
	}
}

func (s *FollowerSynchronizer) classifyFailure(id string, version uint64, err error) followOutcome {
	if err == nil {
		return followOutcome{}
	}
	if errors.Is(err, context.Canceled) {
		return followOutcome{err: err}
	}
	apiErr, ok := asFollowDomainError(err)
	if !ok {
		if errors.Is(err, context.DeadlineExceeded) {
			return followOutcome{retryAfter: s.nextRetryDelay(id), err: err}
		}
		return followOutcome{completed: true, err: err}
	}
	switch apiErr.Kind {
	case domain.ErrorKindAuth:
		return followOutcome{authFailure: true, err: err}
	case domain.ErrorKindServer, domain.ErrorKindNetwork, domain.ErrorKindNetworkTimeout:
		delay := s.nextRetryDelay(id)
		return followOutcome{retryAfter: delay, err: err}
	case domain.ErrorKindRateLimit:
		delay := s.rateLimitRetryDelay(id, apiErr.RetryAfter)
		s.applyCooldown(delay)
		return followOutcome{retryAfter: delay, err: err}
	default:
		// A user-specific or malformed response must not hold up other users.
		return followOutcome{completed: true, err: err}
	}
}

func (s *FollowerSynchronizer) rateLimitRetryDelay(id string, retryAfter *time.Duration) time.Duration {
	delay := s.nextRetryDelay(id)
	if delay < s.settings.WriteInterval {
		delay = s.settings.WriteInterval
	}
	if retryAfter != nil && *retryAfter > delay {
		delay = *retryAfter
	}
	return delay
}

func (s *FollowerSynchronizer) applyCooldown(delay time.Duration) {
	if delay > 0 {
		s.limiter.SetCooldown(s.settings.Clock().Add(delay))
	}
}

func (s *FollowerSynchronizer) nextRetryDelay(id string) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.retries[id].delay
	if previous <= 0 {
		return s.settings.BackoffBase
	}
	if previous >= s.settings.BackoffMax/2 {
		return s.settings.BackoffMax
	}
	return minFollowDuration(previous*2, s.settings.BackoffMax)
}

func (s *FollowerSynchronizer) scheduleRetryLocked(id string, delay time.Duration) {
	previous := s.retries[id].delay
	if delay <= 0 {
		delay = s.settings.BackoffBase
	}
	if previous > 0 && delay < previous {
		delay = previous
	}
	s.retries[id] = followRetry{delay: delay, at: s.settings.Clock().Add(delay)}
}

func (s *FollowerSynchronizer) beginWrite(version uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if version != s.version || s.writes >= s.settings.MaxWritesPerSync {
		return false
	}
	s.writes++
	s.nextWriteAt = s.settings.Clock().Add(s.settings.WriteInterval)
	return true
}

func (s *FollowerSynchronizer) waitForWriteSlot(ctx context.Context) error {
	s.mu.Lock()
	until := s.nextWriteAt
	s.mu.Unlock()
	delay := until.Sub(s.settings.Clock())
	if delay <= 0 {
		return ctx.Err()
	}
	return s.settings.Sleep(ctx, delay)
}

func (s *FollowerSynchronizer) nextCandidate() (followCandidate, uint64, time.Duration, bool) {
	now := s.settings.Clock()
	s.mu.Lock()
	defer s.mu.Unlock()
	version := s.version
	if version == 0 || !s.snapshotValid || s.blockedVersion == version || s.writes >= s.settings.MaxWritesPerSync {
		return followCandidate{}, version, 0, false
	}
	candidates := s.snapshot.candidates
	if len(candidates) == 0 {
		return followCandidate{}, version, 0, false
	}
	start := sort.Search(len(candidates), func(i int) bool { return candidates[i].id > s.cursor })
	if start == len(candidates) {
		start = 0
	}
	var earliest time.Time
	for offset := 0; offset < len(candidates); offset++ {
		candidate := candidates[(start+offset)%len(candidates)]
		if _, done := s.completed[candidate.id]; done {
			continue
		}
		due := s.retries[candidate.id].at
		if !due.After(now) {
			return candidate, version, 0, true
		}
		if earliest.IsZero() || due.Before(earliest) {
			earliest = due
		}
	}
	if !earliest.IsZero() {
		return followCandidate{}, version, earliest.Sub(now), false
	}
	return followCandidate{}, version, 0, false
}

func (s *FollowerSynchronizer) currentVersion(version uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return version == s.version
}

func (s *FollowerSynchronizer) waitForWork(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		select {
		case <-s.wake:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	waitCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- s.settings.Sleep(waitCtx, delay) }()
	select {
	case err := <-done:
		cancel()
		return err
	case <-s.wake:
		cancel()
		return nil
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}

func (s *FollowerSynchronizer) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *FollowerSynchronizer) logFailure(id string, err error) {
	if s.logger == nil {
		return
	}
	if apiErr, ok := asFollowDomainError(err); ok {
		s.logger.Warn("follower relationship sync failed", "user_id", id, "kind", apiErr.Kind, "status", apiErr.StatusCode)
		return
	}
	s.logger.Warn("follower relationship sync failed", "user_id", id)
}

func validIDSet(ids []string, selfID string) map[string]struct{} {
	result := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id != "" && id == strings.TrimSpace(id) && id != selfID {
			result[id] = struct{}{}
		}
	}
	return result
}

func nilFollowClient(client FollowClient) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	return (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface || value.Kind() == reflect.Map || value.Kind() == reflect.Func || value.Kind() == reflect.Slice) && value.IsNil()
}

func nilFollowLimiter(limiter domain.RequestLimiter) bool {
	if limiter == nil {
		return true
	}
	value := reflect.ValueOf(limiter)
	return (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface || value.Kind() == reflect.Map || value.Kind() == reflect.Func || value.Kind() == reflect.Slice) && value.IsNil()
}

func asFollowDomainError(err error) (*domain.Error, bool) {
	var apiErr *domain.Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

func minFollowDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

func sleepFollowContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
