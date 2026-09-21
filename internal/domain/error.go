package domain

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrorKind identifies the small set of failures a consumer can act on
// without depending on the HTTP client package.
type ErrorKind string

const (
	ErrorKindAuth            ErrorKind = "auth"
	ErrorKindClient          ErrorKind = "client"
	ErrorKindRateLimit       ErrorKind = "rate-limit"
	ErrorKindServer          ErrorKind = "server"
	ErrorKindNetwork         ErrorKind = "network"
	ErrorKindNetworkTimeout  ErrorKind = "network-timeout"
	ErrorKindCanceled        ErrorKind = "canceled"
	ErrorKindInvalidResponse ErrorKind = "invalid-response"
)

// Error is a safe, classified error returned by the Misskey boundary. It
// intentionally does not retain response bodies or underlying transport
// errors. RetryAfter is set only when a valid Retry-After response header was
// present on a rate-limit response.
type Error struct {
	Kind       ErrorKind
	StatusCode int
	Code       string
	RetryAfter *time.Duration

	cause error
}

// Error returns a message that contains classification and status only. API
// response bodies, tokens, URLs, and transport error strings are excluded.
func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.StatusCode > 0 {
		return fmt.Sprintf("misskey request failed: %s (status %d)", e.Kind, e.StatusCode)
	}
	return fmt.Sprintf("misskey request failed: %s", e.Kind)
}

// Unwrap preserves only context sentinels so errors.Is remains useful without
// exposing an unsafe network error chain.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// NewError creates a classified error without retaining sensitive details.
func NewError(kind ErrorKind, statusCode int, code string, retryAfter *time.Duration) *Error {
	return &Error{
		Kind:       kind,
		StatusCode: statusCode,
		Code:       code,
		RetryAfter: retryAfter,
	}
}

// NewContextError creates a classified context error and preserves only
// context.Canceled or context.DeadlineExceeded for errors.Is.
func NewContextError(kind ErrorKind, cause error) *Error {
	var safeCause error
	switch {
	case errors.Is(cause, context.Canceled):
		safeCause = context.Canceled
	case errors.Is(cause, context.DeadlineExceeded):
		safeCause = context.DeadlineExceeded
	}
	return &Error{Kind: kind, cause: safeCause}
}
