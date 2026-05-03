package gallery

import "github.com/hecker-01/go-gallery/internal/galleryerrs"

// ExtractionError is the base interface for failures originating in extractors
// or the download pipeline. All concrete error types below implement it.
type ExtractionError = galleryerrs.ExtractionError

// HttpError is returned when an HTTP response has an unexpected status code.
type HttpError = galleryerrs.HttpError

// AuthenticationError indicates invalid or missing credentials (expired
// session, wrong auth_token / ct0 cookies).
type AuthenticationError = galleryerrs.AuthenticationError

// AuthorizationError indicates the authenticated user is not permitted to view
// the requested resource (protected account, not following).
type AuthorizationError = galleryerrs.AuthorizationError

// NotFoundError is returned for deleted tweets, suspended accounts, DMCA
// takedowns, geo-blocked content, or any other permanent unavailability.
// The Reason field distinguishes the cause without requiring a new type per case.
type NotFoundError = galleryerrs.NotFoundError

// ChallengeError indicates Twitter is showing a verification challenge (CAPTCHA
// / unlock flow) and the request cannot proceed.
type ChallengeError = galleryerrs.ChallengeError

// RateLimitError carries the full context of a 429 response or an internal
// rate-limit trip.
type RateLimitError = galleryerrs.RateLimitError

// InputError is returned for invalid configuration or bad input to a public API
// function. It is not an ExtractionError.
type InputError = galleryerrs.InputError

// ClassifyHTTPStatus maps an HTTP status code to a typed extraction error.
var ClassifyHTTPStatus = galleryerrs.ClassifyHTTPStatus

// StopExtraction asks the extractor to stop after the current page / batch.
// Items already in-flight are completed; no new pages are fetched.
var StopExtraction = galleryerrs.StopExtraction

// AbortExtraction cancels the current extraction immediately. In-flight
// downloads that have already started are allowed to finish.
var AbortExtraction = galleryerrs.AbortExtraction

// TerminateExtraction hard-cancels everything, including in-flight downloads.
var TerminateExtraction = galleryerrs.TerminateExtraction
