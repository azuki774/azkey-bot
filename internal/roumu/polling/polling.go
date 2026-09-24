// Package polling owns the cancellable azkey-roumu-bot polling lifecycle.
//
// Polling acquires follower relationships and notes, then hands eligible notes
// to a NoteHandler. Relationship writes are delegated to a separate
// FollowerSynchronizer; note processing remains independent of those writes.
package polling

import (
	"context"
	"errors"
	"hash/fnv"
	"log/slog"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

var (
	errClientRequired             = errors.New("misskey client is required")
	errHandlerRequired            = errors.New("note handler is required")
	errContextRequired            = errors.New("polling context is required")
	errAlreadyRunning             = errors.New("poller is already running")
	errInvalidResponse            = errors.New("polling response is invalid")
	errStuckCursor                = errors.New("polling cursor did not advance")
	errRelationshipSyncIncomplete = errors.New("relationship pagination did not complete")
	errTargetGone                 = errors.New("polling target is no longer a follower")
)

const (
	defaultPollInterval         = time.Minute
	defaultFollowerSyncInterval = 10 * time.Minute
	defaultConcurrency          = 2
	defaultRatePerSecond        = 2.0
	defaultRateBurst            = 1
	defaultPageLimit            = 100
	defaultMaxPagesPerTurn      = 5
	defaultDedupLimit           = 10_000
	defaultDedupTTL             = 24 * time.Hour
	defaultStartupSpread        = time.Minute
	defaultBackoffBase          = time.Second
	defaultBackoffMax           = 5 * time.Minute
	maxRelationshipPages        = 10_000
	maxConcurrency              = 1_000
	maxPagesPerTurn             = 10_000
	maxDedupLimit               = 1_000_000
)

// SleepFunc is the cancellable sleep used by the scheduler and rate limiter.
// It is injectable so lifecycle and backoff tests do not need to wait for
// production intervals.
type SleepFunc func(context.Context, time.Duration) error

// RateLimiter is shared by self, relationship, and note requests. SetCooldown
// is used for a server-provided rate-limit window so all request classes
// observe the same cooldown.
type RateLimiter = domain.RequestLimiter

// FollowerSynchronizer receives a replacement snapshot only after both
// relationship lists have been fully fetched and validated.
type FollowerSynchronizer interface {
	Run(context.Context) error
	InvalidateSnapshot()
	UpdateSnapshot(selfID string, followerIDs, followingIDs []string)
}

// Settings controls polling load and in-memory state bounds.
type Settings struct {
	PollInterval         time.Duration
	FollowerSyncInterval time.Duration
	Concurrency          int
	RatePerSecond        float64
	RateBurst            int
	PageLimit            int
	MaxPagesPerTurn      int
	DedupLimit           int
	DedupTTL             time.Duration
	StartupSpread        time.Duration
	BackoffBase          time.Duration
	BackoffMax           time.Duration

	// Clock and Sleep are optional test hooks. Defaults use the system clock
	// and cancellable real timers.
	Clock  func() time.Time
	Sleep  SleepFunc
	Jitter func(time.Duration) time.Duration

	// RateLimiter, when non-nil, replaces the default token bucket. This is
	// useful for deterministic tests and for embedding applications with a
	// shared limiter.
	RateLimiter RateLimiter

	// FollowerSynchronizer is optional for read-only embedders. The command
	// supplies the bot-layer coordinator so relationship writes run separately
	// from the note worker pool.
	FollowerSynchronizer FollowerSynchronizer
}

// DefaultSettings returns the production defaults: all followers are monitored,
// with a usual one-to-two minute observation latency, two concurrent
// workers, and two read requests per second with a burst of one.
func DefaultSettings() Settings {
	return Settings{
		PollInterval:         defaultPollInterval,
		FollowerSyncInterval: defaultFollowerSyncInterval,
		Concurrency:          defaultConcurrency,
		RatePerSecond:        defaultRatePerSecond,
		RateBurst:            defaultRateBurst,
		PageLimit:            defaultPageLimit,
		MaxPagesPerTurn:      defaultMaxPagesPerTurn,
		DedupLimit:           defaultDedupLimit,
		DedupTTL:             defaultDedupTTL,
		StartupSpread:        defaultStartupSpread,
		BackoffBase:          defaultBackoffBase,
		BackoffMax:           defaultBackoffMax,
		Clock:                time.Now,
		Sleep:                sleepContext,
		Jitter:               jitterDuration,
	}
}

// Validate checks load and memory limits before any network request is made.
func (s Settings) Validate() error {
	if s.PollInterval <= 0 || s.FollowerSyncInterval <= 0 {
		return errors.New("poll intervals must be positive")
	}
	if s.Concurrency <= 0 || s.Concurrency > maxConcurrency {
		return errors.New("poll concurrency is outside the supported range")
	}
	if s.RatePerSecond <= 0 || math.IsNaN(s.RatePerSecond) || math.IsInf(s.RatePerSecond, 0) || s.RateBurst <= 0 {
		return errors.New("poll rate settings must be positive")
	}
	if s.PageLimit <= 0 || s.PageLimit > 100 {
		return errors.New("poll page limit must be between 1 and 100")
	}
	if s.MaxPagesPerTurn <= 0 || s.MaxPagesPerTurn > maxPagesPerTurn {
		return errors.New("poll page limit per turn is outside the supported range")
	}
	if s.DedupLimit <= 0 || s.DedupLimit > maxDedupLimit || s.DedupTTL <= 0 {
		return errors.New("poll dedup settings are outside the supported range")
	}
	if s.StartupSpread < 0 || s.BackoffBase <= 0 || s.BackoffMax < s.BackoffBase {
		return errors.New("poll spread and backoff settings are invalid")
	}
	if s.Clock == nil || s.Sleep == nil || s.Jitter == nil {
		return errors.New("poll timing functions are required")
	}
	return nil
}

// Option customizes a Poller.
type Option func(*Settings, **slog.Logger) error

// WithSettings replaces the defaults with settings. Callers that only need
// to change a few values should start with DefaultSettings and modify it.
func WithSettings(settings Settings) Option {
	return func(current *Settings, _ **slog.Logger) error {
		*current = settings
		return nil
	}
}

// WithLogger adds safe lifecycle logging. Polling never logs note text.
func WithLogger(logger *slog.Logger) Option {
	return func(_ *Settings, current **slog.Logger) error {
		*current = logger
		return nil
	}
}

// WithRateLimiter injects a shared limiter without changing other settings.
func WithRateLimiter(limiter RateLimiter) Option {
	return func(settings *Settings, _ **slog.Logger) error {
		if limiter == nil || (reflect.ValueOf(limiter).Kind() == reflect.Pointer && reflect.ValueOf(limiter).IsNil()) {
			return errors.New("poll rate limiter is required")
		}
		settings.RateLimiter = limiter
		return nil
	}
}

// WithTimingHooks injects the clock, sleep, and optional jitter function.
func WithTimingHooks(clock func() time.Time, sleep SleepFunc, jitter func(time.Duration) time.Duration) Option {
	return func(settings *Settings, _ **slog.Logger) error {
		if clock == nil || sleep == nil || jitter == nil {
			return errors.New("poll timing functions are required")
		}
		settings.Clock = clock
		settings.Sleep = sleep
		settings.Jitter = jitter
		return nil
	}
}

// NoteHandler receives eligible target notes. A nil error means either the
// note was processed or intentionally ignored. A non-nil error leaves the
// cursor at the last successful note, so the failed note is retried later.
type NoteHandler interface {
	HandleNote(context.Context, domain.Note) error
}

// NoteHandlerFunc adapts a function to NoteHandler.
type NoteHandlerFunc func(context.Context, domain.Note) error

// HandleNote implements NoteHandler.
func (f NoteHandlerFunc) HandleNote(ctx context.Context, note domain.Note) error {
	return f(ctx, note)
}

// ObservationHandler is the only handler wired by the current command. It
// records note and author IDs without note contents and intentionally performs
// no Misskey write. Business reaction processing belongs to issue #10.
type ObservationHandler struct {
	Logger *slog.Logger
}

// HandleNote logs an observation and treats it as successfully observed.
func (h ObservationHandler) HandleNote(ctx context.Context, note domain.Note) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.Logger != nil {
		h.Logger.Info("observed target note", "note_id", note.ID, "user_id", note.UserID)
	}
	return nil
}

// Client is the read-only Misskey view required by Poller. It intentionally
// excludes follow/reaction operations.
type Client interface {
	Self(context.Context) (domain.User, error)
	ListFollowers(context.Context, string, domain.PageOptions) ([]domain.Following, error)
	ListFollowing(context.Context, string, domain.PageOptions) ([]domain.Following, error)
	ListUserNotes(context.Context, string, domain.NotePageOptions) ([]domain.Note, error)
}

// Poller owns volatile target and note cursor state. State is intentionally
// memory-only; restarting the process re-establishes baselines instead of
// attempting to infer notes missed while it was down.
type Poller struct {
	client             Client
	handler            NoteHandler
	settings           Settings
	logger             *slog.Logger
	limiter            RateLimiter
	followSynchronizer FollowerSynchronizer

	stateMu    sync.Mutex
	selfID     string
	generation uint64
	targets    map[string]*targetState
	dedup      *dedupCache

	runMu   sync.Mutex
	running bool
}

type fetchOperation uint8

const (
	fetchOperationSelf fetchOperation = iota
	fetchOperationRelationshipSync
	fetchOperationTargetNotes
	fetchOperationCount
)

type fetchOperationCounts struct {
	successes int
	failures  int
	inFlight  int
}

type fetchSummary struct {
	mu         sync.Mutex
	operations [fetchOperationCount]fetchOperationCounts
}

type fetchSummarySnapshot struct {
	operations [fetchOperationCount]fetchOperationCounts
}

func newFetchSummary() *fetchSummary {
	return &fetchSummary{}
}

func (s *fetchSummary) begin(operation fetchOperation) {
	if s == nil || operation >= fetchOperationCount {
		return
	}
	s.mu.Lock()
	s.operations[operation].inFlight++
	s.mu.Unlock()
}

func (s *fetchSummary) finish(operation fetchOperation, err error, ctx context.Context) {
	if s == nil || operation >= fetchOperationCount {
		return
	}
	s.mu.Lock()
	counts := &s.operations[operation]
	if counts.inFlight > 0 {
		counts.inFlight--
	}
	if !errors.Is(err, errTargetGone) && (err == nil || ctx == nil || ctx.Err() == nil) {
		if err == nil {
			counts.successes++
		} else {
			counts.failures++
		}
	}
	s.mu.Unlock()
}

func (s *fetchSummary) take() fetchSummarySnapshot {
	var snapshot fetchSummarySnapshot
	if s == nil {
		return snapshot
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for operation := range s.operations {
		snapshot.operations[operation] = s.operations[operation]
		s.operations[operation].successes = 0
		s.operations[operation].failures = 0
	}
	return snapshot
}

func (c fetchOperationCounts) attempts() int {
	return c.successes + c.failures
}

func (s fetchSummarySnapshot) attempts() int {
	total := 0
	for _, operation := range s.operations {
		total += operation.attempts()
	}
	return total
}

func (s fetchSummarySnapshot) failures() int {
	total := 0
	for _, operation := range s.operations {
		total += operation.failures
	}
	return total
}

func (s fetchSummarySnapshot) inFlight() int {
	total := 0
	for _, operation := range s.operations {
		total += operation.inFlight
	}
	return total
}

func (s fetchSummarySnapshot) outcome() string {
	attempts := s.attempts()
	failures := s.failures()
	if attempts == 0 {
		if s.inFlight() > 0 {
			return "in_progress"
		}
		return "idle"
	}
	if failures == 0 {
		return "success"
	}
	if failures == attempts {
		return "failure"
	}
	return "partial_failure"
}

type targetState struct {
	mu sync.Mutex

	id          string
	generation  uint64
	active      bool
	initialized bool
	cursor      string
	activation  time.Time
	dedup       *dedupCache

	// Scheduler-owned fields. They are only read or written by Run's
	// coordinator, except when a target is removed from the membership map.
	nextAt   time.Time
	backoff  time.Duration
	inFlight bool
}

// New creates a read-only poller. A handler is required so production wiring
// cannot accidentally turn note acquisition into an unacknowledged success.
func New(client Client, handler NoteHandler, options ...Option) (*Poller, error) {
	if nilClient(client) {
		return nil, errClientRequired
	}
	if nilHandler(handler) {
		return nil, errHandlerRequired
	}

	settings := DefaultSettings()
	var logger *slog.Logger
	for _, option := range options {
		if option == nil {
			return nil, errors.New("poll option is required")
		}
		if err := option(&settings, &logger); err != nil {
			return nil, err
		}
	}
	if settings.Clock == nil {
		settings.Clock = time.Now
	}
	if settings.Sleep == nil {
		settings.Sleep = sleepContext
	}
	if settings.Jitter == nil {
		settings.Jitter = jitterDuration
	}
	if err := settings.Validate(); err != nil {
		return nil, err
	}

	limiter := settings.RateLimiter
	if limiter == nil {
		limiter = newTokenBucket(settings.RatePerSecond, settings.RateBurst, settings.Clock, settings.Sleep)
	}
	return &Poller{
		client:             client,
		handler:            handler,
		settings:           settings,
		logger:             logger,
		limiter:            limiter,
		followSynchronizer: settings.FollowerSynchronizer,
		targets:            make(map[string]*targetState),
		dedup:              newDedupCache(settings.DedupLimit, settings.DedupTTL),
	}, nil
}

func nilClient(client Client) bool {
	if client == nil {
		return true
	}
	value := reflect.ValueOf(client)
	return (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface || value.Kind() == reflect.Map || value.Kind() == reflect.Func || value.Kind() == reflect.Slice) && value.IsNil()
}

func nilHandler(handler NoteHandler) bool {
	if handler == nil {
		return true
	}
	value := reflect.ValueOf(handler)
	return (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface || value.Kind() == reflect.Map || value.Kind() == reflect.Func || value.Kind() == reflect.Slice) && value.IsNil()
}

// Run initializes the authenticated user, synchronizes the complete follower
// target snapshot, and then schedules serialized per-user note workers until
// ctx is canceled. Authentication failures terminate the lifecycle; transient
// read failures preserve the previous snapshot and are retried with backoff.
func (p *Poller) Run(ctx context.Context) error {
	if p == nil || nilClient(p.client) {
		return errClientRequired
	}
	if nilHandler(p.handler) {
		return errHandlerRequired
	}
	if ctx == nil {
		return errContextRequired
	}

	p.runMu.Lock()
	if p.running {
		p.runMu.Unlock()
		return errAlreadyRunning
	}
	p.running = true
	p.runMu.Unlock()
	defer func() {
		p.runMu.Lock()
		p.running = false
		p.runMu.Unlock()
	}()

	if err := ctx.Err(); err != nil {
		return nil
	}
	var summary *fetchSummary
	if p.logger != nil {
		summary = newFetchSummary()
		stopSummary := make(chan struct{})
		summaryDone := make(chan struct{})
		go p.reportFetchSummaries(summary, stopSummary, summaryDone)
		defer func() {
			close(stopSummary)
			<-summaryDone
			p.logFetchSummary(summary.take(), true)
		}()
	}

	var self domain.User
	selfBackoff := time.Duration(0)
	for {
		var err error
		self, err = p.acquireSelf(ctx, summary)
		if err == nil {
			break
		}
		if isContextError(err, ctx) {
			return nil
		}
		if isAuthError(err) || !isRetryableError(err) {
			return err
		}
		selfBackoff = p.nextBackoff(selfBackoff)
		p.logReadError("self", "", err)
		if err := p.sleepUntil(ctx, p.now().Add(p.retryDelay(err, selfBackoff))); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	p.stateMu.Lock()
	p.selfID = self.ID
	p.stateMu.Unlock()

	var followCancel context.CancelFunc
	var followDone chan error
	followFinished := false
	if p.followSynchronizer != nil && !nilFollowerSynchronizer(p.followSynchronizer) {
		followCtx, cancel := context.WithCancel(ctx)
		followCancel = cancel
		followDone = make(chan error, 1)
		go func() { followDone <- p.followSynchronizer.Run(followCtx) }()
		defer func() {
			followCancel()
			if !followFinished {
				<-followDone
			}
		}()
	}

	now := p.now()
	_, syncErr := p.syncTargets(ctx, now, summary)
	if isContextError(syncErr, ctx) {
		return nil
	}
	if isAuthError(syncErr) {
		return syncErr
	}
	syncBackoff := time.Duration(0)
	nextSync := now.Add(p.settings.FollowerSyncInterval)
	if syncErr != nil {
		syncBackoff = p.nextBackoff(syncBackoff)
		nextSync = now.Add(p.retryDelay(syncErr, syncBackoff))
		p.logReadError("initial target sync", "", syncErr)
	}

	workerCount := p.settings.Concurrency
	jobs := make(chan pollJob, workerCount)
	results := make(chan pollResult, workerCount+1)
	workerCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workers.Add(1)
		go p.worker(workerCtx, jobs, results, &workers, summary)
	}
	defer func() {
		cancel()
		close(jobs)
		workers.Wait()
		p.resetSchedulingState()
	}()

	inFlight := 0
	syncInFlight := false
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		now = p.now()
		dispatched := false
		if !syncInFlight && inFlight < workerCount && !now.Before(nextSync) {
			jobs <- pollJob{kind: pollJobSync}
			syncInFlight = true
			inFlight++
			dispatched = true
		}

		for _, target := range p.dueTargets(now) {
			if inFlight >= workerCount {
				break
			}
			if target.inFlight {
				continue
			}
			target.inFlight = true
			jobs <- pollJob{kind: pollJobTarget, target: target}
			inFlight++
			dispatched = true
		}
		if dispatched {
			continue
		}

		if inFlight > 0 {
			var timer *time.Timer
			var timerC <-chan time.Time
			if inFlight < workerCount {
				wakeAt := p.nextAvailableAt(now, nextSync, syncInFlight)
				if !wakeAt.IsZero() {
					delay := wakeAt.Sub(p.now())
					if delay <= 0 {
						continue
					}
					timer = time.NewTimer(delay)
					timerC = timer.C
				}
			}
			select {
			case result := <-results:
				stopTimer(timer)
				inFlight--
				if result.kind == pollJobSync {
					syncInFlight = false
					if isContextError(result.err, ctx) {
						return nil
					}
					if isAuthError(result.err) {
						return result.err
					}
					now = p.now()
					if result.err != nil {
						syncBackoff = p.nextBackoff(syncBackoff)
						nextSync = now.Add(p.retryDelay(result.err, syncBackoff))
						p.logReadError("target sync", "", result.err)
					} else {
						syncBackoff = 0
						nextSync = now.Add(p.settings.FollowerSyncInterval)
					}
					continue
				}
				if isAuthError(result.err) {
					return result.err
				}
				p.completeTargetJob(result.target, result.err, p.now())
			case <-timerC:
				// A newly due job can use the spare worker. Re-evaluate all
				// scheduling state before dispatching it.
			case <-ctx.Done():
				stopTimer(timer)
				return nil
			case followErr := <-followDone:
				followFinished = true
				stopTimer(timer)
				if ctx.Err() != nil {
					return nil
				}
				if followErr != nil {
					return followErr
				}
				return errors.New("follower synchronizer stopped unexpectedly")
			}
			continue
		}

		next := nextSync
		for _, target := range p.dueTargets(time.Time{}) {
			if target.inFlight {
				continue
			}
			if next.IsZero() || target.nextAt.Before(next) {
				next = target.nextAt
			}
		}
		if next.IsZero() {
			next = p.now().Add(p.settings.FollowerSyncInterval)
		}
		if followDone == nil {
			if err := p.sleepUntil(ctx, next); err != nil {
				return nil
			}
			continue
		}
		waitCtx, cancelWait := context.WithCancel(ctx)
		sleepDone := make(chan error, 1)
		go func() { sleepDone <- p.sleepUntil(waitCtx, next) }()
		select {
		case err := <-sleepDone:
			cancelWait()
			if err != nil {
				return nil
			}
		case followErr := <-followDone:
			followFinished = true
			cancelWait()
			if ctx.Err() != nil {
				return nil
			}
			if followErr != nil {
				return followErr
			}
			return errors.New("follower synchronizer stopped unexpectedly")
		case <-ctx.Done():
			cancelWait()
			return nil
		}
	}
}

type pollJobKind uint8

const (
	pollJobSync pollJobKind = iota + 1
	pollJobTarget
)

type pollJob struct {
	kind   pollJobKind
	target *targetState
}

type pollResult struct {
	kind   pollJobKind
	target *targetState
	err    error
}

func (p *Poller) worker(ctx context.Context, jobs <-chan pollJob, results chan<- pollResult, workers *sync.WaitGroup, summary *fetchSummary) {
	defer workers.Done()
	for job := range jobs {
		result := pollResult{kind: job.kind, target: job.target}
		switch job.kind {
		case pollJobSync:
			_, result.err = p.syncTargets(ctx, p.now(), summary)
		case pollJobTarget:
			result.err = p.runTargetTurn(ctx, job.target, summary)
		}
		select {
		case results <- result:
		case <-ctx.Done():
			return
		}
	}
}

func (p *Poller) reportFetchSummaries(summary *fetchSummary, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(p.settings.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.logFetchSummary(summary.take(), false)
		case <-stop:
			return
		}
	}
}

func (p *Poller) logFetchSummary(snapshot fetchSummarySnapshot, final bool) {
	if p.logger == nil || (final && snapshot.attempts() == 0) {
		return
	}
	self := snapshot.operations[fetchOperationSelf]
	sync := snapshot.operations[fetchOperationRelationshipSync]
	notes := snapshot.operations[fetchOperationTargetNotes]
	p.logger.Debug("polling fetch summary",
		"window", p.settings.PollInterval,
		"final", final,
		"outcome", snapshot.outcome(),
		"attempts", snapshot.attempts(),
		"failures", snapshot.failures(),
		"in_flight", snapshot.inFlight(),
		"self_attempts", self.attempts(),
		"self_successes", self.successes,
		"self_failures", self.failures,
		"relationship_sync_attempts", sync.attempts(),
		"relationship_sync_successes", sync.successes,
		"relationship_sync_failures", sync.failures,
		"target_note_turns_attempts", notes.attempts(),
		"target_note_turns_successes", notes.successes,
		"target_note_turns_failures", notes.failures,
	)
}

func (p *Poller) acquireSelf(ctx context.Context, summary *fetchSummary) (domain.User, error) {
	summary.begin(fetchOperationSelf)
	user, err := p.fetchSelf(ctx)
	if err == nil && !validID(user.ID) {
		err = errInvalidResponse
	}
	summary.finish(fetchOperationSelf, err, ctx)
	return user, err
}

func (p *Poller) syncTargets(ctx context.Context, now time.Time, summary *fetchSummary) ([]string, error) {
	summary.begin(fetchOperationRelationshipSync)
	targets, err := p.fetchAndApplyTargets(ctx, now)
	summary.finish(fetchOperationRelationshipSync, err, ctx)
	return targets, err
}

func (p *Poller) runTargetTurn(ctx context.Context, target *targetState, summary *fetchSummary) error {
	summary.begin(fetchOperationTargetNotes)
	err := p.processTarget(ctx, target)
	summary.finish(fetchOperationTargetNotes, err, ctx)
	return err
}

// dueTargets returns targets ordered by due time and ID. A stable ID order
// keeps scheduling deterministic while the due time prevents one busy target
// from monopolizing workers.
func (p *Poller) dueTargets(at time.Time) []*targetState {
	p.stateMu.Lock()
	targets := make([]*targetState, 0, len(p.targets))
	for _, target := range p.targets {
		if at.IsZero() || !target.nextAt.After(at) {
			targets = append(targets, target)
		}
	}
	p.stateMu.Unlock()
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].nextAt.Equal(targets[j].nextAt) {
			return targets[i].id < targets[j].id
		}
		return targets[i].nextAt.Before(targets[j].nextAt)
	})
	return targets
}

// nextAvailableAt returns the next future time at which work can be dispatched
// while at least one worker is available. Work that is already in flight is
// deliberately ignored: its result, rather than a timer, is what can free
// that target or sync slot.
func (p *Poller) nextAvailableAt(now, nextSync time.Time, syncInFlight bool) time.Time {
	var next time.Time
	if !syncInFlight && nextSync.After(now) {
		next = nextSync
	}

	p.stateMu.Lock()
	for _, target := range p.targets {
		if target.inFlight || target.nextAt.IsZero() || !target.nextAt.After(now) {
			continue
		}
		if next.IsZero() || target.nextAt.Before(next) {
			next = target.nextAt
		}
	}
	p.stateMu.Unlock()
	return next
}

func stopTimer(timer *time.Timer) {
	if timer == nil {
		return
	}
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

// resetSchedulingState makes a canceled Run safe to start again. Workers are
// joined before this is called, so no worker can subsequently publish a result
// for the old scheduler state.
func (p *Poller) resetSchedulingState() {
	now := p.now()
	p.stateMu.Lock()
	for _, target := range p.targets {
		target.inFlight = false
		target.backoff = 0
		target.nextAt = now
	}
	p.stateMu.Unlock()
}

func (p *Poller) completeTargetJob(target *targetState, err error, now time.Time) {
	if target == nil {
		return
	}
	p.stateMu.Lock()
	current := p.targets[target.id] == target
	if current {
		target.inFlight = false
		if err == nil || errors.Is(err, errTargetGone) {
			target.backoff = 0
			target.nextAt = now.Add(p.settings.PollInterval)
		} else if isAuthError(err) {
			target.nextAt = now.Add(p.settings.PollInterval)
		} else if isRetryableError(err) {
			target.backoff = p.nextBackoff(target.backoff)
			delay := target.backoff
			if apiErr, ok := asDomainError(err); ok && apiErr.Kind == domain.ErrorKindRateLimit && apiErr.RetryAfter != nil {
				delay = *apiErr.RetryAfter
			} else {
				delay = p.withJitter(delay)
			}
			target.nextAt = now.Add(delay)
			p.logReadError("target notes", target.id, err)
		} else {
			target.backoff = 0
			target.nextAt = now.Add(p.settings.PollInterval)
			p.logReadError("target notes", target.id, err)
		}
	}
	p.stateMu.Unlock()
}

func (p *Poller) fetchSelf(ctx context.Context) (domain.User, error) {
	var user domain.User
	err := p.read(ctx, func(ctx context.Context) error {
		var err error
		user, err = p.client.Self(ctx)
		return err
	})
	return user, err
}

type relationshipDirection uint8

const (
	inboundRelationships relationshipDirection = iota + 1
	outboundRelationships
)

func (p *Poller) fetchAndApplyTargets(ctx context.Context, now time.Time) ([]string, error) {
	if p.followSynchronizer != nil && !nilFollowerSynchronizer(p.followSynchronizer) {
		// Do not let the standalone relationship worker act on stale data while
		// a new complete pair of relationship lists is being acquired.
		p.followSynchronizer.InvalidateSnapshot()
	}
	inbound, err := p.fetchRelationshipIDs(ctx, inboundRelationships)
	if err != nil {
		return nil, err
	}
	outbound, err := p.fetchRelationshipIDs(ctx, outboundRelationships)
	if err != nil {
		return nil, err
	}

	// The complete follower set defines note targets. Outbound relationships
	// are retained separately for the bot-layer reconciliation coordinator.
	sort.Strings(inbound)
	p.applyTargetSnapshot(inbound, now)
	if p.followSynchronizer != nil && !nilFollowerSynchronizer(p.followSynchronizer) {
		p.followSynchronizer.UpdateSnapshot(p.selfIdentifier(), inbound, outbound)
	}
	return inbound, nil
}

func (p *Poller) fetchRelationshipIDs(ctx context.Context, direction relationshipDirection) ([]string, error) {
	p.stateMu.Lock()
	selfID := p.selfID
	p.stateMu.Unlock()
	if !validID(selfID) {
		return nil, errInvalidResponse
	}

	var list func(context.Context, string, domain.PageOptions) ([]domain.Following, error)
	switch direction {
	case inboundRelationships:
		list = p.client.ListFollowers
	case outboundRelationships:
		list = p.client.ListFollowing
	default:
		return nil, errInvalidResponse
	}

	ids := make(map[string]struct{})
	untilID := ""
	seenRelationships := make(map[string]domain.Following)
	for page := 0; page < maxRelationshipPages; page++ {
		options := domain.PageOptions{Limit: p.settings.PageLimit, UntilID: untilID}
		var relationships []domain.Following
		err := p.read(ctx, func(ctx context.Context) error {
			var err error
			relationships, err = list(ctx, selfID, options)
			return err
		})
		if err != nil {
			return nil, err
		}
		if relationships == nil {
			return nil, errInvalidResponse
		}
		if len(relationships) == 0 {
			result := make([]string, 0, len(ids))
			for id := range ids {
				result = append(result, id)
			}
			sort.Strings(result)
			return result, nil
		}

		previousID := ""
		for _, relationship := range relationships {
			if !validRelationship(relationship, selfID, direction) {
				return nil, errInvalidResponse
			}
			if previousID != "" && strings.Compare(relationship.ID, previousID) > 0 {
				return nil, errInvalidResponse
			}
			if untilID != "" && strings.Compare(relationship.ID, untilID) > 0 {
				return nil, errInvalidResponse
			}
			previousID = relationship.ID
			previous, seen := seenRelationships[relationship.ID]
			if seen && (previous.FollowerID != relationship.FollowerID || previous.FolloweeID != relationship.FolloweeID) {
				return nil, errInvalidResponse
			}
			if !seen {
				seenRelationships[relationship.ID] = relationship
			}
			candidateID := relationship.FollowerID
			if direction == outboundRelationships {
				candidateID = relationship.FolloweeID
			}
			if candidateID == selfID || seen {
				continue
			}
			ids[candidateID] = struct{}{}
		}

		nextUntilID := relationships[len(relationships)-1].ID
		if !validID(nextUntilID) || (untilID != "" && strings.Compare(nextUntilID, untilID) >= 0) {
			return nil, errRelationshipSyncIncomplete
		}
		untilID = nextUntilID
	}
	return nil, errRelationshipSyncIncomplete
}

func validRelationship(relationship domain.Following, selfID string, direction relationshipDirection) bool {
	if !validID(relationship.ID) || !validID(relationship.FollowerID) || !validID(relationship.FolloweeID) {
		return false
	}
	if relationship.Follower != nil && (!validID(relationship.Follower.ID) || relationship.Follower.ID != relationship.FollowerID) {
		return false
	}
	if relationship.Followee != nil && (!validID(relationship.Followee.ID) || relationship.Followee.ID != relationship.FolloweeID) {
		return false
	}
	switch direction {
	case inboundRelationships:
		return relationship.FolloweeID == selfID
	case outboundRelationships:
		return relationship.FollowerID == selfID
	default:
		return false
	}
}

func (p *Poller) applyTargetSnapshot(targetIDs []string, now time.Time) {
	newIDs := make(map[string]struct{}, len(targetIDs))
	for _, id := range targetIDs {
		if validID(id) && id != p.selfIdentifier() {
			newIDs[id] = struct{}{}
		}
	}

	p.stateMu.Lock()
	removed := make([]*targetState, 0)
	for id, target := range p.targets {
		if _, present := newIDs[id]; !present {
			delete(p.targets, id)
			removed = append(removed, target)
		}
	}
	for id := range newIDs {
		if _, present := p.targets[id]; present {
			continue
		}
		p.generation++
		target := &targetState{
			id:         id,
			generation: p.generation,
			active:     true,
			dedup:      p.dedup,
			nextAt:     now.Add(p.startupOffset(id)),
		}
		p.targets[id] = target
	}
	p.stateMu.Unlock()

	// Do not hold stateMu while waiting for a worker currently processing a
	// removed target. This ordering also lets a re-added user receive a fresh
	// generation immediately.
	for _, target := range removed {
		target.mu.Lock()
		target.active = false
		target.mu.Unlock()
	}
}

func (p *Poller) selfIdentifier() string {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	return p.selfID
}

func (p *Poller) startupOffset(id string) time.Duration {
	if p.settings.StartupSpread <= 0 {
		return 0
	}
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(id))
	return time.Duration(hash.Sum64() % uint64(p.settings.StartupSpread))
}

func (p *Poller) processTarget(ctx context.Context, target *targetState) error {
	if target == nil {
		return errTargetGone
	}
	target.mu.Lock()
	defer target.mu.Unlock()
	if !p.currentTarget(target) || !target.active {
		return errTargetGone
	}
	if !target.initialized {
		return p.bootstrapTarget(ctx, target)
	}

	for page := 0; page < p.settings.MaxPagesPerTurn; page++ {
		options := p.noteOptions(target)
		var notes []domain.Note
		err := p.read(ctx, func(ctx context.Context) error {
			var err error
			notes, err = p.client.ListUserNotes(ctx, target.id, options)
			return err
		})
		if err != nil {
			return err
		}
		if notes == nil {
			return errInvalidResponse
		}
		if err := validateDifferentialPage(notes, target.id); err != nil {
			return err
		}
		if len(notes) == 0 {
			return nil
		}

		progressed := false
		for _, note := range notes {
			if strings.Compare(note.ID, target.cursor) <= 0 {
				continue
			}

			// A sinceDate overlap is used only while the empty baseline has no
			// ID cursor. Strictly older notes are intentionally excluded; a
			// note created exactly at activation remains eligible.
			if !target.activation.IsZero() && note.CreatedAt.Before(target.activation) {
				target.cursor = note.ID
				progressed = true
				continue
			}

			if note.UserID == p.selfIdentifier() || note.Visibility != "public" {
				target.cursor = note.ID
				progressed = true
				continue
			}
			if p.dedup.contains(note.ID, p.now()) {
				target.cursor = note.ID
				progressed = true
				continue
			}

			// Membership is checked immediately before delivery. The target
			// mutex serializes this worker with snapshot removal; generation
			// checking prevents an old worker from delivering after re-add.
			if !p.currentTarget(target) || !target.active {
				return errTargetGone
			}
			if err := p.handler.HandleNote(ctx, note); err != nil {
				return err
			}
			if !p.currentTarget(target) || !target.active {
				return errTargetGone
			}
			target.cursor = note.ID
			p.dedup.add(note.ID, p.now())
			progressed = true
		}
		if !progressed {
			return errStuckCursor
		}
	}
	return nil
}

func (p *Poller) bootstrapTarget(ctx context.Context, target *targetState) error {
	activation := p.now().Truncate(time.Millisecond)
	options := p.noteOptions(target)
	options.SinceID = ""
	options.SinceDate = nil
	var notes []domain.Note
	err := p.read(ctx, func(ctx context.Context) error {
		var err error
		notes, err = p.client.ListUserNotes(ctx, target.id, options)
		return err
	})
	if err != nil {
		return err
	}
	if notes == nil {
		return errInvalidResponse
	}
	if err := validateBootstrapPage(notes, target.id); err != nil {
		return err
	}
	target.initialized = true
	if len(notes) == 0 {
		// Store the millisecond boundary only after a successful, validated
		// request. A failed bootstrap remains uninitialized and can establish a
		// fresh boundary on its next attempt.
		target.activation = activation
		target.cursor = ""
		return nil
	}
	target.activation = time.Time{}
	target.cursor = notes[0].ID
	return nil
}

func (p *Poller) noteOptions(target *targetState) domain.NotePageOptions {
	options := domain.NotePageOptions{
		Limit:            p.settings.PageLimit,
		SinceID:          target.cursor,
		WithReplies:      true,
		WithRenotes:      true,
		WithChannelNotes: false,
	}
	if target.cursor == "" && !target.activation.IsZero() {
		activation := target.activation.Add(-time.Millisecond)
		options.SinceDate = &activation
	}
	return options
}

func validateBootstrapPage(notes []domain.Note, targetID string) error {
	if !validID(targetID) {
		return errInvalidResponse
	}
	previous := ""
	for _, note := range notes {
		if !validNoteShape(note) || note.UserID != targetID {
			return errInvalidResponse
		}
		if previous != "" && strings.Compare(note.ID, previous) > 0 {
			return errInvalidResponse
		}
		previous = note.ID
	}
	return nil
}

func validateDifferentialPage(notes []domain.Note, targetID string) error {
	if !validID(targetID) {
		return errInvalidResponse
	}
	previous := ""
	for _, note := range notes {
		if !validNoteShape(note) || note.UserID != targetID {
			return errInvalidResponse
		}
		if previous != "" && strings.Compare(note.ID, previous) < 0 {
			return errInvalidResponse
		}
		previous = note.ID
	}
	return nil
}

func validNoteShape(note domain.Note) bool {
	if !validID(note.ID) || !validID(note.UserID) {
		return false
	}
	if note.Visibility != "public" && note.Visibility != "home" && note.Visibility != "followers" && note.Visibility != "specified" {
		return false
	}
	if note.User != nil && (!validID(note.User.ID) || note.User.ID != note.UserID) {
		return false
	}
	return true
}

func validID(value string) bool {
	return value != "" && value == strings.TrimSpace(value)
}

func (p *Poller) currentTarget(target *targetState) bool {
	p.stateMu.Lock()
	defer p.stateMu.Unlock()
	current := p.targets[target.id]
	return current == target && current.generation == target.generation
}

func (p *Poller) read(ctx context.Context, request func(context.Context) error) error {
	if err := p.limiter.Wait(ctx); err != nil {
		return err
	}
	err := request(ctx)
	if apiErr, ok := asDomainError(err); ok && apiErr.Kind == domain.ErrorKindRateLimit {
		delay := time.Second
		if apiErr.RetryAfter != nil {
			delay = *apiErr.RetryAfter
		}
		p.limiter.SetCooldown(p.now().Add(delay))
	}
	return err
}

func nilFollowerSynchronizer(synchronizer FollowerSynchronizer) bool {
	if synchronizer == nil {
		return true
	}
	value := reflect.ValueOf(synchronizer)
	return (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface || value.Kind() == reflect.Map || value.Kind() == reflect.Func || value.Kind() == reflect.Slice) && value.IsNil()
}

// NewRateLimiter creates the shared API request limiter used for both polling
// reads and relationship writes.
func NewRateLimiter(ratePerSecond float64, burst int) (RateLimiter, error) {
	if ratePerSecond <= 0 || math.IsNaN(ratePerSecond) || math.IsInf(ratePerSecond, 0) || burst <= 0 {
		return nil, errors.New("API rate settings must be positive")
	}
	return newTokenBucket(ratePerSecond, burst, time.Now, sleepContext), nil
}

func (p *Poller) nextBackoff(previous time.Duration) time.Duration {
	if previous <= 0 {
		return p.settings.BackoffBase
	}
	if previous >= p.settings.BackoffMax/2 {
		return p.settings.BackoffMax
	}
	next := previous * 2
	if next > p.settings.BackoffMax {
		return p.settings.BackoffMax
	}
	return next
}

func (p *Poller) withJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return 0
	}
	value := p.settings.Jitter(delay)
	if value <= 0 {
		return time.Nanosecond
	}
	return value
}

func (p *Poller) retryDelay(err error, backoff time.Duration) time.Duration {
	if apiErr, ok := asDomainError(err); ok && apiErr.Kind == domain.ErrorKindRateLimit && apiErr.RetryAfter != nil {
		return *apiErr.RetryAfter
	}
	return p.withJitter(backoff)
}

func (p *Poller) now() time.Time {
	return p.settings.Clock()
}

func (p *Poller) sleepUntil(ctx context.Context, target time.Time) error {
	delay := target.Sub(p.now())
	if delay <= 0 {
		return nil
	}
	return p.settings.Sleep(ctx, delay)
}

func (p *Poller) logReadError(operation, targetID string, err error) {
	if p.logger == nil || err == nil || errors.Is(err, errTargetGone) {
		return
	}
	if apiErr, ok := asDomainError(err); ok {
		p.logger.Warn("polling read failed", "operation", operation, "target_id", targetID, "kind", apiErr.Kind, "status", apiErr.StatusCode)
		return
	}
	p.logger.Warn("polling operation failed", "operation", operation, "target_id", targetID)
}

func isAuthError(err error) bool {
	apiErr, ok := asDomainError(err)
	return ok && apiErr.Kind == domain.ErrorKindAuth
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errStuckCursor) || errors.Is(err, errInvalidResponse) || errors.Is(err, errRelationshipSyncIncomplete) {
		return false
	}
	if apiErr, ok := asDomainError(err); ok {
		switch apiErr.Kind {
		case domain.ErrorKindServer, domain.ErrorKindNetwork, domain.ErrorKindNetworkTimeout, domain.ErrorKindRateLimit, domain.ErrorKindCanceled:
			return true
		default:
			return false
		}
	}
	return true
}

func isContextError(err error, ctx context.Context) bool {
	return err != nil && ctx != nil && ctx.Err() != nil
}

func asDomainError(err error) (*domain.Error, bool) {
	if err == nil {
		return nil, false
	}
	var apiErr *domain.Error
	if errors.As(err, &apiErr) {
		return apiErr, true
	}
	return nil, false
}

type dedupCache struct {
	mu    sync.Mutex
	limit int
	ttl   time.Duration
	items map[string]time.Time
}

func newDedupCache(limit int, ttl time.Duration) *dedupCache {
	return &dedupCache{limit: limit, ttl: ttl, items: make(map[string]time.Time)}
}

func (d *dedupCache) purgeLocked(now time.Time) {
	for id, expires := range d.items {
		if !expires.After(now) {
			delete(d.items, id)
		}
	}
}

func (d *dedupCache) contains(id string, now time.Time) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.purgeLocked(now)
	expires, ok := d.items[id]
	return ok && expires.After(now)
}

func (d *dedupCache) add(id string, now time.Time) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.purgeLocked(now)
	d.items[id] = now.Add(d.ttl)
	for len(d.items) > d.limit {
		oldestID := ""
		var oldest time.Time
		for candidate, expires := range d.items {
			if oldestID == "" || expires.Before(oldest) || (expires.Equal(oldest) && candidate < oldestID) {
				oldestID = candidate
				oldest = expires
			}
		}
		if oldestID == "" {
			break
		}
		delete(d.items, oldestID)
	}
}

func sleepContext(ctx context.Context, delay time.Duration) error {
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

func jitterDuration(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	// The process-global source is safe for concurrent use. A small bounded
	// jitter avoids synchronized retries without changing the configured cap.
	factor := 0.8 + rand.Float64()*0.4
	return time.Duration(float64(delay) * factor)
}

type tokenBucket struct {
	mu       sync.Mutex
	rate     float64
	burst    float64
	tokens   float64
	last     time.Time
	cooldown time.Time
	now      func() time.Time
	sleep    SleepFunc
}

func newTokenBucket(rate float64, burst int, now func() time.Time, sleep SleepFunc) *tokenBucket {
	return &tokenBucket{
		rate:   rate,
		burst:  float64(burst),
		tokens: float64(burst),
		last:   now(),
		now:    now,
		sleep:  sleep,
	}
}

func (b *tokenBucket) Wait(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		now := b.now()
		b.mu.Lock()
		if now.After(b.last) {
			b.tokens = minFloat(b.burst, b.tokens+now.Sub(b.last).Seconds()*b.rate)
			b.last = now
		}
		wait := time.Duration(0)
		if b.cooldown.After(now) {
			wait = b.cooldown.Sub(now)
		} else if b.tokens >= 1 {
			b.tokens--
			b.mu.Unlock()
			return nil
		} else {
			wait = time.Duration((1 - b.tokens) / b.rate * float64(time.Second))
			if wait <= 0 {
				wait = time.Nanosecond
			}
		}
		b.mu.Unlock()
		if err := b.sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (b *tokenBucket) SetCooldown(until time.Time) {
	b.mu.Lock()
	if until.After(b.cooldown) {
		b.cooldown = until
	}
	b.mu.Unlock()
}

func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
