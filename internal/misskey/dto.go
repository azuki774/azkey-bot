package misskey

import (
	"errors"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

// These DTOs mirror only the fields consumed by this package. The complete
// Misskey response objects stay private to the HTTP boundary.
type selfRequest struct {
	I string `json:"i"`
}

type pageRequest struct {
	I       string `json:"i"`
	UserID  string `json:"userId"`
	Limit   int    `json:"limit,omitempty"`
	SinceID string `json:"sinceId,omitempty"`
	UntilID string `json:"untilId,omitempty"`
}

type notePageRequest struct {
	I                string `json:"i"`
	UserID           string `json:"userId"`
	Limit            int    `json:"limit,omitempty"`
	SinceID          string `json:"sinceId,omitempty"`
	UntilID          string `json:"untilId,omitempty"`
	SinceDate        *int64 `json:"sinceDate,omitempty"`
	WithReplies      bool   `json:"withReplies"`
	WithRenotes      bool   `json:"withRenotes"`
	WithChannelNotes bool   `json:"withChannelNotes"`
}

type followRequest struct {
	I      string `json:"i"`
	UserID string `json:"userId"`
}

type reactionRequest struct {
	I        string `json:"i"`
	NoteID   string `json:"noteId"`
	Reaction string `json:"reaction"`
}

type userDTO struct {
	ID       string  `json:"id"`
	Username string  `json:"username"`
	Name     *string `json:"name"`
	Host     *string `json:"host"`
}

func (u userDTO) toDomain() (domain.User, error) {
	if u.ID == "" || u.Username == "" {
		return domain.User{}, errors.New("user response is missing an identifier")
	}
	return domain.User{
		ID:       u.ID,
		Username: u.Username,
		Name:     u.Name,
		Host:     u.Host,
	}, nil
}

type followingDTO struct {
	ID         string   `json:"id"`
	CreatedAt  string   `json:"createdAt"`
	FollowerID string   `json:"followerId"`
	FolloweeID string   `json:"followeeId"`
	Follower   *userDTO `json:"follower"`
	Followee   *userDTO `json:"followee"`
}

func (f followingDTO) toDomain() (domain.Following, error) {
	if f.ID == "" || f.FollowerID == "" || f.FolloweeID == "" {
		return domain.Following{}, errors.New("following response is missing an identifier")
	}
	createdAt, err := parseDate(f.CreatedAt)
	if err != nil {
		return domain.Following{}, err
	}
	follower, err := optionalUser(f.Follower)
	if err != nil {
		return domain.Following{}, err
	}
	followee, err := optionalUser(f.Followee)
	if err != nil {
		return domain.Following{}, err
	}
	return domain.Following{
		ID:         f.ID,
		CreatedAt:  createdAt,
		FollowerID: f.FollowerID,
		FolloweeID: f.FolloweeID,
		Follower:   follower,
		Followee:   followee,
	}, nil
}

type noteDTO struct {
	ID         string   `json:"id"`
	CreatedAt  string   `json:"createdAt"`
	UserID     string   `json:"userId"`
	Text       *string  `json:"text"`
	CW         *string  `json:"cw"`
	Visibility string   `json:"visibility"`
	User       *userDTO `json:"user"`
}

func (n noteDTO) toDomain() (domain.Note, error) {
	if n.ID == "" || n.UserID == "" {
		return domain.Note{}, errors.New("note response is missing an identifier")
	}
	createdAt, err := parseDate(n.CreatedAt)
	if err != nil {
		return domain.Note{}, err
	}
	user, err := optionalUser(n.User)
	if err != nil {
		return domain.Note{}, err
	}
	return domain.Note{
		ID:         n.ID,
		CreatedAt:  createdAt,
		UserID:     n.UserID,
		Text:       n.Text,
		CW:         n.CW,
		Visibility: n.Visibility,
		User:       user,
	}, nil
}

type apiErrorResponse struct {
	Error struct {
		Code string `json:"code"`
	} `json:"error"`
}

func optionalUser(user *userDTO) (*domain.User, error) {
	if user == nil {
		return nil, nil
	}
	converted, err := user.toDomain()
	if err != nil {
		return nil, err
	}
	return &converted, nil
}

func parseDate(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, errors.New("response is missing a date")
	}
	return time.Parse(time.RFC3339Nano, value)
}
