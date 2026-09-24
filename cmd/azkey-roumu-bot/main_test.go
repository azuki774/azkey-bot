package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
	"github.com/azuki774/azkey-bot/internal/roumu/polling"
)

type logCapture struct {
	mu          sync.Mutex
	output      bytes.Buffer
	started     chan struct{}
	stopped     chan struct{}
	summary     chan struct{}
	startedOnce sync.Once
	stoppedOnce sync.Once
	summaryOnce sync.Once
}

func newLogCapture() *logCapture {
	return &logCapture{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
		summary: make(chan struct{}),
	}
}

func (c *logCapture) Write(data []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, _ = c.output.Write(data)
	rendered := c.output.String()
	if strings.Contains(rendered, `msg="azkey-roumu-bot started"`) {
		c.startedOnce.Do(func() { close(c.started) })
	}
	if strings.Contains(rendered, `msg="azkey-roumu-bot stopped"`) {
		c.stoppedOnce.Do(func() { close(c.stopped) })
	}
	// Initial reads may finish in different summary windows.
	if strings.Contains(rendered, `msg="polling fetch summary"`) && strings.Contains(rendered, "relationship_sync_successes=1") {
		c.summaryOnce.Do(func() { close(c.summary) })
	}
	return len(data), nil
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func TestRunStartsAndStopsOnContextCancellationWithEnvironmentOnlyConfig(t *testing.T) {
	secret := "run-lifecycle-test-secret"
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    secret,
	}
	capture := newLogCapture()
	logger := slog.New(slog.NewTextHandler(capture, nil))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)

	go func() {
		done <- runWithClientFactory(ctx, mainMapLookup(env), logger, func(*url.URL, string) (polling.Client, error) {
			return mainTestClient{}, nil
		})
	}()

	select {
	case <-capture.started:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not report startup")
	}
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after cancellation")
	}

	select {
	case <-capture.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not render stopped log")
	}
	rendered := capture.String()
	if strings.Contains(rendered, secret) {
		t.Fatalf("rendered log contains token: %q", rendered)
	}
}

func TestObserveModePerformsFollowAndUnfollowWritesAtStartup(t *testing.T) {
	client := newMainObserveClient()
	env := map[string]string{
		"MISSKEY_BASE_URL":       "https://misskey.example.test",
		"MISSKEY_TOKEN":          "observe-mode-test-token",
		"POLLING_MODE":           "observe",
		"POLL_RATE_PER_SECOND":   "1000",
		"POLL_STARTUP_SPREAD":    "0s",
		"FOLLOW_WRITE_INTERVAL":  "1ms",
		"POLL_BACKOFF_BASE":      "1ms",
		"POLL_BACKOFF_MAX":       "1s",
		"POLL_INTERVAL":          "1h",
		"FOLLOWER_SYNC_INTERVAL": "1h",
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() {
		runDone <- runWithClientFactory(ctx, mainMapLookup(env), nil, func(*url.URL, string) (polling.Client, error) {
			return client, nil
		})
	}()

	gotFollow := false
	gotUnfollow := false
	deadline := time.After(5 * time.Second)
	for !gotFollow || !gotUnfollow {
		select {
		case <-client.followCalls:
			gotFollow = true
		case <-client.unfollowCalls:
			gotUnfollow = true
		case err := <-runDone:
			if err != nil {
				t.Fatalf("runWithClientFactory returned before relationship writes: %v", err)
			}
			t.Fatal("runWithClientFactory stopped before both relationship writes")
		case <-deadline:
			t.Fatalf("observe-mode startup did not perform both writes: follow=%t unfollow=%t", gotFollow, gotUnfollow)
		}
	}
	cancel()
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("runWithClientFactory returned error after cancellation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runWithClientFactory did not stop after cancellation")
	}
	if got := client.relationshipWrites(); !reflect.DeepEqual(got, []string{"follow:follower", "unfollow:followee"}) && !reflect.DeepEqual(got, []string{"unfollow:followee", "follow:follower"}) {
		t.Fatalf("relationship writes = %v, want one inbound follow and one outbound unfollow", got)
	}
}

func TestRunRejectsInvalidConfigBeforeStartup(t *testing.T) {
	secret := "invalid-config-test-secret"
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    secret,
		"POLL_INTERVAL":    "not-a-duration",
	}
	capture := newLogCapture()
	err := run(context.Background(), mainMapLookup(env), slog.New(slog.NewTextHandler(capture, nil)))
	if err == nil {
		t.Fatal("run returned nil error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error contains sensitive input: %q", err)
	}
	if rendered := capture.String(); rendered != "" {
		t.Fatalf("invalid configuration emitted log %q", rendered)
	}
}

func TestCLIHandlesSIGTERMAndInvalidConfig(t *testing.T) {
	binary := buildCLI(t)
	secret := "cli-process-test-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/i":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "bot-id", "username": "bot", "name": nil, "host": nil})
		case "/api/users/followers":
			_ = json.NewEncoder(w).Encode([]any{})
		case "/api/users/following":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	validEnv := map[string]string{
		"MISSKEY_BASE_URL": server.URL,
		"MISSKEY_TOKEN":    secret,
		"LOG_LEVEL":        "DEBUG",
		"POLL_INTERVAL":    "10ms",
	}

	cmd := exec.Command(binary)
	cmd.Env = commandEnvironment(validEnv)
	capture := newLogCapture()
	cmd.Stderr = capture
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	processStarted := false
	waitStarted := false
	waitCompleted := false
	var wait chan error
	defer func() {
		if !processStarted || waitCompleted {
			return
		}
		if waitStarted {
			_ = cmd.Process.Kill()
			<-wait
			return
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()
	if err := cmd.Start(); err != nil {
		t.Fatalf("start CLI: %v", err)
	}
	processStarted = true
	select {
	case <-capture.started:
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not report startup")
	}
	select {
	case <-capture.summary:
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not emit a debug polling summary")
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	wait = make(chan error, 1)
	waitStarted = true
	go func() {
		wait <- cmd.Wait()
	}()
	select {
	case err := <-wait:
		waitCompleted = true
		if err != nil {
			t.Fatalf("CLI did not exit successfully after SIGTERM: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not stop after SIGTERM")
	}
	select {
	case <-capture.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("CLI did not render stopped log")
	}
	outputText := capture.String() + stdout.String()
	if strings.Contains(outputText, secret) {
		t.Fatalf("captured CLI output contains token: %q", outputText)
	}
	if !strings.Contains(outputText, `msg="polling fetch summary"`) || !strings.Contains(outputText, "self_successes=1") || !strings.Contains(outputText, "relationship_sync_successes=1") {
		t.Fatalf("debug logging did not include the completed fetch summary: %q", outputText)
	}

	invalidSecret := "cli-invalid-config-secret"
	invalidEnv := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    invalidSecret,
		"POLL_INTERVAL":    "not-a-duration",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	invalid := exec.CommandContext(ctx, binary)
	invalid.Env = commandEnvironment(invalidEnv)
	invalidCapture := newLogCapture()
	invalid.Stderr = invalidCapture
	var invalidStdout bytes.Buffer
	invalid.Stdout = &invalidStdout
	err := invalid.Run()
	if ctx.Err() != nil {
		t.Fatal("CLI did not reject invalid configuration promptly")
	}
	if err == nil {
		t.Fatal("CLI returned success for invalid configuration")
	}
	invalidOutput := invalidCapture.String() + invalidStdout.String()
	if strings.Contains(invalidOutput, invalidSecret) || strings.Contains(invalidOutput, "azkey-roumu-bot started") {
		t.Fatalf("invalid-config output leaked data or startup log: %q", invalidOutput)
	}
}

type mainTestClient struct{}

func (mainTestClient) Self(context.Context) (domain.User, error) {
	return domain.User{ID: "bot-id", Username: "bot"}, nil
}

func (mainTestClient) ListFollowers(context.Context, string, domain.PageOptions) ([]domain.Following, error) {
	return []domain.Following{}, nil
}

func (mainTestClient) ListFollowing(context.Context, string, domain.PageOptions) ([]domain.Following, error) {
	return []domain.Following{}, nil
}

func (mainTestClient) ListUserNotes(context.Context, string, domain.NotePageOptions) ([]domain.Note, error) {
	return []domain.Note{}, nil
}

func (mainTestClient) GetRelations(context.Context, []string) ([]domain.Relation, error) {
	return []domain.Relation{}, nil
}

func (mainTestClient) CreateFollow(context.Context, string) (domain.User, error) {
	return domain.User{}, nil
}

func (mainTestClient) DeleteFollow(context.Context, string) (domain.User, error) {
	return domain.User{}, nil
}

type mainObserveClient struct {
	mu            sync.Mutex
	relations     map[string]domain.Relation
	writes        []string
	followCalls   chan string
	unfollowCalls chan string
}

func newMainObserveClient() *mainObserveClient {
	return &mainObserveClient{
		relations: map[string]domain.Relation{
			"follower": {ID: "follower", IsFollowed: true},
			"followee": {ID: "followee", IsFollowing: true},
		},
		followCalls:   make(chan string, 1),
		unfollowCalls: make(chan string, 1),
	}
}

func (c *mainObserveClient) Self(ctx context.Context) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	return domain.User{ID: "bot"}, nil
}

func (c *mainObserveClient) ListFollowers(ctx context.Context, _ string, options domain.PageOptions) ([]domain.Following, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.UntilID != "" {
		return []domain.Following{}, nil
	}
	return []domain.Following{{ID: "inbound-relation", FollowerID: "follower", FolloweeID: "bot"}}, nil
}

func (c *mainObserveClient) ListFollowing(ctx context.Context, _ string, options domain.PageOptions) ([]domain.Following, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.UntilID != "" {
		return []domain.Following{}, nil
	}
	return []domain.Following{{ID: "outbound-relation", FollowerID: "bot", FolloweeID: "followee"}}, nil
}

func (c *mainObserveClient) ListUserNotes(ctx context.Context, _ string, _ domain.NotePageOptions) ([]domain.Note, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return []domain.Note{}, nil
}

func (c *mainObserveClient) GetRelations(ctx context.Context, ids []string) ([]domain.Relation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	relations := make([]domain.Relation, 0, len(ids))
	for _, id := range ids {
		relations = append(relations, c.relations[id])
	}
	return relations, nil
}

func (c *mainObserveClient) CreateFollow(ctx context.Context, id string) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	c.mu.Lock()
	c.writes = append(c.writes, "follow:"+id)
	c.relations[id] = domain.Relation{ID: id, IsFollowing: true, IsFollowed: true}
	c.mu.Unlock()
	c.followCalls <- id
	return domain.User{ID: id}, nil
}

func (c *mainObserveClient) DeleteFollow(ctx context.Context, id string) (domain.User, error) {
	if err := ctx.Err(); err != nil {
		return domain.User{}, err
	}
	c.mu.Lock()
	c.writes = append(c.writes, "unfollow:"+id)
	c.relations[id] = domain.Relation{ID: id}
	c.mu.Unlock()
	c.unfollowCalls <- id
	return domain.User{ID: id}, nil
}

func (c *mainObserveClient) relationshipWrites() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.writes...)
}

func buildCLI(t *testing.T) string {
	t.Helper()
	workdir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	binary := filepath.Join(t.TempDir(), "azkey-roumu-bot")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = workdir
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}

func mainMapLookup(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func commandEnvironment(overrides map[string]string) []string {
	environment := make([]string, 0, len(os.Environ())+len(overrides))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if _, overridden := overrides[key]; !overridden {
			environment = append(environment, entry)
		}
	}
	for key, value := range overrides {
		environment = append(environment, key+"="+value)
	}
	return environment
}
