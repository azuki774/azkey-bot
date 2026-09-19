// Package misskey provides the HTTP client boundary for Misskey.
package misskey

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const requestTimeout = 30 * time.Second

var (
	errBaseURLInvalid = errors.New("misskey base URL is invalid")
	errTokenRequired  = errors.New("misskey token is required")
)

// Client owns the HTTP transport settings and authentication material for
// future Misskey endpoint implementations. Endpoint methods are deliberately
// not present until their behavior is defined.
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
		baseURL:    &baseURLCopy,
		httpClient: &http.Client{Timeout: requestTimeout},
		token:      token,
	}, nil
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
