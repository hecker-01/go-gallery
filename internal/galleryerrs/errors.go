// Package galleryerrs contains the exported error types shared between the
// root gallery package and internal sub-packages (extractor, downloader).
// The root package re-exports every type as a type alias so the public API is unchanged.
package galleryerrs

import (
	"errors"
	"fmt"
	"net/http"
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
func (e *AuthenticationError) Unwrap() error      { return e.Cause }
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
func (e *AuthorizationError) Unwrap() error      { return e.Cause }
func (e *AuthorizationError) isExtractionError() {}

// NotFoundError is returned for deleted tweets, suspended accounts, DMCA
// takedowns, geo-blocked content, or any other permanent unavailability.
// Reason distinguishes the cause without requiring a new type per case.
type NotFoundError struct {
	URL    string
	Reason string // "deleted" | "suspended" | "protected" | "tombstone" | "dmca" | "geo-blocked" | "gone" | ""
}

func (e *NotFoundError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("unavailable (%s): %s", e.Reason, e.URL)
	}
	return fmt.Sprintf("not found: %s", e.URL)
}
func (e *NotFoundError) isExtractionError() {}

// ChallengeError indicates Twitter is showing a verification challenge (CAPTCHA
// / unlock flow) and the request cannot proceed.
type ChallengeError struct {
	URL string
}

func (e *ChallengeError) Error() string {
	return fmt.Sprintf("Twitter challenge required for %s - log in via a browser to resolve", e.URL)
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
// function. It is NOT an ExtractionError.
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

// StopExtraction asks the extractor to stop after the current page / batch.
var StopExtraction = errors.New("stop extraction")

// AbortExtraction cancels the current extraction immediately.
var AbortExtraction = errors.New("abort extraction")

// TerminateExtraction hard-cancels everything, including in-flight downloads.
var TerminateExtraction = errors.New("terminate extraction")

// ClassifyHTTPStatus maps an HTTP status code to a typed extraction error.
// This is the single source of truth for HTTP-status → typed-error mapping
// used by both extractors and the downloader.
func ClassifyHTTPStatus(status int, url string, body []byte) error {
	switch {
	case status == http.StatusUnauthorized: // 401
		return &AuthenticationError{}
	case status == http.StatusForbidden: // 403
		return &AuthorizationError{URL: url}
	case status == http.StatusNotFound: // 404
		return &NotFoundError{URL: url, Reason: "deleted"}
	case status == http.StatusGone: // 410
		return &NotFoundError{URL: url, Reason: "gone"}
	case status == http.StatusUnavailableForLegalReasons: // 451
		return &NotFoundError{URL: url, Reason: "dmca"}
	case status == http.StatusTooManyRequests: // 429
		return &RateLimitError{Endpoint: url}
	default:
		return &HttpError{StatusCode: status, URL: url, Body: body}
	}
}
