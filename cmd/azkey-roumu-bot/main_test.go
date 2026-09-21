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
	startedOnce sync.Once
	stoppedOnce sync.Once
}

func newLogCapture() *logCapture {
	return &logCapture{
		started: make(chan struct{}),
		stopped: make(chan struct{}),
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
	return len(data), nil
}

func (c *logCapture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output.String()
}

func TestRunStartsAndStopsOnContextCancellation(t *testing.T) {
	rulesPath := writeMainRules(t, `{"version":1,"rules":[]}`)
	secret := "run-lifecycle-test-secret"
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    secret,
		"RULES_FILE":       rulesPath,
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

func TestRunRejectsInvalidConfigBeforeStartup(t *testing.T) {
	secret := "invalid-config-test-secret"
	env := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    secret,
		"RULES_FILE":       writeMainRules(t, `{"version":1,"rules":[{"secret":"invalid-config-test-secret"}]}`),
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
	validRules := writeMainRules(t, `{"version":1,"rules":[]}`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/i":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "bot-id", "username": "bot", "name": nil, "host": nil})
		case "/api/users/followers":
			_ = json.NewEncoder(w).Encode([]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	validEnv := map[string]string{
		"MISSKEY_BASE_URL": server.URL,
		"MISSKEY_TOKEN":    secret,
		"RULES_FILE":       validRules,
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

	invalidSecret := "cli-invalid-config-secret"
	invalidEnv := map[string]string{
		"MISSKEY_BASE_URL": "https://misskey.example.test",
		"MISSKEY_TOKEN":    invalidSecret,
		"RULES_FILE":       writeMainRules(t, `{"version":1,"rules":[{"secret":"cli-invalid-config-secret"}]}`),
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

func (mainTestClient) ListUserNotes(context.Context, string, domain.NotePageOptions) ([]domain.Note, error) {
	return []domain.Note{}, nil
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

func writeMainRules(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write rules file: %v", err)
	}
	return path
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
