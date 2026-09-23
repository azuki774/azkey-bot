package polling

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/misskey"
)

var (
	testNow       = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	errTestAPI    = errors.New("test API failure")
	errTestHandle = errors.New("test handler failure")
)

func TestRunReturnsAfterCancellation(t *testing.T) {
	client, err := misskey.NewClient(mustBaseURL(t), "polling-test-token")
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	poller, err := New(client, ObservationHandler{})
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancel()
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
}

func TestSettingsRejectNonFiniteRate(t *testing.T) {
	for _, rate := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		t.Run(strconv.FormatFloat(rate, 'g', -1, 64), func(t *testing.T) {
			settings := DefaultSettings()
			settings.RatePerSecond = rate
			if err := settings.Validate(); err == nil {
				t.Fatalf("Settings.Validate accepted rate %v", rate)
			}
		})
	}
}

func TestStartupOffsetUsesTheFullDefaultSpread(t *testing.T) {
	poller := newTestPoller(t, &fakeClient{self: domain.User{ID: "bot"}}, nil)
	poller.settings.StartupSpread = defaultStartupSpread

	const sampleCount = 1_000
	const bucketWidth = 6 * time.Second
	buckets := make(map[int]struct{})
	var maximum time.Duration
	for i := 0; i < sampleCount; i++ {
		offset := poller.startupOffset("follower-" + strconv.Itoa(i))
		if offset < 0 || offset >= defaultStartupSpread {
			t.Fatalf("startup offset = %s, want [0, %s)", offset, defaultStartupSpread)
		}
		if offset > maximum {
			maximum = offset
		}
		buckets[int(offset/bucketWidth)] = struct{}{}
	}
	if maximum <= 30*time.Second {
		t.Fatalf("startup offsets only reached %s; expected distribution across the minute", maximum)
	}
	if len(buckets) < 8 {
		t.Fatalf("startup offsets occupied %d of 10 buckets; expected broad distribution", len(buckets))
	}
}

func TestRequestDeadlineDoesNotCancelAnActiveLifecycle(t *testing.T) {
	requestErr := domain.NewContextError(domain.ErrorKindNetworkTimeout, context.DeadlineExceeded)
	if isContextError(requestErr, context.Background()) {
		t.Fatal("request timeout was treated as parent cancellation")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !isContextError(requestErr, ctx) {
		t.Fatal("parent cancellation was not recognized")
	}
}

func TestRunRetriesTransientSelfFailureUntilSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	retryAfter := 3 * time.Second
	client := &fakeClient{
		self:       domain.User{ID: "bot"},
		selfErrors: []error{domain.NewError(domain.ErrorKindRateLimit, 429, "RATE_LIMIT", &retryAfter)},
		followers: func(string, domain.PageOptions) ([]domain.Following, error) {
			cancel()
			return []domain.Following{}, nil
		},
	}
	poller := newTestPoller(t, client, nil)
	var sleeps []time.Duration
	poller.settings.Sleep = func(ctx context.Context, delay time.Duration) error {
		sleeps = append(sleeps, delay)
		return ctx.Err()
	}

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := client.selfCallCount(); got != 2 {
		t.Fatalf("Self calls = %d, want transient retry followed by success", got)
	}
	if !reflect.DeepEqual(sleeps, []time.Duration{retryAfter}) {
		t.Fatalf("retry sleeps = %v, want [%s]", sleeps, retryAfter)
	}
	limiter, ok := poller.limiter.(*recordingLimiter)
	if !ok {
		t.Fatalf("limiter type = %T, want *recordingLimiter", poller.limiter)
	}
	limiter.mu.Lock()
	gotWaits := limiter.waits
	limiter.mu.Unlock()
	if got := gotWaits; got != 3 {
		t.Fatalf("limiter waits = %d, want Self twice plus follower sync", got)
	}
}

func TestRunStopsOnNonRetryableStartupSelfFailure(t *testing.T) {
	authErr := domain.NewError(domain.ErrorKindAuth, 401, "UNAUTHORIZED", nil)
	client := &fakeClient{
		self:       domain.User{ID: "bot"},
		selfErrors: []error{authErr},
	}
	poller := newTestPoller(t, client, nil)

	if err := poller.Run(context.Background()); !errors.Is(err, authErr) {
		t.Fatalf("Run error = %v, want %v", err, authErr)
	}
	if got := client.selfCallCount(); got != 1 {
		t.Fatalf("Self calls = %d, want 1", got)
	}
}

func TestBootstrapExistingNotesOnlyInitializesCursor(t *testing.T) {
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID != "" || options.SinceDate != nil {
				t.Fatalf("bootstrap options = %+v", options)
			}
			return []domain.Note{
				testNote("n2", "follower", "public", testNow.Add(-time.Minute)),
				testNote("n1", "follower", "public", testNow.Add(-2*time.Minute)),
			}, nil
		},
	}
	poller := newTestPoller(t, client, nil)
	target := addTarget(t, poller, "follower")

	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("processTarget returned error: %v", err)
	}
	if !target.initialized || target.cursor != "n2" || !target.activation.IsZero() {
		t.Fatalf("target after bootstrap = %+v", target)
	}
	if got := client.noteCallCount(); got != 1 {
		t.Fatalf("bootstrap calls = %d, want 1", got)
	}
}

func TestEmptyBaselineUsesOverlappingSinceDateAndKeepsFirstNewPost(t *testing.T) {
	activationNow := testNow.Add(900 * time.Microsecond)
	var calls []domain.NotePageOptions
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			calls = append(calls, options)
			switch {
			case options.SinceID == "" && options.SinceDate == nil:
				return []domain.Note{}, nil
			case options.SinceID == "n1":
				return []domain.Note{}, nil
			case options.SinceID == "" && options.SinceDate != nil:
				return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
			default:
				return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
			}
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	}))
	poller.settings.Clock = func() time.Time { return activationNow }
	target := addTarget(t, poller, "follower")

	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("empty bootstrap returned error: %v", err)
	}
	if !target.activation.Equal(testNow) || target.cursor != "" {
		t.Fatalf("empty baseline = activation %s cursor %q", target.activation, target.cursor)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("first differential returned error: %v", err)
	}
	if !reflect.DeepEqual(handled, []string{"n1"}) || target.cursor != "n1" {
		t.Fatalf("handled = %v cursor = %q", handled, target.cursor)
	}
	if len(calls) != 3 || calls[1].SinceDate == nil {
		t.Fatalf("note calls = %+v, want an overlapping sinceDate call", calls)
	}
	wantSinceDate := testNow.Add(-time.Millisecond)
	if !calls[1].SinceDate.Equal(wantSinceDate) {
		t.Fatalf("sinceDate = %s, want %s", calls[1].SinceDate, wantSinceDate)
	}
	if !calls[1].WithReplies || !calls[1].WithRenotes || calls[1].WithChannelNotes {
		t.Fatalf("note filters = %+v", calls[1])
	}
}

func TestFailedBootstrapRemainsUninitializedAndDoesNotMoveActivation(t *testing.T) {
	call := 0
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, _ domain.NotePageOptions) ([]domain.Note, error) {
			call++
			if call == 1 {
				return nil, errTestAPI
			}
			return []domain.Note{}, nil
		},
	}
	poller := newTestPoller(t, client, nil)
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errTestAPI) {
		t.Fatalf("first bootstrap error = %v, want %v", err, errTestAPI)
	}
	if target.initialized || !target.activation.IsZero() || target.cursor != "" {
		t.Fatalf("failed bootstrap changed target = %+v", target)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("retry bootstrap returned error: %v", err)
	}
	if !target.initialized || !target.activation.Equal(testNow) {
		t.Fatalf("retry bootstrap target = %+v", target)
	}
}

func TestDifferentialPaginationHandlesShortPagesSameTimestampAndDuplicates(t *testing.T) {
	var calls []string
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			calls = append(calls, options.SinceID)
			switch options.SinceID {
			case "":
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			case "n0":
				return []domain.Note{
					testNote("n1", "follower", "public", testNow),
					testNote("n1", "follower", "public", testNow),
					testNote("n2", "follower", "public", testNow),
				}, nil
			case "n2":
				return []domain.Note{testNote("n3", "follower", "public", testNow)}, nil
			case "n3":
				return []domain.Note{}, nil
			default:
				return nil, errors.New("unexpected cursor")
			}
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("differential returned error: %v", err)
	}
	if !reflect.DeepEqual(handled, []string{"n1", "n2", "n3"}) {
		t.Fatalf("handled = %v", handled)
	}
	if !reflect.DeepEqual(calls, []string{"", "n0", "n2", "n3"}) {
		t.Fatalf("sinceId calls = %v", calls)
	}
	if target.cursor != "n3" {
		t.Fatalf("cursor = %q, want n3", target.cursor)
	}
}

func TestDifferentialPageLimitResumesLater(t *testing.T) {
	var calls []string
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			calls = append(calls, options.SinceID)
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			id := options.SinceID
			if id == "n0" {
				return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
			}
			if id == "n1" {
				return []domain.Note{testNote("n2", "follower", "public", testNow)}, nil
			}
			if id == "n2" {
				return []domain.Note{testNote("n3", "follower", "public", testNow)}, nil
			}
			return []domain.Note{}, nil
		},
	}
	poller := newTestPoller(t, client, nil)
	poller.settings.MaxPagesPerTurn = 2
	var handled []string
	poller.handler = NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	})
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("first turn returned error: %v", err)
	}
	if target.cursor != "n2" || !reflect.DeepEqual(handled, []string{"n1", "n2"}) {
		t.Fatalf("first turn cursor=%q handled=%v", target.cursor, handled)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("resumed turn returned error: %v", err)
	}
	if target.cursor != "n3" || !reflect.DeepEqual(handled, []string{"n1", "n2", "n3"}) {
		t.Fatalf("resumed cursor=%q handled=%v", target.cursor, handled)
	}
	if !reflect.DeepEqual(calls, []string{"", "n0", "n1", "n2", "n3"}) {
		t.Fatalf("calls = %v", calls)
	}
}

func TestDuplicatesCanProgressButStuckCursorStops(t *testing.T) {
	page := 0
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			page++
			if page == 1 {
				return []domain.Note{testNote("n0", "follower", "public", testNow), testNote("n1", "follower", "public", testNow), testNote("n1", "follower", "public", testNow)}, nil
			}
			if page == 2 {
				return []domain.Note{}, nil
			}
			return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("progressing duplicate page returned error: %v", err)
	}
	if target.cursor != "n1" || !reflect.DeepEqual(handled, []string{"n1"}) {
		t.Fatalf("progressing duplicate result cursor=%q handled=%v", target.cursor, handled)
	}
	err := poller.processTarget(context.Background(), target)
	if !errors.Is(err, errStuckCursor) {
		t.Fatalf("stuck page error = %v, want %v", err, errStuckCursor)
	}
	if target.cursor != "n1" || len(handled) != 1 || page != 3 {
		t.Fatalf("stuck result cursor=%q handled=%v calls=%d", target.cursor, handled, page)
	}
}

func TestMalformedOrderingIsRejectedBeforeDelivery(t *testing.T) {
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			return []domain.Note{testNote("n2", "follower", "public", testNow), testNote("n1", "follower", "public", testNow)}, nil
		},
	}
	var handled int
	poller := newTestPoller(t, client, NoteHandlerFunc(func(context.Context, domain.Note) error {
		handled++
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("ordering error = %v, want %v", err, errInvalidResponse)
	}
	if handled != 0 || target.cursor != "n0" {
		t.Fatalf("ordering result handled=%d cursor=%q", handled, target.cursor)
	}
}

func TestVisibilityAndMismatchedAuthorsRejectWholePage(t *testing.T) {
	malformed := true
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			if options.SinceID == "n4" {
				return []domain.Note{}, nil
			}
			if !malformed {
				return []domain.Note{
					testNote("n3", "follower", "followers", testNow),
					testNote("n4", "follower", "public", testNow),
				}, nil
			}
			return []domain.Note{
				testNote("n1", "bot", "public", testNow),
				testNote("n2", "other", "public", testNow),
				testNote("n3", "follower", "followers", testNow),
				testNote("n4", "follower", "public", testNow),
			}, nil
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("differential error = %v, want %v", err, errInvalidResponse)
	}
	if len(handled) != 0 || target.cursor != "n0" {
		t.Fatalf("handled=%v cursor=%q", handled, target.cursor)
	}
	malformed = false
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("valid differential returned error: %v", err)
	}
	if !reflect.DeepEqual(handled, []string{"n4"}) || target.cursor != "n4" {
		t.Fatalf("valid page handled=%v cursor=%q", handled, target.cursor)
	}
}

func TestMalformedLaterNotePreventsEarlierDelivery(t *testing.T) {
	client := &fakeClient{
		self: domain.User{ID: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			return []domain.Note{
				testNote("n1", "follower", "public", testNow),
				testNote("n2", "other", "public", testNow),
			}, nil
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("differential error = %v, want %v", err, errInvalidResponse)
	}
	if len(handled) != 0 || target.cursor != "n0" {
		t.Fatalf("malformed page delivered=%v cursor=%q; want no delivery and n0", handled, target.cursor)
	}
}

func TestBootstrapRejectsConflictingEmbeddedUser(t *testing.T) {
	note := testNote("n1", "follower", "public", testNow)
	note.User = &domain.User{ID: "other"}
	client := &fakeClient{
		self: domain.User{ID: "bot"},
		notes: func(_ string, _ domain.NotePageOptions) ([]domain.Note, error) {
			return []domain.Note{note}, nil
		},
	}
	poller := newTestPoller(t, client, nil)
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("bootstrap error = %v, want %v", err, errInvalidResponse)
	}
	if target.initialized || target.cursor != "" {
		t.Fatalf("invalid bootstrap changed target = %+v", target)
	}
}

func TestHandlerFailureAdvancesOnlySuccessfulNotes(t *testing.T) {
	attempts := 0
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			if options.SinceID == "n0" {
				return []domain.Note{testNote("n1", "follower", "public", testNow), testNote("n2", "follower", "public", testNow)}, nil
			}
			if options.SinceID == "n1" {
				return []domain.Note{testNote("n2", "follower", "public", testNow)}, nil
			}
			return []domain.Note{}, nil
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		attempts++
		if note.ID == "n2" && attempts == 2 {
			return errTestHandle
		}
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errTestHandle) {
		t.Fatalf("handler error = %v, want %v", err, errTestHandle)
	}
	if target.cursor != "n1" || !reflect.DeepEqual(handled, []string{"n1", "n2"}) {
		t.Fatalf("failed delivery cursor=%q handled=%v", target.cursor, handled)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("retry returned error: %v", err)
	}
	if target.cursor != "n2" || !reflect.DeepEqual(handled, []string{"n1", "n2", "n2"}) {
		t.Fatalf("retry cursor=%q handled=%v", target.cursor, handled)
	}
	if !target.dedup.contains("n2", testNow) {
		t.Fatal("successful retry was not added to dedup cache")
	}
}

func TestDedupLimitIsSharedAcrossTargetsAndExpires(t *testing.T) {
	poller := newTestPoller(t, &fakeClient{self: domain.User{ID: "bot"}}, nil)
	poller.dedup.limit = 2
	poller.dedup.ttl = time.Hour
	poller.applyTargetSnapshot([]string{"f1", "f2"}, testNow)
	f1 := targetFor(poller, "f1")
	f2 := targetFor(poller, "f2")
	if f1.dedup != poller.dedup || f2.dedup != poller.dedup {
		t.Fatal("targets do not share the poller-wide dedup cache")
	}
	f1.dedup.add("n1", testNow)
	f2.dedup.add("n2", testNow)
	f1.dedup.add("n3", testNow)

	poller.dedup.mu.Lock()
	count := len(poller.dedup.items)
	poller.dedup.mu.Unlock()
	if count != 2 {
		t.Fatalf("shared dedup size = %d, want 2", count)
	}
	if poller.dedup.contains("n1", testNow) {
		t.Fatal("oldest entry survived the shared bound")
	}
	if !poller.dedup.contains("n2", testNow) || !poller.dedup.contains("n3", testNow) {
		t.Fatal("newer entries were evicted from the shared cache")
	}
	if poller.dedup.contains("n2", testNow.Add(time.Hour)) {
		t.Fatal("expired dedup entry was still present")
	}
}

func TestDedupCacheIsSafeUnderConcurrentAccess(t *testing.T) {
	cache := newDedupCache(64, time.Hour)
	var workers sync.WaitGroup
	for i := 0; i < 256; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			id := "note-" + strconv.Itoa(i)
			cache.add(id, testNow)
			_ = cache.contains(id, testNow)
		}(i)
	}
	workers.Wait()

	cache.mu.Lock()
	count := len(cache.items)
	cache.mu.Unlock()
	if count > cache.limit {
		t.Fatalf("concurrent dedup size = %d, exceeds limit %d", count, cache.limit)
	}
}

func TestAPIErrorAfterSuccessfulPageKeepsPreciseCheckpoint(t *testing.T) {
	call := 0
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			call++
			if call == 1 {
				return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
			}
			if call == 2 {
				return nil, errTestAPI
			}
			return []domain.Note{}, nil
		},
	}
	var handled []string
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		handled = append(handled, note.ID)
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	if err := poller.processTarget(context.Background(), target); !errors.Is(err, errTestAPI) {
		t.Fatalf("API error = %v, want %v", err, errTestAPI)
	}
	if target.cursor != "n1" || !reflect.DeepEqual(handled, []string{"n1"}) {
		t.Fatalf("checkpoint after API error cursor=%q handled=%v", target.cursor, handled)
	}
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("empty retry returned error: %v", err)
	}
	if !reflect.DeepEqual(handled, []string{"n1"}) || target.cursor != "n1" {
		t.Fatalf("retry changed state cursor=%q handled=%v", target.cursor, handled)
	}
}

func TestMutualTargetSnapshotRequiresCompletePaginationAndSupportsRemovalReadd(t *testing.T) {
	client := &fakeClient{self: domain.User{ID: "bot", Username: "bot"}}
	inboundPage := 0
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		inboundPage++
		switch {
		case inboundPage == 1 && options.UntilID == "":
			return []domain.Following{testFollowing("r3", "f1", "bot"), testFollowing("r2", "bot", "bot")}, nil
		case inboundPage == 2 && options.UntilID == "r2":
			return []domain.Following{testFollowing("r1", "f2", "bot")}, nil
		case inboundPage == 3 && options.UntilID == "r1":
			return []domain.Following{}, nil
		default:
			return nil, errTestAPI
		}
	}
	outboundPage := 0
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		outboundPage++
		switch {
		case outboundPage == 1 && options.UntilID == "":
			return []domain.Following{testFollowing("o3", "bot", "f1"), testFollowing("o2", "bot", "bot")}, nil
		case outboundPage == 2 && options.UntilID == "o2":
			return []domain.Following{testFollowing("o1", "bot", "f2")}, nil
		case outboundPage == 3 && options.UntilID == "o1":
			return []domain.Following{}, nil
		default:
			return nil, errTestAPI
		}
	}
	poller := newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	poller.applyTargetSnapshot([]string{"old"}, testNow)
	if got, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("complete mutual sync returned error: %v", err)
	} else if !reflect.DeepEqual(got, []string{"f1", "f2"}) {
		t.Fatalf("mutual IDs = %v", got)
	}
	if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"f1", "f2"}) {
		t.Fatalf("snapshot IDs = %v", got)
	}
	oldF1 := targetFor(poller, "f1")

	client.followers = func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
		return nil, errTestAPI
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errTestAPI) {
		t.Fatalf("failed inbound sync error = %v, want %v", err, errTestAPI)
	}
	if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"f1", "f2"}) || targetFor(poller, "f1") != oldF1 {
		t.Fatalf("failed inbound sync replaced snapshot IDs=%v", got)
	}

	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("r4", "f1", "bot")}, nil
		}
		return []domain.Following{}, nil
	}
	client.following = func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
		return []domain.Following{}, nil
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("valid empty outbound sync returned error: %v", err)
	}
	if got := targetIDs(poller); len(got) != 0 {
		t.Fatalf("empty outbound snapshot IDs = %v", got)
	}

	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("o4", "bot", "f1")}, nil
		}
		return []domain.Following{}, nil
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("re-add sync returned error: %v", err)
	}
	newF1 := targetFor(poller, "f1")
	if newF1 == nil || newF1 == oldF1 || newF1.initialized {
		t.Fatalf("re-added target = %+v, old=%p", newF1, oldF1)
	}
}

func TestMutualTargetIntersectionExcludesUnilateralAndSelfWithDuplicatePages(t *testing.T) {
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		switch options.UntilID {
		case "":
			return []domain.Following{
				testFollowing("r5", "in-only", "bot"),
				testFollowing("r4", "mutual", "bot"),
				testFollowing("r4", "mutual", "bot"),
			}, nil
		case "r4":
			return []domain.Following{
				testFollowing("r3", "bot", "bot"),
				testFollowing("r2", "in-other", "bot"),
			}, nil
		case "r2":
			return []domain.Following{}, nil
		default:
			return nil, errTestAPI
		}
	}
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		switch options.UntilID {
		case "":
			return []domain.Following{
				testFollowing("o5", "bot", "out-only"),
				testFollowing("o4", "bot", "mutual"),
				testFollowing("o4", "bot", "mutual"),
			}, nil
		case "o4":
			return []domain.Following{
				testFollowing("o3", "bot", "bot"),
				testFollowing("o2", "bot", "out-other"),
			}, nil
		case "o2":
			return []domain.Following{}, nil
		default:
			return nil, errTestAPI
		}
	}
	poller := newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()

	got, err := poller.fetchAndApplyTargets(context.Background(), testNow)
	if err != nil {
		t.Fatalf("mutual sync returned error: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"mutual"}) || !reflect.DeepEqual(targetIDs(poller), []string{"mutual"}) {
		t.Fatalf("mutual intersection = %v, targets = %v", got, targetIDs(poller))
	}
}

func TestMutualTargetSnapshotPreservesPreviousTargetsWhenEitherDirectionFails(t *testing.T) {
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("r1", "mutual", "bot")}, nil
		}
		return []domain.Following{}, nil
	}
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("o1", "bot", "mutual")}, nil
		}
		return []domain.Following{}, nil
	}
	poller := newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("initial mutual sync returned error: %v", err)
	}
	previous := targetFor(poller, "mutual")

	client.followers = func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
		return nil, errTestAPI
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errTestAPI) {
		t.Fatalf("inbound failure = %v, want %v", err, errTestAPI)
	}
	if targetFor(poller, "mutual") != previous {
		t.Fatal("inbound failure replaced the previous target snapshot")
	}

	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("r2", "mutual", "bot")}, nil
		}
		return []domain.Following{}, nil
	}
	client.following = func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
		return nil, errTestAPI
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errTestAPI) {
		t.Fatalf("outbound failure = %v, want %v", err, errTestAPI)
	}
	if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"mutual"}) || targetFor(poller, "mutual") != previous {
		t.Fatalf("outbound failure replaced snapshot: IDs=%v target=%p previous=%p", got, targetFor(poller, "mutual"), previous)
	}
}

func TestMutualTargetSnapshotPreservesStateAfterPartialListFailure(t *testing.T) {
	for _, outbound := range []bool{false, true} {
		name := "inbound"
		if outbound {
			name = "outbound"
		}
		t.Run(name, func(t *testing.T) {
			client := &fakeClient{self: domain.User{ID: "bot"}}
			client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
				if options.UntilID == "" {
					return []domain.Following{testFollowing("r1", "new", "bot")}, nil
				}
				if !outbound {
					return nil, errTestAPI
				}
				return []domain.Following{}, nil
			}
			client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
				if options.UntilID == "" {
					return []domain.Following{testFollowing("o1", "bot", "new")}, nil
				}
				return nil, errTestAPI
			}
			poller := newTestPoller(t, client, nil)
			old := addTarget(t, poller, "old")
			old.initialized = true
			old.cursor = "checkpoint"
			if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errTestAPI) {
				t.Fatalf("partial sync error = %v", err)
			}
			if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"old"}) || targetFor(poller, "old") != old || old.cursor != "checkpoint" {
				t.Fatalf("partial sync changed prior targets or checkpoint: %v", got)
			}
		})
	}
}

func TestMutualTargetSnapshotRejectsMalformedOutboundRelationships(t *testing.T) {
	tests := []struct {
		name         string
		relationship domain.Following
	}{
		{
			name:         "wrong follower direction",
			relationship: testFollowing("o1", "other", "mutual"),
		},
		{
			name: "embedded followee mismatch",
			relationship: domain.Following{
				ID:         "o1",
				FollowerID: "bot",
				FolloweeID: "mutual",
				Followee:   &domain.User{ID: "other"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeClient{
				self: domain.User{ID: "bot"},
				followers: func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
					return []domain.Following{}, nil
				},
				following: func(_ string, options domain.PageOptions) ([]domain.Following, error) {
					if options.UntilID == "" {
						return []domain.Following{test.relationship}, nil
					}
					return []domain.Following{}, nil
				},
			}
			poller := newTestPoller(t, client, nil)
			poller.stateMu.Lock()
			poller.selfID = "bot"
			poller.stateMu.Unlock()
			poller.applyTargetSnapshot([]string{"old"}, testNow)

			if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errInvalidResponse) {
				t.Fatalf("malformed outbound error = %v, want %v", err, errInvalidResponse)
			}
			if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"old"}) {
				t.Fatalf("malformed outbound changed targets = %v", got)
			}
		})
	}
}

func TestMutualTargetLossAndReadditionOnEitherSideResetsBaseline(t *testing.T) {
	inboundPresent := true
	outboundPresent := true
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if !inboundPresent || options.UntilID != "" {
			return []domain.Following{}, nil
		}
		return []domain.Following{testFollowing("r1", "mutual", "bot")}, nil
	}
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if !outboundPresent || options.UntilID != "" {
			return []domain.Following{}, nil
		}
		return []domain.Following{testFollowing("o1", "bot", "mutual")}, nil
	}
	poller := newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("initial sync returned error: %v", err)
	}
	first := targetFor(poller, "mutual")
	if first == nil {
		t.Fatal("initial mutual target was not created")
	}

	inboundPresent = false
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("inbound loss sync returned error: %v", err)
	}
	if targetFor(poller, "mutual") != nil {
		t.Fatal("inbound loss kept the target")
	}
	inboundPresent = true
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("inbound re-add sync returned error: %v", err)
	}
	second := targetFor(poller, "mutual")
	if second == nil || second == first || second.initialized {
		t.Fatalf("inbound re-add target = %+v, old=%p", second, first)
	}

	second.initialized = true
	outboundPresent = false
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("outbound loss sync returned error: %v", err)
	}
	if targetFor(poller, "mutual") != nil {
		t.Fatal("outbound loss kept the target")
	}
	outboundPresent = true
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); err != nil {
		t.Fatalf("outbound re-add sync returned error: %v", err)
	}
	third := targetFor(poller, "mutual")
	if third == nil || third == second || third.initialized {
		t.Fatalf("outbound re-add target = %+v, old=%p", third, second)
	}
}

func TestFollowerMalformedOrStuckPaginationPreservesSnapshot(t *testing.T) {
	client := &fakeClient{self: domain.User{ID: "bot", Username: "bot"}}
	poller := newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	poller.applyTargetSnapshot([]string{"old"}, testNow)

	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("r2", "new", "bot")}, nil
		}
		return []domain.Following{testFollowing("r2", "new", "bot")}, nil
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errRelationshipSyncIncomplete) {
		t.Fatalf("stuck sync error = %v, want %v", err, errRelationshipSyncIncomplete)
	}
	if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("stuck sync changed snapshot = %v", got)
	}

	client.followers = func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
		return []domain.Following{{ID: "r1", FollowerID: "new", FolloweeID: "other"}}, nil
	}
	if _, err := poller.fetchAndApplyTargets(context.Background(), testNow); !errors.Is(err, errInvalidResponse) {
		t.Fatalf("malformed sync error = %v, want %v", err, errInvalidResponse)
	}
	if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"old"}) {
		t.Fatalf("malformed sync changed snapshot = %v", got)
	}
}

func TestRunRetriesFollowerRequestDeadlineWithoutReplacingSnapshot(t *testing.T) {
	requestErr := domain.NewContextError(domain.ErrorKindNetworkTimeout, context.DeadlineExceeded)
	call := 0
	var poller *Poller
	var cancelRun context.CancelFunc
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, _ domain.PageOptions) ([]domain.Following, error) {
		call++
		if call == 1 {
			if got := targetIDs(poller); !reflect.DeepEqual(got, []string{"old"}) {
				t.Errorf("snapshot during failed sync = %v, want [old]", got)
			}
			return nil, requestErr
		}
		cancelRun()
		return []domain.Following{}, nil
	}

	poller = newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	poller.applyTargetSnapshot([]string{"old"}, testNow)
	poller.settings.BackoffBase = time.Second
	poller.settings.BackoffMax = time.Minute

	var clockMu sync.Mutex
	now := testNow
	poller.settings.Clock = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancelRun = cancel
	defer cancel()
	poller.settings.Sleep = func(ctx context.Context, delay time.Duration) error {
		clockMu.Lock()
		now = now.Add(delay)
		clockMu.Unlock()
		return ctx.Err()
	}

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if call != 2 {
		t.Fatalf("follower sync calls = %d, want timeout retry followed by success", call)
	}
}

func TestRateLimitCooldownAndRetryAfterBackoffAreShared(t *testing.T) {
	limiter := &recordingLimiter{}
	client := &fakeClient{self: domain.User{ID: "bot", Username: "bot"}}
	poller := newTestPoller(t, client, nil)
	poller.limiter = limiter
	retryAfter := 7 * time.Second
	apiErr := domain.NewError(domain.ErrorKindRateLimit, 429, "RATE_LIMIT", &retryAfter)
	if err := poller.read(context.Background(), func(context.Context) error { return apiErr }); !errors.Is(err, apiErr) {
		t.Fatalf("read error = %v, want %v", err, apiErr)
	}
	if len(limiter.cooldowns) != 1 || !limiter.cooldowns[0].Equal(testNow.Add(retryAfter)) {
		t.Fatalf("cooldowns = %v", limiter.cooldowns)
	}

	poller.applyTargetSnapshot([]string{"follower"}, testNow)
	target := targetFor(poller, "follower")
	target.inFlight = true
	poller.completeTargetJob(target, apiErr, testNow)
	if target.nextAt != testNow.Add(retryAfter) {
		t.Fatalf("rate-limit nextAt = %s, want %s", target.nextAt, testNow.Add(retryAfter))
	}
	if target.backoff != poller.settings.BackoffBase {
		t.Fatalf("rate-limit backoff = %s", target.backoff)
	}
}

func TestServerErrorsUseExponentialPerTargetBackoff(t *testing.T) {
	poller := newTestPoller(t, &fakeClient{self: domain.User{ID: "bot", Username: "bot"}}, nil)
	poller.applyTargetSnapshot([]string{"follower"}, testNow)
	target := targetFor(poller, "follower")
	target.inFlight = true
	serverErr := domain.NewError(domain.ErrorKindServer, 503, "TEMPORARY", nil)
	poller.completeTargetJob(target, serverErr, testNow)
	if target.backoff != poller.settings.BackoffBase || target.nextAt != testNow.Add(poller.settings.BackoffBase) {
		t.Fatalf("first backoff=%s nextAt=%s", target.backoff, target.nextAt)
	}
	target.inFlight = true
	poller.completeTargetJob(target, serverErr, testNow.Add(poller.settings.BackoffBase))
	if target.backoff != 2*poller.settings.BackoffBase {
		t.Fatalf("second backoff=%s", target.backoff)
	}
}

func TestSameTargetProcessingIsSerializedAndRemovalInvalidatesGeneration(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var active int
	var maxActive int
	var mu sync.Mutex
	var startedOnce sync.Once
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			if options.SinceID == "" {
				return []domain.Note{testNote("n0", "follower", "public", testNow)}, nil
			}
			return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
		},
	}
	poller := newTestPoller(t, client, NoteHandlerFunc(func(_ context.Context, note domain.Note) error {
		if note.ID != "n1" {
			return nil
		}
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		startedOnce.Do(func() { close(started) })
		<-release
		mu.Lock()
		active--
		mu.Unlock()
		return nil
	}))
	target := addTarget(t, poller, "follower")
	if err := poller.processTarget(context.Background(), target); err != nil {
		t.Fatalf("bootstrap returned error: %v", err)
	}
	firstDone := make(chan error, 1)
	secondDone := make(chan error, 1)
	go func() { firstDone <- poller.processTarget(context.Background(), target) }()
	<-started
	go func() { secondDone <- poller.processTarget(context.Background(), target) }()
	select {
	case <-secondDone:
		t.Fatal("same-target process completed while first handler was blocked")
	case <-time.After(20 * time.Millisecond):
	}

	removed := make(chan struct{})
	go func() {
		poller.applyTargetSnapshot(nil, testNow)
		close(removed)
	}()
	select {
	case <-removed:
		t.Fatal("removal completed while target handler was blocked")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; !errors.Is(err, errTargetGone) {
		t.Fatalf("first process error = %v, want %v", err, errTargetGone)
	}
	if err := <-secondDone; !errors.Is(err, errTargetGone) {
		t.Fatalf("second process error = %v, want %v", err, errTargetGone)
	}
	<-removed
	mu.Lock()
	gotMaxActive := maxActive
	mu.Unlock()
	if gotMaxActive != 1 {
		t.Fatalf("same-target maximum concurrent handlers = %d, want 1", gotMaxActive)
	}
}

func TestRunSchedulesTargetsFairlyAndStopsOnCancellation(t *testing.T) {
	client := &fakeClient{
		self: domain.User{ID: "bot", Username: "bot"},
		followers: func(_ string, options domain.PageOptions) ([]domain.Following, error) {
			if options.UntilID == "" {
				return []domain.Following{testFollowing("r2", "f2", "bot"), testFollowing("r1", "f1", "bot")}, nil
			}
			return []domain.Following{}, nil
		},
		following: func(_ string, options domain.PageOptions) ([]domain.Following, error) {
			if options.UntilID == "" {
				return []domain.Following{testFollowing("o2", "bot", "f2"), testFollowing("o1", "bot", "f1")}, nil
			}
			return []domain.Following{}, nil
		},
		notes: func(_ string, _ domain.NotePageOptions) ([]domain.Note, error) {
			return []domain.Note{}, nil
		},
	}
	poller := newTestPoller(t, client, nil)
	poller.settings.Concurrency = 1
	poller.settings.PollInterval = time.Hour
	poller.settings.FollowerSyncInterval = time.Hour
	poller.settings.StartupSpread = 0
	ctx, cancel := context.WithCancel(context.Background())
	poller.settings.Sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := client.noteCallCount(); got != 2 {
		t.Fatalf("target bootstrap calls = %d, want 2", got)
	}
}

func TestRunOnlyPollsMutualTargets(t *testing.T) {
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{
				testFollowing("r2", "in-only", "bot"),
				testFollowing("r1", "mutual", "bot"),
			}, nil
		}
		return []domain.Following{}, nil
	}
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{
				testFollowing("o2", "bot", "out-only"),
				testFollowing("o1", "bot", "mutual"),
			}, nil
		}
		return []domain.Following{}, nil
	}
	var polled []string
	var polledMu sync.Mutex
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client.notes = func(userID string, _ domain.NotePageOptions) ([]domain.Note, error) {
		polledMu.Lock()
		polled = append(polled, userID)
		polledMu.Unlock()
		if userID == "mutual" {
			cancel()
		}
		return []domain.Note{}, nil
	}
	poller := newTestPoller(t, client, nil)
	poller.settings.Concurrency = 1
	poller.settings.PollInterval = time.Hour
	poller.settings.FollowerSyncInterval = time.Hour
	poller.settings.StartupSpread = 0

	if err := poller.Run(ctx); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	polledMu.Lock()
	got := append([]string(nil), polled...)
	polledMu.Unlock()
	if !reflect.DeepEqual(got, []string{"mutual"}) {
		t.Fatalf("polled users = %v, want only mutual target", got)
	}
}

func TestRunWakesSpareWorkerForNewlyDueTarget(t *testing.T) {
	var slowTargetID, fastTargetID string
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID != "" {
			return []domain.Following{}, nil
		}
		return []domain.Following{
			testFollowing("r-slow", slowTargetID, "bot"),
			testFollowing("r-fast", fastTargetID, "bot"),
		}, nil
	}
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID != "" {
			return []domain.Following{}, nil
		}
		return []domain.Following{
			testFollowing("o-slow", "bot", slowTargetID),
			testFollowing("o-fast", "bot", fastTargetID),
		}, nil
	}

	poller := newTestPoller(t, client, nil)
	poller.settings.Concurrency = 2
	poller.settings.PollInterval = time.Hour
	poller.settings.FollowerSyncInterval = time.Hour
	poller.settings.StartupSpread = 500 * time.Millisecond
	poller.settings.Clock = time.Now
	poller.settings.Sleep = sleepContext

	for i := 0; i < 100_000 && (slowTargetID == "" || fastTargetID == ""); i++ {
		id := "follower-" + strconv.Itoa(i)
		offset := poller.startupOffset(id)
		if slowTargetID == "" && offset < 10*time.Millisecond {
			slowTargetID = id
		}
		if fastTargetID == "" && offset >= 100*time.Millisecond && offset < 250*time.Millisecond {
			fastTargetID = id
		}
	}
	if slowTargetID == "" || fastTargetID == "" {
		t.Fatalf("could not find deterministic startup offsets: slow=%q fast=%q", slowTargetID, fastTargetID)
	}

	slowEntered := make(chan struct{})
	fastStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var slowOnce, fastOnce, releaseOnce sync.Once
	client.notes = func(userID string, _ domain.NotePageOptions) ([]domain.Note, error) {
		switch userID {
		case slowTargetID:
			slowOnce.Do(func() { close(slowEntered) })
			<-releaseSlow
		case fastTargetID:
			fastOnce.Do(func() { close(fastStarted) })
		}
		return []domain.Note{}, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		releaseOnce.Do(func() { close(releaseSlow) })
		cancel()
	}()
	runDone := make(chan error, 1)
	go func() { runDone <- poller.Run(ctx) }()

	select {
	case <-slowEntered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow target did not occupy a worker")
	}
	select {
	case <-fastStarted:
		// The slow callback is still blocked here. Seeing the fast callback
		// proves the scheduler woke for due work instead of waiting for the
		// slow result.
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not wake for the newly due target")
	}
	releaseOnce.Do(func() { close(releaseSlow) })
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}

func TestRunCanBeStartedAgainAfterCancellation(t *testing.T) {
	var notesMu sync.Mutex
	blockFirstNote := true
	firstEntered := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var firstOnce, secondOnce, releaseFirstOnce sync.Once
	var cancelSecond context.CancelFunc
	client := &fakeClient{
		self: domain.User{ID: "bot"},
		followers: func(_ string, options domain.PageOptions) ([]domain.Following, error) {
			if options.UntilID != "" {
				return []domain.Following{}, nil
			}
			return []domain.Following{testFollowing("r1", "follower", "bot")}, nil
		},
		following: func(_ string, options domain.PageOptions) ([]domain.Following, error) {
			if options.UntilID != "" {
				return []domain.Following{}, nil
			}
			return []domain.Following{testFollowing("o1", "bot", "follower")}, nil
		},
	}
	client.notes = func(_ string, _ domain.NotePageOptions) ([]domain.Note, error) {
		notesMu.Lock()
		block := blockFirstNote
		if block {
			blockFirstNote = false
		}
		notesMu.Unlock()
		if block {
			firstOnce.Do(func() { close(firstEntered) })
			<-releaseFirst
			return nil, context.Canceled
		}
		secondOnce.Do(func() { close(secondStarted) })
		if cancelSecond != nil {
			cancelSecond()
		}
		return []domain.Note{}, nil
	}

	poller := newTestPoller(t, client, nil)
	poller.settings.Concurrency = 1
	poller.settings.StartupSpread = 0
	poller.settings.PollInterval = time.Hour
	poller.settings.FollowerSyncInterval = time.Hour

	ctx1, cancel1 := context.WithCancel(context.Background())
	run1Done := make(chan error, 1)
	go func() { run1Done <- poller.Run(ctx1) }()
	select {
	case <-firstEntered:
	case <-time.After(time.Second):
		cancel1()
		t.Fatal("first Run did not start target processing")
	}
	cancel1()
	releaseFirstOnce.Do(func() { close(releaseFirst) })
	select {
	case err := <-run1Done:
		if err != nil {
			t.Fatalf("first Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first Run did not stop after cancellation")
	}

	if target := targetFor(poller, "follower"); target == nil || target.inFlight {
		t.Fatalf("stale scheduling state after first Run: %+v", target)
	}

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	cancelSecond = cancel2
	run2Done := make(chan error, 1)
	go func() { run2Done <- poller.Run(ctx2) }()
	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second Run did not schedule the existing target")
	}
	select {
	case err := <-run2Done:
		if err != nil {
			t.Fatalf("second Run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Run did not stop after cancellation")
	}
}

func TestObservationHandlerDoesNotExposeNoteText(t *testing.T) {
	var logLines []string
	logger := testLogger(&logLines)
	handler := ObservationHandler{Logger: logger}
	text := "private note content that must not be logged"
	if err := handler.HandleNote(context.Background(), domain.Note{ID: "n1", UserID: "f1", Text: &text}); err != nil {
		t.Fatalf("HandleNote returned error: %v", err)
	}
	if len(logLines) != 1 || !strings.Contains(logLines[0], "n1") {
		t.Fatalf("observation log = %q", logLines)
	}
	if strings.Contains(logLines[0], text) {
		t.Fatalf("observation log contains note text: %q", logLines[0])
	}
}

func TestFetchSummarySeparatesSuccessFailureIdleAndCancellation(t *testing.T) {
	summary := newFetchSummary()
	summary.begin(fetchOperationSelf)
	summary.finish(fetchOperationSelf, nil, context.Background())
	summary.begin(fetchOperationRelationshipSync)
	summary.finish(fetchOperationRelationshipSync, errTestAPI, context.Background())

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	summary.begin(fetchOperationTargetNotes)
	summary.finish(fetchOperationTargetNotes, context.Canceled, canceled)
	summary.begin(fetchOperationTargetNotes)
	summary.finish(fetchOperationTargetNotes, errTargetGone, context.Background())

	snapshot := summary.take()
	if snapshot.outcome() != "partial_failure" || snapshot.attempts() != 2 || snapshot.failures() != 1 || snapshot.inFlight() != 0 {
		t.Fatalf("summary outcome=%q attempts=%d failures=%d in-flight=%d", snapshot.outcome(), snapshot.attempts(), snapshot.failures(), snapshot.inFlight())
	}
	if got := snapshot.operations[fetchOperationSelf]; got.attempts() != 1 || got.successes != 1 || got.failures != 0 {
		t.Fatalf("self counts = %+v", got)
	}
	if got := snapshot.operations[fetchOperationRelationshipSync]; got.attempts() != 1 || got.successes != 0 || got.failures != 1 {
		t.Fatalf("relationship counts = %+v", got)
	}
	if got := snapshot.operations[fetchOperationTargetNotes]; got.attempts() != 0 || got.inFlight != 0 {
		t.Fatalf("canceled/removed target counts = %+v", got)
	}
	if got := summary.take(); got.outcome() != "idle" || got.attempts() != 0 {
		t.Fatalf("empty next window outcome=%q attempts=%d, want idle", got.outcome(), got.attempts())
	}
}

func TestFetchSummaryReportsInFlightInsteadOfFalseSuccess(t *testing.T) {
	summary := newFetchSummary()
	summary.begin(fetchOperationTargetNotes)
	snapshot := summary.take()
	if snapshot.outcome() != "in_progress" || snapshot.attempts() != 0 || snapshot.inFlight() != 1 {
		t.Fatalf("in-progress summary outcome=%q attempts=%d in-flight=%d", snapshot.outcome(), snapshot.attempts(), snapshot.inFlight())
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	summary.finish(fetchOperationTargetNotes, context.Canceled, canceled)
	if got := summary.take(); got.outcome() != "idle" || got.inFlight() != 0 {
		t.Fatalf("canceled window outcome=%q in-flight=%d, want idle and no failures", got.outcome(), got.inFlight())
	}
}

func TestTargetNoteTurnCountsMultiplePagesOnce(t *testing.T) {
	client := &fakeClient{
		self: domain.User{ID: "bot"},
		notes: func(_ string, options domain.NotePageOptions) ([]domain.Note, error) {
			switch options.SinceID {
			case "n0":
				return []domain.Note{testNote("n1", "follower", "public", testNow)}, nil
			case "n1":
				return []domain.Note{testNote("n2", "follower", "public", testNow)}, nil
			case "n2":
				return []domain.Note{}, nil
			default:
				return nil, errors.New("unexpected cursor")
			}
		},
	}
	poller := newTestPoller(t, client, nil)
	target := addTarget(t, poller, "follower")
	target.initialized = true
	target.cursor = "n0"
	summary := newFetchSummary()

	if err := poller.runTargetTurn(context.Background(), target, summary); err != nil {
		t.Fatalf("runTargetTurn returned error: %v", err)
	}
	if got := client.noteCallCount(); got != 3 {
		t.Fatalf("note page requests = %d, want 3", got)
	}
	counts := summary.take().operations[fetchOperationTargetNotes]
	if counts.attempts() != 1 || counts.successes != 1 || counts.failures != 0 {
		t.Fatalf("target turn counts = %+v, want one successful turn", counts)
	}
}

func TestRelationshipSyncCountsCompletePaginatedAttemptOnce(t *testing.T) {
	client := &fakeClient{self: domain.User{ID: "bot"}}
	client.followers = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("r2", "f2", "bot"), testFollowing("r1", "f1", "bot")}, nil
		}
		return []domain.Following{}, nil
	}
	client.following = func(_ string, options domain.PageOptions) ([]domain.Following, error) {
		if options.UntilID == "" {
			return []domain.Following{testFollowing("o2", "bot", "f2"), testFollowing("o1", "bot", "f1")}, nil
		}
		return []domain.Following{}, nil
	}
	poller := newTestPoller(t, client, nil)
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	summary := newFetchSummary()

	if _, err := poller.syncTargets(context.Background(), testNow, summary); err != nil {
		t.Fatalf("syncTargets returned error: %v", err)
	}
	counts := summary.take().operations[fetchOperationRelationshipSync]
	if counts.attempts() != 1 || counts.successes != 1 || counts.failures != 0 {
		t.Fatalf("relationship sync counts = %+v, want one successful sync", counts)
	}
}

func TestFetchSummaryLoggingUsesDebugLevelAndIncludesIdleOutcome(t *testing.T) {
	for _, test := range []struct {
		name      string
		level     slog.Level
		wantEvent bool
	}{
		{name: "info suppresses", level: slog.LevelInfo},
		{name: "debug emits", level: slog.LevelDebug, wantEvent: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var lines []string
			logger := slog.New(slog.NewTextHandler(&lineWriter{lines: &lines}, &slog.HandlerOptions{Level: test.level}))
			poller := &Poller{logger: logger, settings: DefaultSettings()}
			poller.logFetchSummary(fetchSummarySnapshot{}, false)
			if (len(lines) != 0) != test.wantEvent {
				t.Fatalf("summary lines = %q, want event=%t", lines, test.wantEvent)
			}
			if test.wantEvent && (!strings.Contains(lines[0], "outcome=idle") || !strings.Contains(lines[0], "attempts=0") || strings.Contains(lines[0], "target_id")) {
				t.Fatalf("debug summary = %q", lines[0])
			}
		})
	}
}

func TestRunSummaryFlushesFatalFailureAndIsLifecycleScoped(t *testing.T) {
	authErr := domain.NewError(domain.ErrorKindAuth, 401, "UNAUTHORIZED", nil)
	client := &fakeClient{
		self:       domain.User{ID: "bot"},
		selfErrors: []error{authErr},
	}
	var lines []string
	logger := slog.New(slog.NewTextHandler(&lineWriter{lines: &lines}, &slog.HandlerOptions{Level: slog.LevelDebug}))
	poller := newTestPoller(t, client, nil)
	poller.logger = logger

	if err := poller.Run(context.Background()); !errors.Is(err, authErr) {
		t.Fatalf("first Run error = %v, want %v", err, authErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	poller.settings.Sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	if err := poller.Run(ctx); err != nil {
		t.Fatalf("second Run error = %v", err)
	}
	cancel()

	if len(lines) != 2 {
		t.Fatalf("summary lines = %q, want one final summary per lifecycle", lines)
	}
	if !strings.Contains(lines[0], "outcome=failure") || !strings.Contains(lines[0], "self_attempts=1") || !strings.Contains(lines[0], "self_failures=1") {
		t.Fatalf("fatal lifecycle summary = %q", lines[0])
	}
	if !strings.Contains(lines[1], "outcome=success") || !strings.Contains(lines[1], "self_attempts=1") || !strings.Contains(lines[1], "self_successes=1") || !strings.Contains(lines[1], "relationship_sync_attempts=1") || !strings.Contains(lines[1], "relationship_sync_successes=1") || strings.Contains(lines[1], "self_failures=1") {
		t.Fatalf("restarted lifecycle summary = %q", lines[1])
	}
}

type fakeClient struct {
	mu         sync.Mutex
	self       domain.User
	selfErrors []error
	selfCalls  int
	followers  func(string, domain.PageOptions) ([]domain.Following, error)
	following  func(string, domain.PageOptions) ([]domain.Following, error)
	notes      func(string, domain.NotePageOptions) ([]domain.Note, error)
	lastNotes  []domain.NotePageOptions
}

func (c *fakeClient) Self(ctx context.Context) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	c.mu.Lock()
	call := c.selfCalls
	c.selfCalls++
	self := c.self
	var selfErr error
	if call < len(c.selfErrors) {
		selfErr = c.selfErrors[call]
	}
	c.mu.Unlock()
	if selfErr != nil {
		return domain.User{}, selfErr
	}
	return self, nil
}

func (c *fakeClient) ListFollowers(ctx context.Context, userID string, options domain.PageOptions) ([]domain.Following, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.followers == nil {
		return []domain.Following{}, nil
	}
	return c.followers(userID, options)
}

func (c *fakeClient) ListFollowing(ctx context.Context, userID string, options domain.PageOptions) ([]domain.Following, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.following == nil {
		return []domain.Following{}, nil
	}
	return c.following(userID, options)
}

func (c *fakeClient) ListUserNotes(ctx context.Context, userID string, options domain.NotePageOptions) ([]domain.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.lastNotes = append(c.lastNotes, options)
	c.mu.Unlock()
	if c.notes == nil {
		return []domain.Note{}, nil
	}
	return c.notes(userID, options)
}

func (c *fakeClient) noteCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lastNotes)
}

func (c *fakeClient) selfCallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.selfCalls
}

type recordingLimiter struct {
	mu        sync.Mutex
	waits     int
	cooldowns []time.Time
}

func (l *recordingLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	l.waits++
	l.mu.Unlock()
	return nil
}

func (l *recordingLimiter) SetCooldown(until time.Time) {
	l.mu.Lock()
	l.cooldowns = append(l.cooldowns, until)
	l.mu.Unlock()
}

func newTestPoller(t *testing.T, client Client, handler NoteHandler) *Poller {
	t.Helper()
	if handler == nil {
		handler = NoteHandlerFunc(func(context.Context, domain.Note) error { return nil })
	}
	settings := DefaultSettings()
	settings.PollInterval = time.Hour
	settings.FollowerSyncInterval = time.Hour
	settings.Concurrency = 2
	settings.PageLimit = 3
	settings.MaxPagesPerTurn = 5
	settings.StartupSpread = 0
	settings.BackoffBase = time.Second
	settings.BackoffMax = time.Minute
	settings.Clock = func() time.Time { return testNow }
	settings.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	settings.Jitter = func(delay time.Duration) time.Duration { return delay }
	settings.RateLimiter = &recordingLimiter{}
	poller, err := New(client, handler, WithSettings(settings))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return poller
}

func addTarget(t *testing.T, poller *Poller, id string) *targetState {
	t.Helper()
	poller.stateMu.Lock()
	poller.selfID = "bot"
	poller.stateMu.Unlock()
	poller.applyTargetSnapshot([]string{id}, testNow)
	return targetFor(poller, id)
}

func targetFor(poller *Poller, id string) *targetState {
	poller.stateMu.Lock()
	defer poller.stateMu.Unlock()
	return poller.targets[id]
}

func targetIDs(poller *Poller) []string {
	poller.stateMu.Lock()
	ids := make([]string, 0, len(poller.targets))
	for id := range poller.targets {
		ids = append(ids, id)
	}
	poller.stateMu.Unlock()
	sort.Strings(ids)
	return ids
}

func testNote(id, userID, visibility string, createdAt time.Time) domain.Note {
	return domain.Note{ID: id, UserID: userID, Visibility: visibility, CreatedAt: createdAt}
}

func testFollowing(id, followerID, followeeID string) domain.Following {
	return domain.Following{ID: id, FollowerID: followerID, FolloweeID: followeeID}
}

func testLogger(lines *[]string) *slog.Logger {
	return slog.New(slog.NewTextHandler(&lineWriter{lines: lines}, nil))
}

type lineWriter struct {
	mu    sync.Mutex
	lines *[]string
}

func (w *lineWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	*w.lines = append(*w.lines, string(data))
	return len(data), nil
}

func mustBaseURL(t *testing.T) *url.URL {
	t.Helper()
	u, err := url.Parse("https://misskey.example.test")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	return u
}
