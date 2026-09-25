package gallery

import "github.com/hecker-01/go-gallery/internal/ratelimit"

// RateLimitRegistry coordinates one authentication session across clients.
// Construct it with NewRateLimitRegistry; do not copy it.
type RateLimitRegistry struct{ registry *ratelimit.Registry }
type RateLimitSnapshot = ratelimit.Snapshot

func NewRateLimitRegistry() *RateLimitRegistry              { return &RateLimitRegistry{ratelimit.New(nil)} }
func (r *RateLimitRegistry) Snapshots() []RateLimitSnapshot { return r.registry.Snapshots() }
func WithRateLimitRegistry(r *RateLimitRegistry) Option {
	return func(c *Client) {
		if r != nil {
			c.rlRegistry = r.registry
		}
	}
}
