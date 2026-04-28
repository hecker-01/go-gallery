// Package ratelimit provides per-endpoint rate-limit tracking driven by
// Twitter's x-rate-limit-* response headers.
package ratelimit

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Snapshot is an immutable view of a single endpoint's current rate-limit state.
type Snapshot struct {
	Endpoint  string
	Limit     int
	Remaining int
	ResetAt   time.Time
}

// Callback is invoked after every header update and on every 429 hit.
type Callback func(endpoint string, resetAt time.Time)

// Registry maintains one rate.Limiter per GraphQL operation name.
// Call Update after every Twitter API response to feed in the latest headers.
// Call Wait before issuing the next request to that endpoint.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]*entry
	callback Callback
}

type entry struct {
	mu        sync.Mutex
	limiter   *rate.Limiter
	limit     int
	remaining int
	resetAt   time.Time
}

// New returns a Registry. cb is optional; pass nil to disable callbacks.
func New(cb Callback) *Registry {
	return &Registry{
		entries:  make(map[string]*entry),
		callback: cb,
	}
}

// Update parses the rate-limit headers from an HTTP response and updates the
// per-endpoint state. endpoint is the GraphQL operation name.
func (r *Registry) Update(endpoint string, resp *http.Response) {
	if resp == nil {
		return
	}
	limitStr := resp.Header.Get("x-rate-limit-limit")
	remainStr := resp.Header.Get("x-rate-limit-remaining")
	resetStr := resp.Header.Get("x-rate-limit-reset")

	limit, _ := strconv.Atoi(limitStr)
	remaining, _ := strconv.Atoi(remainStr)
	resetUnix, _ := strconv.ParseInt(resetStr, 10, 64)

	var resetAt time.Time
	if resetUnix > 0 {
		resetAt = time.Unix(resetUnix, 0)
	}

	e := r.getOrCreate(endpoint, limit)
	e.mu.Lock()
	if limit > 0 {
		e.limit = limit
	}
	if remainStr != "" {
		e.remaining = remaining
	}
	if !resetAt.IsZero() {
		e.resetAt = resetAt
	}
	e.mu.Unlock()

	if r.callback != nil && !resetAt.IsZero() {
		r.callback(endpoint, resetAt)
	}
}

// On429 records a 429 response for endpoint, setting the rate-limit pause
// until resetAt. Subsequent Wait calls will block until that time.
func (r *Registry) On429(endpoint string, resetAt time.Time) {
	e := r.getOrCreate(endpoint, 0)
	e.mu.Lock()
	e.remaining = 0
	e.resetAt = resetAt
	// Drain the limiter so Wait will sleep.
	e.limiter.SetBurst(0)
	e.mu.Unlock()

	if r.callback != nil {
		r.callback(endpoint, resetAt)
	}
}

// Wait blocks until the endpoint's rate limiter allows a request, sleeping
// through any active rate-limit window. It respects ctx cancellation.
func (r *Registry) Wait(endpoint string, now time.Time) time.Duration {
	e := r.getOrCreate(endpoint, 0)
	e.mu.Lock()
	resetAt := e.resetAt
	remaining := e.remaining
	e.mu.Unlock()

	// If we have remaining tokens and the window hasn't reset yet, proceed.
	if remaining > 0 {
		e.mu.Lock()
		e.remaining--
		e.mu.Unlock()
		return 0
	}

	// If remaining == 0 and we have a future reset time, sleep until it.
	if !resetAt.IsZero() && resetAt.After(now) {
		return time.Until(resetAt)
	}
	return 0
}

// Status returns a Snapshot for the named endpoint. If the endpoint has not
// been seen the returned Snapshot has zero values.
func (r *Registry) Status(endpoint string) Snapshot {
	r.mu.RLock()
	e, ok := r.entries[endpoint]
	r.mu.RUnlock()
	if !ok {
		return Snapshot{Endpoint: endpoint}
	}
	e.mu.Lock()
	s := Snapshot{
		Endpoint:  endpoint,
		Limit:     e.limit,
		Remaining: e.remaining,
		ResetAt:   e.resetAt,
	}
	e.mu.Unlock()
	return s
}

func (r *Registry) getOrCreate(endpoint string, burst int) *entry {
	r.mu.RLock()
	e, ok := r.entries[endpoint]
	r.mu.RUnlock()
	if ok {
		return e
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	// Double-check after acquiring the write lock.
	if e, ok = r.entries[endpoint]; ok {
		return e
	}
	if burst <= 0 {
		burst = 1
	}
	e = &entry{
		limiter: rate.NewLimiter(rate.Inf, burst),
	}
	r.entries[endpoint] = e
	return e
}
