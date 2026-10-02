package misskey

import (
	"context"
	"github.com/azuki774/azkey-bot/internal/domain"
	"strings"
)

// ListMentions includes non-followed users. The caller selects direct replies.
// Checked against upstream Misskey 2026.9.0 notes/mentions.ts: sinceId/date
// requests are ascending; without them the latest notes are returned first.
func (c *Client) ListMentions(ctx context.Context, options domain.NotePageOptions) ([]domain.Note, error) {
	if err := validateNotePageOptions(options); err != nil {
		return nil, err
	}
	body, err := c.post(ctx, "notes/mentions", struct {
		I          string `json:"i"`
		Limit      int    `json:"limit,omitempty"`
		SinceID    string `json:"sinceId,omitempty"`
		SinceDate  *int64 `json:"sinceDate,omitempty"`
		Visibility string `json:"visibility"`
		Following  bool   `json:"following"`
	}{cToken(c), options.Limit, options.SinceID, sinceDateMillis(options.SinceDate), "public", false})
	if err != nil {
		return nil, err
	}
	var response []noteDTO
	if decodeJSON(body, &response) != nil || response == nil {
		return nil, invalidResponseError()
	}
	result := make([]domain.Note, 0, len(response))
	for _, note := range response {
		converted, err := note.toDomain()
		if err != nil {
			return nil, invalidResponseError()
		}
		result = append(result, converted)
	}
	return result, nil
}

// IsFollowing checks the bot-to-user direction, including one-way follows.
// In 2026.9.0 users/relation returns an array even for a scalar userId.
func (c *Client) IsFollowing(ctx context.Context, userID string) (bool, error) {
	if err := validateUserID(userID); err != nil {
		return false, err
	}
	body, err := c.post(ctx, "users/relation", followRequest{I: cToken(c), UserID: userID})
	if err != nil {
		return false, err
	}
	var response []struct {
		ID          string `json:"id"`
		IsFollowing *bool  `json:"isFollowing"`
	}
	if decodeJSON(body, &response) != nil || len(response) != 1 || response[0].ID != userID || response[0].IsFollowing == nil {
		return false, invalidResponseError()
	}
	return *response[0].IsFollowing, nil
}

// CreateReply makes exactly one public notes/create request and preserves localOnly.
func (c *Client) CreateReply(ctx context.Context, note domain.Note, text string) error {
	if note.ID == "" || note.Visibility != "public" || strings.TrimSpace(text) == "" {
		return invalidArgumentError()
	}
	_, err := c.post(ctx, "notes/create", struct {
		I          string `json:"i"`
		ReplyID    string `json:"replyId"`
		Text       string `json:"text"`
		Visibility string `json:"visibility"`
		LocalOnly  bool   `json:"localOnly"`
	}{cToken(c), note.ID, text, "public", note.LocalOnly})
	return err
}
