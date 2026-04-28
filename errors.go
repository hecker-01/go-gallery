package gallery

import (
	"errors"
	"fmt"
	"time"
)

// ExtractionError is the base interface for failures originating in extractors
// or the download pipeline. All concrete error types below implement it.
type ExtractionError interface {
	error
	isExtractionError()
}

// HttpError is returned when an HTTP response has an unexpected status code.
type HttpError struct {
	StatusCode int
	URL        string
	Body       []byte
}

func (e *HttpError) Error() string {
	return fmt.Sprintf("HTTP %d fetching %s", e.StatusCode, e.URL)
}
func (e *HttpError) isExtractionError() {}

// AuthenticationError indicates invalid or missing credentials (expired
// session, wrong auth_token / ct0 cookies).
type AuthenticationError struct {
	Cause error
}

func (e *AuthenticationError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("authentication failed: %v", e.Cause)
	}
	return "authentication failed"
}
func (e *AuthenticationError) Unwrap() error    { return e.Cause }
func (e *AuthenticationError) isExtractionError() {}

// AuthorizationError indicates the authenticated user is not permitted to view
// the requested resource (protected account, not following).
type AuthorizationError struct {
	URL   string
	Cause error
}

func (e *AuthorizationError) Error() string {
	return fmt.Sprintf("not authorized to access %s", e.URL)
}
func (e *AuthorizationError) Unwrap() error    { return e.Cause }
func (e *AuthorizationError) isExtractionError() {}

// NotFoundError is returned for deleted tweets or suspended accounts.
type NotFoundError struct {
	URL string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("not found: %s", e.URL)
}
func (e *NotFoundError) isExtractionError() {}

// ChallengeError indicates Twitter is showing a verification challenge (CAPTCHA
// / unlock flow) and the request cannot proceed.
type ChallengeError struct {
	URL string
}

func (e *ChallengeError) Error() string {
	return fmt.Sprintf("Twitter challenge required for %s — log in via a browser to resolve", e.URL)
}
func (e *ChallengeError) isExtractionError() {}

// RateLimitError carries the full context of a 429 response or an internal
// rate-limit trip.
type RateLimitError struct {
	Endpoint  string
	Limit     int
	Remaining int
	ResetAt   time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("rate limit hit on %s (limit=%d, resets at %s)",
		e.Endpoint, e.Limit, e.ResetAt.UTC().Format(time.RFC3339))
}
func (e *RateLimitError) isExtractionError() {}

// InputError is returned for invalid configuration or bad input to a public API
// function. It is not an ExtractionError.
type InputError struct {
	Field   string
	Message string
}

func (e *InputError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("invalid input for %s: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("invalid input: %s", e.Message)
}

// ControlError signals flow control rather than a real failure. Use errors.Is
// to distinguish the three levels.

// StopExtraction asks the extractor to stop after the current page / batch.
// Items already in-flight are completed; no new pages are fetched.
var StopExtraction = errors.New("stop extraction")

// AbortExtraction cancels the current extraction immediately. In-flight
// downloads that have already started are allowed to finish.
var AbortExtraction = errors.New("abort extraction")

// TerminateExtraction hard-cancels everything, including in-flight downloads.
var TerminateExtraction = errors.New("terminate extraction")
