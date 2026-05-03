package gallery

import (
	"errors"
	"net/http"
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

func TestNotFoundError_Reason(t *testing.T) {
	cases := []struct {
		reason  string
		wantSub string
	}{
		{"deleted", "deleted"},
		{"suspended", "suspended"},
		{"dmca", "dmca"},
		{"gone", "gone"},
		{"tombstone", "tombstone"},
		{"", "not found"}, // empty reason uses the old message format
	}
	for _, tc := range cases {
		err := &NotFoundError{URL: "https://twitter.com/status/1", Reason: tc.reason}
		msg := err.Error()
		if msg == "" {
			t.Errorf("reason %q: Error() empty", tc.reason)
		}
		found := false
		for i := 0; i <= len(msg)-len(tc.wantSub); i++ {
			if msg[i:i+len(tc.wantSub)] == tc.wantSub {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("reason %q: Error() = %q, want substring %q", tc.reason, msg, tc.wantSub)
		}
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

func TestClassifyHTTPStatus(t *testing.T) {
	cases := []struct {
		status   int
		wantType string
	}{
		{http.StatusUnauthorized, "*gallery.AuthenticationError"},
		{http.StatusForbidden, "*gallery.AuthorizationError"},
		{http.StatusNotFound, "*gallery.NotFoundError"},
		{http.StatusGone, "*gallery.NotFoundError"},
		{http.StatusUnavailableForLegalReasons, "*gallery.NotFoundError"},
		{http.StatusTooManyRequests, "*gallery.RateLimitError"},
		{http.StatusInternalServerError, "*gallery.HttpError"},
		{http.StatusBadGateway, "*gallery.HttpError"},
	}
	for _, tc := range cases {
		err := ClassifyHTTPStatus(tc.status, "https://example.com/", nil)
		if err == nil {
			t.Errorf("status %d: expected non-nil error", tc.status)
			continue
		}
		// Verify the concrete type.
		switch tc.status {
		case http.StatusUnauthorized:
			var target *AuthenticationError
			if !errors.As(err, &target) {
				t.Errorf("status %d: want *AuthenticationError, got %T", tc.status, err)
			}
		case http.StatusForbidden:
			var target *AuthorizationError
			if !errors.As(err, &target) {
				t.Errorf("status %d: want *AuthorizationError, got %T", tc.status, err)
			}
		case http.StatusNotFound, http.StatusGone, http.StatusUnavailableForLegalReasons:
			var target *NotFoundError
			if !errors.As(err, &target) {
				t.Errorf("status %d: want *NotFoundError, got %T", tc.status, err)
			}
		case http.StatusTooManyRequests:
			var target *RateLimitError
			if !errors.As(err, &target) {
				t.Errorf("status %d: want *RateLimitError, got %T", tc.status, err)
			}
		default:
			var target *HttpError
			if !errors.As(err, &target) {
				t.Errorf("status %d: want *HttpError, got %T", tc.status, err)
			}
		}
	}
}

func TestClassifyHTTPStatus_Reasons(t *testing.T) {
	cases := []struct {
		status     int
		wantReason string
	}{
		{http.StatusNotFound, "deleted"},
		{http.StatusGone, "gone"},
		{http.StatusUnavailableForLegalReasons, "dmca"},
	}
	for _, tc := range cases {
		err := ClassifyHTTPStatus(tc.status, "https://example.com/", nil)
		var nfe *NotFoundError
		if !errors.As(err, &nfe) {
			t.Errorf("status %d: expected *NotFoundError", tc.status)
			continue
		}
		if nfe.Reason != tc.wantReason {
			t.Errorf("status %d: Reason = %q, want %q", tc.status, nfe.Reason, tc.wantReason)
		}
	}
}
