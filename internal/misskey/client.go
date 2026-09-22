// Package misskey provides a small HTTP client for upstream Misskey 2026.9.0.
// API behavior was checked against https://github.com/misskey-dev/misskey/tree/2026.9.0.
// Methods make a single request without automatic retries or pagination.
package misskey

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/azuki774/azkey-bot/internal/domain"
)

const requestTimeout = 30 * time.Second
const maxResponseBodySize = 1 << 20

var errResponseBodyTooLarge = errors.New("misskey response body is too large")

var (
	errBaseURLInvalid = errors.New("misskey base URL is invalid")
	errTokenRequired  = errors.New("misskey token is required")
)

// Client is a small HTTP client for the supported Misskey API endpoints.
type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	token      string
}

// NewClient constructs a bounded HTTP client without making a network or
// authentication request.
func NewClient(baseURL *url.URL, token string) (*Client, error) {
	if !validBaseURL(baseURL) {
		return nil, errBaseURLInvalid
	}
	if strings.TrimSpace(token) == "" {
		return nil, errTokenRequired
	}

	baseURLCopy := *baseURL
	return &Client{
		baseURL: &baseURLCopy,
		httpClient: &http.Client{
			Timeout: requestTimeout,
			// Misskey credentials are sent in the JSON request body. Do not
			// follow redirects, which could forward that body to another host.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		token: token,
	}, nil
}

// Self returns the authenticated bot user from the /api/i endpoint.
func (c *Client) Self(ctx context.Context) (domain.User, error) {
	body, err := c.post(ctx, "i", selfRequest{I: cToken(c)})
	if err != nil {
		return domain.User{}, err
	}

	var response userDTO
	if err := decodeJSON(body, &response); err != nil {
		return domain.User{}, invalidResponseError()
	}
	user, err := response.toDomain()
	if err != nil {
		return domain.User{}, invalidResponseError()
	}
	return user, nil
}

// ListFollowers returns one page of relationships whose followee is userID.
// SinceID and UntilID refer to relationship IDs returned in Following.ID.
func (c *Client) ListFollowers(ctx context.Context, userID string, options domain.PageOptions) ([]domain.Following, error) {
	return c.listFollowing(ctx, "users/followers", userID, options)
}

// ListFollowing returns one page of relationships whose follower is userID.
// SinceID and UntilID refer to relationship IDs returned in Following.ID.
func (c *Client) ListFollowing(ctx context.Context, userID string, options domain.PageOptions) ([]domain.Following, error) {
	return c.listFollowing(ctx, "users/following", userID, options)
}

// ListUserNotes returns one page of notes authored by userID. SinceID and
// UntilID are note IDs, as defined by users/notes. Note filters are sent
// explicitly so the caller can choose the business policy for renotes while
// replies are included and channel notes are excluded by the polling layer.
// The caller must select public notes. Server-side filters mean a short page
// alone does not prove the cursor range is complete. Results retain server
// order; the client does not sort them.
func (c *Client) ListUserNotes(ctx context.Context, userID string, options domain.NotePageOptions) ([]domain.Note, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validateNotePageOptions(options); err != nil {
		return nil, err
	}

	body, err := c.post(ctx, "users/notes", notePageRequest{
		I:                cToken(c),
		UserID:           userID,
		Limit:            options.Limit,
		SinceID:          options.SinceID,
		UntilID:          options.UntilID,
		SinceDate:        sinceDateMillis(options.SinceDate),
		WithReplies:      options.WithReplies,
		WithRenotes:      options.WithRenotes,
		WithChannelNotes: options.WithChannelNotes,
	})
	if err != nil {
		return nil, err
	}

	var response []noteDTO
	if err := decodeJSON(body, &response); err != nil {
		return nil, invalidResponseError()
	}
	if response == nil {
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

// CreateFollow follows userID and returns the followed user.
// Success can mean a pending follow request, not an accepted follow.
func (c *Client) CreateFollow(ctx context.Context, userID string) (domain.User, error) {
	if err := validateUserID(userID); err != nil {
		return domain.User{}, err
	}

	body, err := c.post(ctx, "following/create", followRequest{
		I:      cToken(c),
		UserID: userID,
	})
	if err != nil {
		return domain.User{}, err
	}

	var response userDTO
	if err := decodeJSON(body, &response); err != nil {
		return domain.User{}, invalidResponseError()
	}
	user, err := response.toDomain()
	if err != nil {
		return domain.User{}, invalidResponseError()
	}
	return user, nil
}

// CreateReaction adds reaction to noteID. Reaction deletion is intentionally
// not part of this client.
func (c *Client) CreateReaction(ctx context.Context, noteID, reaction string) error {
	if strings.TrimSpace(noteID) == "" || strings.TrimSpace(reaction) == "" {
		return invalidArgumentError()
	}

	_, err := c.post(ctx, "notes/reactions/create", reactionRequest{
		I:        cToken(c),
		NoteID:   noteID,
		Reaction: reaction,
	})
	return err
}

func (c *Client) listFollowing(ctx context.Context, endpoint, userID string, options domain.PageOptions) ([]domain.Following, error) {
	if err := validateUserID(userID); err != nil {
		return nil, err
	}
	if err := validatePageOptions(options); err != nil {
		return nil, err
	}

	body, err := c.post(ctx, endpoint, pageRequest{
		I:       cToken(c),
		UserID:  userID,
		Limit:   options.Limit,
		SinceID: options.SinceID,
		UntilID: options.UntilID,
	})
	if err != nil {
		return nil, err
	}

	var response []followingDTO
	if err := decodeJSON(body, &response); err != nil {
		return nil, invalidResponseError()
	}
	if response == nil {
		return nil, invalidResponseError()
	}

	result := make([]domain.Following, 0, len(response))
	for _, following := range response {
		converted, err := following.toDomain()
		if err != nil {
			return nil, invalidResponseError()
		}
		result = append(result, converted)
	}
	return result, nil
}

func (c *Client) post(ctx context.Context, endpoint string, payload any) ([]byte, error) {
	if c == nil || c.baseURL == nil || c.httpClient == nil {
		return nil, invalidArgumentError()
	}
	if ctx == nil {
		return nil, invalidArgumentError()
	}
	if err := ctx.Err(); err != nil {
		return nil, contextError(err)
	}

	requestURL, err := c.endpointURL(endpoint)
	if err != nil {
		return nil, invalidArgumentError()
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, invalidArgumentError()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return nil, invalidArgumentError()
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := c.httpClient.Do(request)
	if err != nil {
		return nil, transportError(ctx, err)
	}
	defer response.Body.Close()

	body, readErr := readBody(response.Body)
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, httpResponseError(response.StatusCode, response.Header, body)
	}
	if readErr != nil {
		if errors.Is(readErr, errResponseBodyTooLarge) {
			return nil, invalidResponseError()
		}
		return nil, transportError(ctx, readErr)
	}
	return body, nil
}

func (c *Client) endpointURL(endpoint string) (string, error) {
	if endpoint == "" || strings.Contains(endpoint, "?") || strings.Contains(endpoint, "#") {
		return "", errors.New("invalid endpoint")
	}
	base := *c.baseURL
	base.Path = strings.TrimRight(base.Path, "/") + "/api/" + endpoint
	base.RawPath = ""
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return base.String(), nil
}

func cToken(c *Client) string {
	if c == nil {
		return ""
	}
	return c.token
}

func validateUserID(userID string) error {
	if strings.TrimSpace(userID) == "" {
		return invalidArgumentError()
	}
	return nil
}

func validatePageOptions(options domain.PageOptions) error {
	if options.Limit < 0 || options.Limit > 100 {
		return invalidArgumentError()
	}
	return nil
}

func validateNotePageOptions(options domain.NotePageOptions) error {
	if options.Limit < 0 || options.Limit > 100 {
		return invalidArgumentError()
	}
	if options.SinceDate != nil && options.SinceDate.IsZero() {
		return invalidArgumentError()
	}
	return nil
}

func sinceDateMillis(value *time.Time) *int64 {
	if value == nil {
		return nil
	}
	millis := value.UnixMilli()
	return &millis
}

func decodeJSON(body []byte, target any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("empty response")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("response contains multiple JSON values")
		}
		return err
	}
	return nil
}

func readBody(reader io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBodySize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBodySize {
		return nil, errResponseBodyTooLarge
	}
	return body, nil
}

func invalidArgumentError() *domain.Error {
	return domain.NewError(domain.ErrorKindClient, 0, "INVALID_ARGUMENT", nil)
}

func invalidResponseError() *domain.Error {
	return domain.NewError(domain.ErrorKindInvalidResponse, 0, "INVALID_RESPONSE", nil)
}

func contextError(err error) *domain.Error {
	if errors.Is(err, context.Canceled) {
		return domain.NewContextError(domain.ErrorKindCanceled, context.Canceled)
	}
	return domain.NewContextError(domain.ErrorKindNetworkTimeout, context.DeadlineExceeded)
}

func transportError(ctx context.Context, err error) *domain.Error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return contextError(ctxErr)
	}
	if errors.Is(err, context.Canceled) {
		return domain.NewContextError(domain.ErrorKindCanceled, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) || os.IsTimeout(err) {
		return domain.NewContextError(domain.ErrorKindNetworkTimeout, context.DeadlineExceeded)
	}
	return domain.NewError(domain.ErrorKindNetwork, 0, "NETWORK_ERROR", nil)
}

func httpResponseError(statusCode int, headers http.Header, body []byte) *domain.Error {
	kind := domain.ErrorKindInvalidResponse
	switch {
	case statusCode == http.StatusUnauthorized:
		kind = domain.ErrorKindAuth
	case statusCode == http.StatusForbidden:
		kind = domain.ErrorKindAuth
	case statusCode == http.StatusTooManyRequests:
		kind = domain.ErrorKindRateLimit
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		kind = domain.ErrorKindClient
	case statusCode >= http.StatusInternalServerError:
		kind = domain.ErrorKindServer
	}

	var response apiErrorResponse
	_ = json.Unmarshal(body, &response)
	var retryAfter *time.Duration
	if statusCode == http.StatusTooManyRequests {
		retryAfter = parseRetryAfter(headers.Get("Retry-After"))
	}
	return domain.NewError(kind, statusCode, response.Error.Code, retryAfter)
}

func parseRetryAfter(value string) *time.Duration {
	// Misskey 2026.9.0 ApiCallService emits decimal seconds, not HTTP dates.
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	const maxRetryAfterSeconds = int64((1<<63 - 1) / int64(time.Second))
	if err != nil || seconds < 0 || seconds > maxRetryAfterSeconds {
		return nil
	}
	duration := time.Duration(seconds) * time.Second
	return &duration
}

func validBaseURL(baseURL *url.URL) bool {
	if baseURL == nil || !baseURL.IsAbs() || baseURL.Opaque != "" || baseURL.Host == "" || baseURL.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(baseURL.Scheme, "http") && !strings.EqualFold(baseURL.Scheme, "https") {
		return false
	}
	if baseURL.User != nil || baseURL.RawQuery != "" || baseURL.ForceQuery || baseURL.Fragment != "" || baseURL.RawFragment != "" {
		return false
	}
	if port := baseURL.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber < 0 || portNumber > 65535 {
			return false
		}
	}
	return true
}
