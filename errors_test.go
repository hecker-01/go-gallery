package gallery

import (
	"errors"
	"testing"
	"time"
)

func TestHttpError(t *testing.T) {
	err := &HttpError{StatusCode: 404, URL: "https://example.com/test"}
	if err.Error() == "" {
		t.Error("HttpError.Error() should not be empty")
	}
	if !errors.As(err, new(*HttpError)) {
		t.Error("errors.As should match *HttpError")
	}
	// HttpError implements ExtractionError
	var ee ExtractionError
	if !errors.As(err, &ee) {
		t.Error("HttpError should implement ExtractionError")
	}
}

func TestAuthenticationError_Unwrap(t *testing.T) {
	cause := errors.New("bad cookie")
	err := &AuthenticationError{Cause: cause}
	if !errors.Is(err, cause) {
		t.Error("errors.Is should find wrapped cause")
	}
}

func TestAuthorizationError_Unwrap(t *testing.T) {
	cause := errors.New("protected account")
	err := &AuthorizationError{URL: "https://twitter.com/user", Cause: cause}
	if !errors.Is(err, cause) {
		t.Error("errors.Is should find wrapped cause")
	}
}

func TestNotFoundError(t *testing.T) {
	err := &NotFoundError{URL: "https://twitter.com/status/0"}
	if err.Error() == "" {
		t.Error("NotFoundError.Error() should not be empty")
	}
	var ee ExtractionError
	if !errors.As(err, &ee) {
		t.Error("NotFoundError should implement ExtractionError")
	}
}

func TestRateLimitError(t *testing.T) {
	reset := time.Now().Add(15 * time.Minute)
	err := &RateLimitError{
		Endpoint:  "UserTweets",
		Limit:     500,
		Remaining: 0,
		ResetAt:   reset,
	}
	if err.Error() == "" {
		t.Error("RateLimitError.Error() should not be empty")
	}
	var target *RateLimitError
	if !errors.As(err, &target) {
		t.Error("errors.As should match *RateLimitError")
	}
	if target.Endpoint != "UserTweets" {
		t.Errorf("Endpoint: got %q, want %q", target.Endpoint, "UserTweets")
	}
}

func TestInputError(t *testing.T) {
	err := &InputError{Field: "url", Message: "must not be empty"}
	if err.Error() == "" {
		t.Error("InputError.Error() should not be empty")
	}
	// InputError is NOT an ExtractionError.
	var ee ExtractionError
	if errors.As(err, &ee) {
		t.Error("InputError should NOT implement ExtractionError")
	}
}

func TestControlErrors_Is(t *testing.T) {
	for _, sentinel := range []error{StopExtraction, AbortExtraction, TerminateExtraction} {
		if !errors.Is(sentinel, sentinel) {
			t.Errorf("errors.Is(%v, %v) should be true", sentinel, sentinel)
		}
		if errors.Is(sentinel, errors.New("other")) {
			t.Errorf("errors.Is(%v, other) should be false", sentinel)
		}
	}
	// They must be distinct.
	if errors.Is(StopExtraction, AbortExtraction) {
		t.Error("StopExtraction and AbortExtraction should be distinct")
	}
	if errors.Is(AbortExtraction, TerminateExtraction) {
		t.Error("AbortExtraction and TerminateExtraction should be distinct")
	}
}
