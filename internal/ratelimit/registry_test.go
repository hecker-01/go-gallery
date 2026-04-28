package ratelimit

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

func makeResp(limit, remaining int, resetAt time.Time) *http.Response {
	h := http.Header{}
	h.Set("x-rate-limit-limit", itoa(limit))
	h.Set("x-rate-limit-remaining", itoa(remaining))
	h.Set("x-rate-limit-reset", itoa64(resetAt.Unix()))
	return &http.Response{Header: h}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	return fmt.Sprint(n)
}

func itoa64(n int64) string {
	return fmt.Sprint(n)
}

func TestRegistry_Update_Status(t *testing.T) {
	reg := New(nil)
	reset := time.Now().Add(15 * time.Minute)
	reg.Update("UserTweets", makeResp(500, 450, reset))

	s := reg.Status("UserTweets")
	if s.Limit != 500 {
		t.Errorf("Limit: got %d, want 500", s.Limit)
	}
	if s.Remaining != 450 {
		t.Errorf("Remaining: got %d, want 450", s.Remaining)
	}
	if s.ResetAt.Unix() != reset.Unix() {
		t.Errorf("ResetAt: got %v, want %v", s.ResetAt, reset)
	}
}

func TestRegistry_Status_UnknownEndpoint(t *testing.T) {
	reg := New(nil)
	s := reg.Status("UnknownOp")
	if s.Endpoint != "UnknownOp" {
		t.Errorf("Endpoint: got %q", s.Endpoint)
	}
	if s.Limit != 0 || s.Remaining != 0 {
		t.Error("unknown endpoint should have zero limit/remaining")
	}
}

func TestRegistry_Wait_HasRemaining(t *testing.T) {
	reg := New(nil)
	reset := time.Now().Add(15 * time.Minute)
	reg.Update("SearchTimeline", makeResp(50, 49, reset))

	delay := reg.Wait("SearchTimeline", time.Now())
	if delay != 0 {
		t.Errorf("Wait should return 0 when remaining > 0, got %v", delay)
	}
	// Remaining should have decremented.
	s := reg.Status("SearchTimeline")
	if s.Remaining != 48 {
		t.Errorf("Remaining after Wait: got %d, want 48", s.Remaining)
	}
}

func TestRegistry_Wait_Exhausted(t *testing.T) {
	reg := New(nil)
	reset := time.Now().Add(5 * time.Second)
	reg.Update("TweetDetail", makeResp(150, 0, reset))

	delay := reg.Wait("TweetDetail", time.Now())
	if delay <= 0 {
		t.Errorf("Wait should return positive delay when remaining=0, got %v", delay)
	}
}

func TestRegistry_On429_Callback(t *testing.T) {
	called := make(chan string, 1)
	cb := func(endpoint string, _ time.Time) {
		called <- endpoint
	}
	reg := New(cb)
	reset := time.Now().Add(30 * time.Second)
	reg.On429("Bookmarks", reset)

	select {
	case ep := <-called:
		if ep != "Bookmarks" {
			t.Errorf("callback got %q, want Bookmarks", ep)
		}
	case <-time.After(time.Second):
		t.Error("callback was not called")
	}

	s := reg.Status("Bookmarks")
	if s.Remaining != 0 {
		t.Errorf("Remaining after On429: got %d, want 0", s.Remaining)
	}
}

func TestRegistry_Update_Callback(t *testing.T) {
	called := make(chan string, 1)
	reg := New(func(ep string, _ time.Time) {
		select {
		case called <- ep:
		default:
		}
	})
	reg.Update("HomeTimeline", makeResp(25, 20, time.Now().Add(time.Minute)))
	select {
	case ep := <-called:
		if ep != "HomeTimeline" {
			t.Errorf("got %q, want HomeTimeline", ep)
		}
	case <-time.After(time.Second):
		t.Error("callback not called")
	}
}
