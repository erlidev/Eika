package provider

import (
	"errors"
	"fmt"
	"time"
)

// Error is a failure reported by a provider. The agent loop retries a call
// whose error is Retryable, waiting at least RetryAfter when it is set.
type Error struct {
	// Op is the operation that failed, for example "stream chat completion".
	Op string
	// StatusCode is the HTTP status the provider returned, or zero.
	StatusCode int
	// Retryable reports whether the same request may succeed if repeated.
	Retryable bool
	// RetryAfter is the delay the provider asked for, or zero.
	RetryAfter time.Duration
	// Err is the underlying failure.
	Err error
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s: http %d: %v", e.Op, e.StatusCode, e.Err)
	}
	return fmt.Sprintf("%s: %v", e.Op, e.Err)
}

// Unwrap returns the underlying failure.
func (e *Error) Unwrap() error { return e.Err }

// Retryable reports whether err asks the caller to try the request again.
func Retryable(err error) bool {
	var perr *Error
	if errors.As(err, &perr) {
		return perr.Retryable
	}
	return false
}

// RetryAfter reports the delay a provider asked for before a retry, or zero.
func RetryAfter(err error) time.Duration {
	var perr *Error
	if errors.As(err, &perr) {
		return perr.RetryAfter
	}
	return 0
}
