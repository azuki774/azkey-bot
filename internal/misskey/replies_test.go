package misskey

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

func TestReplyEndpoints(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body := decodeRequest(t, r)
		if body["i"] != "secret" {
			t.Error("missing credential")
		}
		switch r.URL.Path {
		case "/api/notes/mentions":
			if body["following"] != false || body["visibility"] != "public" || body["sinceId"] != "cursor" || body["limit"] != float64(10) {
				t.Errorf("mentions request: %v", body)
			}
			writeJSON(w, 200, []any{map[string]any{"id": "note", "createdAt": "2026-10-02T00:00:00Z", "userId": "user", "visibility": "public", "localOnly": true, "replyId": "parent", "reply": map[string]any{"userId": "bot"}, "user": map[string]any{"id": "user", "username": "user", "isBot": true}}})
		case "/api/users/relation":
			if body["userId"] != "user" {
				t.Error("wrong relation target")
			}
			writeJSON(w, 200, []any{map[string]any{"id": "user", "isFollowing": true, "isFollowed": false}})
		case "/api/notes/create":
			if body["replyId"] != "note" || body["text"] != "reply" || body["visibility"] != "public" || body["localOnly"] != true {
				t.Errorf("reply request: %v", body)
			}
			writeJSON(w, 500, map[string]any{"error": map[string]any{"code": "INTERNAL_ERROR"}})
		default:
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := NewClient(mustParseURL(t, server.URL), "secret")
	if err != nil {
		t.Fatal(err)
	}
	notes, err := c.ListMentions(context.Background(), domain.NotePageOptions{Limit: 10, SinceID: "cursor"})
	if err != nil || len(notes) != 1 {
		t.Fatalf("mentions: %v %v", notes, err)
	}
	if notes[0].ReplyUserID != "bot" || notes[0].ReplyID != "parent" || !notes[0].LocalOnly || !notes[0].User.IsBot {
		t.Fatalf("lost note fields: %+v", notes[0])
	}
	following, err := c.IsFollowing(context.Background(), "user")
	if err != nil || !following {
		t.Fatalf("relation: %v %v", following, err)
	}
	err = c.CreateReply(context.Background(), notes[0], "reply")
	var apiErr *domain.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindServer {
		t.Fatalf("send failure: %v", err)
	}
	if requests != 3 {
		t.Fatalf("write retried: %d requests", requests)
	}
}

func TestRelationRejectsMissingOrMismatchedData(t *testing.T) {
	for _, response := range []any{nil, []any{}, []any{map[string]any{"id": "user"}}, []any{map[string]any{"id": "other", "isFollowing": true}}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, response) }))
		c, _ := NewClient(mustParseURL(t, server.URL), "secret")
		if following, err := c.IsFollowing(context.Background(), "user"); err == nil || following {
			t.Errorf("invalid relation accepted: %v", response)
		}
		server.Close()
	}
}

func TestMentionsTimeBaselineAndRateLimit(t *testing.T) {
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeRequest(t, r)
		if body["sinceDate"] != float64(now.UnixMilli()) {
			t.Error("missing baseline")
		}
		w.Header().Set("Retry-After", "60")
		writeJSON(w, 429, map[string]any{"error": map[string]any{"code": "RATE_LIMIT_EXCEEDED"}})
	}))
	defer server.Close()
	c, _ := NewClient(mustParseURL(t, server.URL), "secret")
	_, err := c.ListMentions(context.Background(), domain.NotePageOptions{SinceDate: &now})
	var apiErr *domain.Error
	if !errors.As(err, &apiErr) || apiErr.Kind != domain.ErrorKindRateLimit || apiErr.RetryAfter == nil || *apiErr.RetryAfter != time.Minute {
		t.Fatalf("rate limit: %v", err)
	}
}
