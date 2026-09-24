package misskey

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

func TestNewClientBuildsBoundedPrivateHTTPClient(t *testing.T) {
	baseURL := mustParseURL(t, "https://misskey.example.test")
	secret := "misskey-client-test-secret"

	client, err := NewClient(baseURL, secret)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if client.httpClient == nil {
		t.Fatal("NewClient did not create an HTTP client")
	}
	if client.httpClient.Timeout != requestTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", client.httpClient.Timeout, requestTimeout)
	}
	if client.httpClient.CheckRedirect == nil {
		t.Fatal("NewClient did not install redirect protection")
	}
	if client.token != secret {
		t.Fatal("client did not retain its token privately")
	}
}

func TestNewClientRejectsInvalidInputsWithoutEchoingValues(t *testing.T) {
	secret := "misskey-client-invalid-secret"
	tests := []struct {
		name    string
		baseURL *url.URL
		token   string
	}{
		{name: "nil URL", token: secret},
		{name: "missing token", baseURL: mustParseURL(t, "https://misskey.example.test")},
		{name: "query URL", baseURL: mustParseURL(t, "https://misskey.example.test?secret=value"), token: secret},
		{name: "userinfo URL", baseURL: mustParseURL(t, "https://user:password@misskey.example.test"), token: secret},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.baseURL, test.token)
			if err == nil {
				t.Fatal("NewClient returned nil error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error contains token: %q", err)
			}
		})
	}
}

func TestSelfSendsCredentialAndConvertsResponse(t *testing.T) {
	const secret = "self-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/i" {
			t.Errorf("request = %s %s, want POST /api/i", r.Method, r.URL.Path)
		}
		body := decodeRequest(t, r)
		if body["i"] != secret {
			t.Errorf("token = %v, want %q", body["i"], secret)
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":       "bot-id",
			"username": "bot",
			"name":     "Bot name",
			"host":     nil,
			"isBot":    true,
		})
	}))
	defer server.Close()

	client := newTestClient(t, server, secret)
	user, err := client.Self(context.Background())
	if err != nil {
		t.Fatalf("Self returned error: %v", err)
	}
	if user.ID != "bot-id" || user.Username != "bot" || user.Name == nil || *user.Name != "Bot name" || user.Host != nil {
		t.Fatalf("Self returned %+v", user)
	}
}

func TestListRelationshipsUsesRelationshipIDCursors(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Client) ([]domain.Following, error)
	}{
		{
			name: "followers",
			path: "/api/users/followers",
			call: func(client *Client) ([]domain.Following, error) {
				return client.ListFollowers(context.Background(), "target-user", domain.PageOptions{
					Limit: 25, SinceID: "relationship-since", UntilID: "relationship-until",
				})
			},
		},
		{
			name: "following",
			path: "/api/users/following",
			call: func(client *Client) ([]domain.Following, error) {
				return client.ListFollowing(context.Background(), "target-user", domain.PageOptions{
					Limit: 25, SinceID: "relationship-since", UntilID: "relationship-until",
				})
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("path = %q, want %q", r.URL.Path, test.path)
				}
				body := decodeRequest(t, r)
				if body["userId"] != "target-user" || body["sinceId"] != "relationship-since" || body["untilId"] != "relationship-until" || body["limit"] != float64(25) {
					t.Errorf("pagination body = %#v", body)
				}
				writeJSON(w, http.StatusOK, []map[string]any{{
					"id":         "relationship-id",
					"createdAt":  "2026-09-20T10:00:00.000Z",
					"followerId": "follower-id",
					"followeeId": "target-user",
					"follower": map[string]any{
						"id": "follower-id", "username": "follower", "name": nil, "host": nil,
					},
					"followee": map[string]any{
						"id": "target-user", "username": "target", "name": "Target", "host": nil,
					},
				}})
			}))
			defer server.Close()

			relationships, err := test.call(newTestClient(t, server, "relationship-token"))
			if err != nil {
				t.Fatalf("list returned error: %v", err)
			}
			if len(relationships) != 1 {
				t.Fatalf("relationship count = %d, want 1", len(relationships))
			}
			relationship := relationships[0]
			if relationship.ID != "relationship-id" || relationship.FollowerID != "follower-id" || relationship.FolloweeID != "target-user" {
				t.Fatalf("relationship = %+v", relationship)
			}
			if relationship.Follower == nil || relationship.Follower.ID != "follower-id" || relationship.Followee == nil || relationship.Followee.ID != "target-user" {
				t.Fatalf("relationship users = follower %v, followee %v", relationship.Follower, relationship.Followee)
			}
		})
	}
}

func TestListUserNotesUsesSinceUntilAndPreservesNullableText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/users/notes" {
			t.Errorf("path = %q, want /api/users/notes", r.URL.Path)
		}
		body := decodeRequest(t, r)
		if body["userId"] != "author-id" || body["sinceId"] != "note-since" || body["untilId"] != "note-until" || body["limit"] != float64(10) || body["withReplies"] != true || body["withRenotes"] != true || body["withChannelNotes"] != false {
			t.Errorf("notes body = %#v", body)
		}
		writeJSON(w, http.StatusOK, []map[string]any{{
			"id":         "note-id",
			"createdAt":  "2026-09-20T11:00:00Z",
			"userId":     "author-id",
			"text":       nil,
			"cw":         "warning",
			"visibility": "public",
			"user": map[string]any{
				"id": "author-id", "username": "author", "name": nil, "host": nil,
			},
		}})
	}))
	defer server.Close()

	notes, err := newTestClient(t, server, "notes-token").ListUserNotes(context.Background(), "author-id", domain.NotePageOptions{
		Limit: 10, SinceID: "note-since", UntilID: "note-until", WithReplies: true, WithRenotes: true,
	})
	if err != nil {
		t.Fatalf("ListUserNotes returned error: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("note count = %d, want 1", len(notes))
	}
	note := notes[0]
	if note.ID != "note-id" || note.UserID != "author-id" || note.Visibility != "public" || note.Text != nil || note.CW == nil || *note.CW != "warning" {
		t.Fatalf("note = %+v", note)
	}
	if note.User == nil || note.User.ID != "author-id" {
		t.Fatalf("note user = %+v", note.User)
	}
}

func TestListUserNotesEncodesEmptyBaselineDateAndExplicitFilters(t *testing.T) {
	activation := time.Date(2026, 9, 21, 10, 0, 0, 123456789, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeRequest(t, r)
		if body["sinceDate"] != float64(activation.UnixMilli()) {
			t.Errorf("sinceDate = %v, want milliseconds %d", body["sinceDate"], activation.UnixMilli())
		}
		for _, key := range []string{"sinceId", "untilId"} {
			if _, ok := body[key]; ok {
				t.Errorf("unexpected cursor %q in empty-baseline request", key)
			}
		}
		if body["withReplies"] != true || body["withRenotes"] != true || body["withChannelNotes"] != false {
			t.Errorf("unexpected note filters: %#v", body)
		}
		writeJSON(w, http.StatusOK, []any{})
	}))
	defer server.Close()
	client := newTestClient(t, server, "notes-test-token")
	_, err := client.ListUserNotes(context.Background(), "author", domain.NotePageOptions{
		Limit: 100, SinceDate: &activation, WithReplies: true, WithRenotes: true,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestListEndpointsDistinguishNullEmptyAndMalformedResponses(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Client) (bool, error)
	}{
		{
			name: "followers",
			path: "/api/users/followers",
			call: func(client *Client) (bool, error) {
				items, err := client.ListFollowers(context.Background(), "target-user", domain.PageOptions{})
				return items == nil, err
			},
		},
		{
			name: "following",
			path: "/api/users/following",
			call: func(client *Client) (bool, error) {
				items, err := client.ListFollowing(context.Background(), "target-user", domain.PageOptions{})
				return items == nil, err
			},
		},
		{
			name: "notes",
			path: "/api/users/notes",
			call: func(client *Client) (bool, error) {
				items, err := client.ListUserNotes(context.Background(), "target-user", domain.NotePageOptions{})
				return items == nil, err
			},
		},
	}
	responses := []struct {
		name       string
		body       string
		wantError  bool
		wantNilSet bool
	}{
		{name: "null", body: "null", wantError: true},
		{name: "empty array", body: "[]", wantNilSet: false},
		{name: "malformed list", body: `{"items":`, wantError: true},
	}

	for _, test := range tests {
		for _, response := range responses {
			t.Run(test.name+"/"+response.name, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != test.path {
						t.Errorf("path = %q, want %q", r.URL.Path, test.path)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(response.body))
				}))
				defer server.Close()

				isNil, err := test.call(newTestClient(t, server, "list-response-token"))
				if response.wantError {
					if err == nil {
						t.Fatal("list returned nil error")
					}
					var apiErr *domain.Error
					if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindInvalidResponse {
						t.Fatalf("error = %#v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("list returned error: %v", err)
				}
				if isNil != response.wantNilSet {
					t.Fatalf("nil slice = %t, want %t", isNil, response.wantNilSet)
				}
			})
		}
	}
}

func TestCreateFollowAndReactionUseExpectedWriteEndpoints(t *testing.T) {
	var followCalls, reactionCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/following/create":
			atomic.AddInt32(&followCalls, 1)
			body := decodeRequest(t, r)
			if body["userId"] != "follow-target" || body["i"] != "write-token" {
				t.Errorf("follow body = %#v", body)
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": "follow-target", "username": "target", "name": nil, "host": nil})
		case "/api/notes/reactions/create":
			atomic.AddInt32(&reactionCalls, 1)
			body := decodeRequest(t, r)
			if body["noteId"] != "note-target" || body["reaction"] != ":heart:" || body["i"] != "write-token" {
				t.Errorf("reaction body = %#v", body)
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server, "write-token")
	user, err := client.CreateFollow(context.Background(), "follow-target")
	if err != nil {
		t.Fatalf("CreateFollow returned error: %v", err)
	}
	if user.ID != "follow-target" {
		t.Fatalf("followed user = %+v", user)
	}
	if err := client.CreateReaction(context.Background(), "note-target", ":heart:"); err != nil {
		t.Fatalf("CreateReaction returned error: %v", err)
	}
	if atomic.LoadInt32(&followCalls) != 1 || atomic.LoadInt32(&reactionCalls) != 1 {
		t.Fatalf("calls = follow %d, reaction %d", followCalls, reactionCalls)
	}
}

func TestGetRelationsSendsArrayAndConvertsStrictRelationshipState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/users/relation" {
			t.Errorf("request = %s %s, want POST /api/users/relation", r.Method, r.URL.Path)
		}
		body := decodeRequest(t, r)
		ids, ok := body["userId"].([]any)
		if !ok || len(ids) != 2 || ids[0] != "target-one" || ids[1] != "target-two" {
			t.Errorf("userId = %#v, want array [target-one target-two]", body["userId"])
		}
		if body["i"] != "relation-token" {
			t.Errorf("token = %v", body["i"])
		}
		writeJSON(w, http.StatusOK, []map[string]any{
			testRelationDTO("target-one", true, false, false, false, false),
			testRelationDTO("target-two", false, true, true, false, false),
		})
	}))
	defer server.Close()

	relations, err := newTestClient(t, server, "relation-token").GetRelations(context.Background(), []string{"target-one", "target-two"})
	if err != nil {
		t.Fatalf("GetRelations returned error: %v", err)
	}
	if len(relations) != 2 || relations[0].ID != "target-one" || !relations[0].IsFollowing || relations[0].IsFollowed || relations[1].ID != "target-two" || relations[1].IsFollowing || !relations[1].IsFollowed || !relations[1].HasPendingFollowRequestFromYou {
		t.Fatalf("relations = %+v", relations)
	}
}

func TestGetRelationsRejectsIncompleteOrAmbiguousResponses(t *testing.T) {
	validOne := testRelationDTO("target-one", false, true, false, false, false)
	validTwo := testRelationDTO("target-two", false, true, false, false, false)
	missingBool := testRelationDTO("target-one", false, true, false, false, false)
	delete(missingBool, "isBlocked")
	nullBool := testRelationDTO("target-one", false, true, false, false, false)
	nullBool["isBlocking"] = nil
	extraID := testRelationDTO("unrequested", false, true, false, false, false)
	missingID := testRelationDTO("", false, true, false, false, false)
	delete(missingID, "id")
	tests := []struct {
		name string
		body any
	}{
		{name: "null array", body: nil},
		{name: "object instead of array", body: validOne},
		{name: "missing requested item", body: []any{validOne}},
		{name: "duplicate ID", body: []any{validOne, validOne}},
		{name: "unexpected ID", body: []any{validOne, extraID}},
		{name: "missing ID", body: []any{missingID, validTwo}},
		{name: "missing boolean", body: []any{missingBool, validTwo}},
		{name: "null boolean", body: []any{nullBool, validTwo}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, test.body)
			}))
			defer server.Close()
			_, err := newTestClient(t, server, "relation-invalid-token").GetRelations(context.Background(), []string{"target-one", "target-two"})
			var apiErr *domain.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindInvalidResponse {
				t.Fatalf("GetRelations error = %#v, want invalid response", err)
			}
		})
	}
}

func TestDeleteFollowUsesExpectedEndpointAndRejectsMismatchedUser(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/following/delete" {
				t.Errorf("path = %q, want /api/following/delete", r.URL.Path)
			}
			body := decodeRequest(t, r)
			if body["userId"] != "target" {
				t.Errorf("userId = %v, want target", body["userId"])
			}
			writeJSON(w, http.StatusOK, map[string]any{"id": "target", "username": "target", "name": nil, "host": nil})
		}))
		defer server.Close()
		user, err := newTestClient(t, server, "delete-token").DeleteFollow(context.Background(), "target")
		if err != nil || user.ID != "target" {
			t.Fatalf("DeleteFollow = %+v, %v", user, err)
		}
	})
	t.Run("mismatched user", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, map[string]any{"id": "other", "username": "other", "name": nil, "host": nil})
		}))
		defer server.Close()
		_, err := newTestClient(t, server, "delete-token").DeleteFollow(context.Background(), "target")
		var apiErr *domain.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindInvalidResponse {
			t.Fatalf("DeleteFollow error = %#v, want invalid response", err)
		}
	})
}

func TestWriteFailureIsNotRetried(t *testing.T) {
	tests := []struct {
		name string
		path string
		call func(*Client) error
	}{
		{
			name: "follow",
			path: "/api/following/create",
			call: func(client *Client) error {
				_, err := client.CreateFollow(context.Background(), "follow-target")
				return err
			},
		},
		{
			name: "reaction",
			path: "/api/notes/reactions/create",
			call: func(client *Client) error {
				return client.CreateReaction(context.Background(), "note-target", ":heart:")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var calls int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					t.Errorf("path = %q, want %q", r.URL.Path, test.path)
				}
				atomic.AddInt32(&calls, 1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"temporary failure"}}`))
			}))
			defer server.Close()

			if err := test.call(newTestClient(t, server, "write-failure-token")); err == nil {
				t.Fatal("write returned nil error")
			}
			if got := atomic.LoadInt32(&calls); got != 1 {
				t.Fatalf("request count = %d, want 1", got)
			}
		})
	}
}

func TestHTTPErrorClassificationAndSafeMessage(t *testing.T) {
	const secret = "error-token-secret"
	tests := []struct {
		name       string
		status     int
		kind       domain.ErrorKind
		code       string
		retryAfter string
	}{
		{name: "auth", status: http.StatusUnauthorized, kind: domain.ErrorKindAuth, code: "AUTHENTICATION_FAILED"},
		{name: "permission", status: http.StatusForbidden, kind: domain.ErrorKindAuth, code: "PERMISSION_DENIED"},
		{name: "client", status: http.StatusBadRequest, kind: domain.ErrorKindClient, code: "INVALID_PARAM"},
		{name: "rate limit", status: http.StatusTooManyRequests, kind: domain.ErrorKindRateLimit, code: "RATE_LIMIT_EXCEEDED", retryAfter: "7"},
		{name: "server", status: http.StatusBadGateway, kind: domain.ErrorKindServer, code: "INTERNAL_ERROR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if test.retryAfter != "" {
					w.Header().Set("Retry-After", test.retryAfter)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"error":{"code":"` + test.code + `","message":"` + secret + `"}}`))
			}))
			client := newTestClient(t, server, secret)
			_, err := client.Self(context.Background())
			server.Close()

			if err == nil {
				t.Fatal("Self returned nil error")
			}
			var apiErr *domain.Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type = %T, want *domain.Error", err)
			}
			if apiErr.Kind != test.kind || apiErr.StatusCode != test.status || apiErr.Code != test.code {
				t.Fatalf("classified error = %+v", apiErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error contains token: %q", err)
			}
			if test.retryAfter == "" && apiErr.RetryAfter != nil {
				t.Fatalf("RetryAfter = %v, want nil", apiErr.RetryAfter)
			}
			if test.retryAfter != "" && (apiErr.RetryAfter == nil || *apiErr.RetryAfter != 7*time.Second) {
				t.Fatalf("RetryAfter = %v, want 7s", apiErr.RetryAfter)
			}
		})
	}
}

func TestParseRetryAfterAcceptsOnlyBoundedSeconds(t *testing.T) {
	overflow := strconv.FormatInt(int64((1<<63-1)/int64(time.Second))+1, 10)
	tests := []struct {
		name  string
		value string
		want  *time.Duration
	}{
		{name: "missing", value: ""},
		{name: "invalid", value: "not-a-number"},
		{name: "negative", value: "-1"},
		{name: "overflow", value: overflow},
		{name: "zero", value: "0", want: durationPointer(0)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := parseRetryAfter(test.value)
			if test.want == nil {
				if got != nil {
					t.Fatalf("parseRetryAfter(%q) = %v, want nil", test.value, got)
				}
				return
			}
			if got == nil || *got != *test.want {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", test.value, got, *test.want)
			}
		})
	}
}

func TestMalformedSuccessResponseIsClassifiedWithoutRawDetails(t *testing.T) {
	const secret = "malformed-response-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"` + secret + `"`))
	}))
	defer server.Close()

	_, err := newTestClient(t, server, secret).Self(context.Background())
	if err == nil {
		t.Fatal("Self returned nil error")
	}
	var apiErr *domain.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindInvalidResponse {
		t.Fatalf("error = %#v", err)
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error contains response data: %q", err)
	}
}

func TestContextCancellationAndTimeoutAreClassified(t *testing.T) {
	t.Run("already canceled", func(t *testing.T) {
		var calls int32
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			atomic.AddInt32(&calls, 1)
		}))
		defer server.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := newTestClient(t, server, "cancel-token").Self(ctx)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		var apiErr *domain.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindCanceled {
			t.Fatalf("classified error = %#v", err)
		}
		if atomic.LoadInt32(&calls) != 0 {
			t.Fatal("canceled request reached server")
		}
	})

	t.Run("cancel in flight", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
		}))

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		client := newTestClient(t, server, "cancel-in-flight-token")
		done := make(chan error, 1)
		go func() {
			_, err := client.Self(ctx)
			done <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			server.Close()
			t.Fatal("server did not receive canceled request")
		}
		cancel()
		err := <-done
		close(release)
		server.Close()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		var apiErr *domain.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindCanceled {
			t.Fatalf("classified error = %#v", err)
		}
	})

	t.Run("deadline", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			time.Sleep(100 * time.Millisecond)
		}))
		defer server.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, err := newTestClient(t, server, "timeout-token").Self(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want context.DeadlineExceeded", err)
		}
		var apiErr *domain.Error
		if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindNetworkTimeout {
			t.Fatalf("classified error = %#v", err)
		}
	})
}

func TestConnectionDropIsNotRetried(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support connection hijacking")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()

	err := newTestClient(t, server, "connection-drop-token").CreateReaction(context.Background(), "note-target", "👍")
	if err == nil {
		t.Fatal("CreateReaction returned nil error")
	}
	var apiErr *domain.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindNetwork {
		t.Fatalf("connection error = %#v", err)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("request count = %d, want 1", got)
	}
}

func TestRedirectIsNotFollowedAndDoesNotForwardCredential(t *testing.T) {
	const secret = "redirect-token-secret"
	var targetCalls int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetCalls, 1)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL+"/api/i")
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()

	_, err := newTestClient(t, redirect, secret).Self(context.Background())
	if err == nil {
		t.Fatal("Self returned nil error")
	}
	var apiErr *domain.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindInvalidResponse || apiErr.StatusCode != http.StatusFound {
		t.Fatalf("redirect error = %#v", err)
	}
	if atomic.LoadInt32(&targetCalls) != 0 {
		t.Fatal("redirect target received a request")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("redirect error contains token: %q", err)
	}
}

func TestInvalidLocalArgumentsDoNotMakeRequests(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		atomic.AddInt32(&calls, 1)
	}))
	defer server.Close()
	client := newTestClient(t, server, "argument-token")

	if _, err := client.ListFollowers(context.Background(), "", domain.PageOptions{}); err == nil {
		t.Fatal("empty user ID returned nil error")
	}
	if _, err := client.ListUserNotes(context.Background(), "author", domain.NotePageOptions{Limit: 101}); err == nil {
		t.Fatal("invalid limit returned nil error")
	}
	if err := client.CreateReaction(context.Background(), "", ":heart:"); err == nil {
		t.Fatal("empty note ID returned nil error")
	}
	if atomic.LoadInt32(&calls) != 0 {
		t.Fatal("invalid argument reached server")
	}
}

func newTestClient(t *testing.T, server *httptest.Server, token string) *Client {
	t.Helper()
	client, err := NewClient(mustParseURL(t, server.URL), token)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return client
}

func decodeRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return body
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func testRelationDTO(id string, following, followed, pending, blocking, blocked bool) map[string]any {
	return map[string]any{
		"id":                             id,
		"isFollowing":                    following,
		"isFollowed":                     followed,
		"hasPendingFollowRequestFromYou": pending,
		"isBlocking":                     blocking,
		"isBlocked":                      blocked,
	}
}

func durationPointer(value time.Duration) *time.Duration {
	return &value
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}
