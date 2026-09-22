// Package domain contains values shared by the bot layers.
package domain

import "time"

// PageOptions selects one page from a Misskey list endpoint. A zero Limit
// leaves the endpoint default in effect. SinceID and UntilID are passed to
// Misskey as-is; for relationship lists they are relationship IDs, not user
// IDs.
// Bounds are exclusive. SinceID alone returns ascending IDs; UntilID,
// both cursors, or neither cursor returns descending IDs in the target version.
type PageOptions struct {
	Limit   int
	SinceID string
	UntilID string
}

// NotePageOptions selects one page from users/notes. Note-specific filters are
// kept separate from relationship pagination because Misskey exposes a
// different set of options for notes. SinceDate is encoded by the HTTP client
// as Unix milliseconds when it is present.
type NotePageOptions struct {
	Limit            int
	SinceID          string
	UntilID          string
	SinceDate        *time.Time
	WithReplies      bool
	WithRenotes      bool
	WithChannelNotes bool
}

// User is the small user projection used by the bot.
type User struct {
	ID       string
	Username string
	Name     *string
	Host     *string
}

// Following is a Misskey follow relationship. ID is the relationship ID and
// can be used as a cursor for a following/followers page.
type Following struct {
	ID         string
	CreatedAt  time.Time
	FollowerID string
	FolloweeID string
	Follower   *User
	Followee   *User
}

// Note is the small note projection needed by the bot. Text and CW are
// nullable in Misskey responses, including for notes such as pure renotes.
type Note struct {
	ID         string
	CreatedAt  time.Time
	UserID     string
	Text       *string
	CW         *string
	Visibility string
	User       *User
}
