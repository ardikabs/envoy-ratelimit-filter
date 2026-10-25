package ratelimit

import (
	"fmt"
	"time"

	"github.com/ardikabs/gonvoy"
)

const sharedBucket = "__shared__"

// SetRateLimitHeaders sets the exact standard headers (overwrites any upstream values).
func SetRateLimitHeaders(c gonvoy.Context, limit, remaining, resetSec, windowSec int) {
	if windowSec < 1 {
		windowSec = 1
	}
	if resetSec < 1 {
		resetSec = 1
	}
	c.SetHeader("RateLimit-Limit", itoa(limit))
	c.SetHeader("RateLimit-Remaining", itoa(remaining))
	c.SetHeader("RateLimit-Reset", itoa(resetSec))
	c.SetHeader("RateLimit-Policy", fmt.Sprintf("%d;w=%d", limit, windowSec))
	c.SetHeader("RateLimit", fmt.Sprintf("limit=%d, remaining=%d, reset=%d", limit, remaining, resetSec))
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// RateLimit returns a middleware enforcing cfg against store.
// Fail-open: any invalid config/rule or panic passes through via next(c).
func RateLimit(cfg *Config, store *Store) gonvoy.Middleware {
	return func(next gonvoy.Handler) gonvoy.Handler {
		return gonvoy.Handler(func(c gonvoy.Context) (err error) {
			defer func() {
				if r := recover(); r != nil {
					_ = next(c)
					err = nil
				}
			}()
			if cfg == nil || store == nil {
				return next(c)
			}
			routeName, _ := c.Attr("xds.route_name", "")
			rule, scope := cfg.Resolve(routeName)
			if rule == nil || !rule.valid() {
				return next(c)
			}
			if req := c.Request(); req != nil {
				if !rule.MatchesHeaders(req.Header) {
					return next(c)
				}
			}
			bucket := ""
			if rule.BucketKey != "" {
				bucket = c.Header(rule.BucketKey)
			}
			if bucket == "" {
				bucket = sharedBucket
			}
			interval := time.Duration(rule.Interval)
			windowSec := int(interval.Seconds())
			if windowSec < 1 {
				windowSec = 1
			}
			allowed, remaining, resetSec := store.Allow(scope, bucket, rule.Limit, interval, time.Now())
			if !allowed {
				SetRateLimitHeaders(c, rule.Limit, 0, resetSec, windowSec)
				c.SetHeader("Retry-After", itoa(resetSec))
				c.Stats().Counter("ratelimit_denied_total").Increment(1)
				c.Log().Info("rate limit exceeded", "scope", scope, "limit", rule.Limit)
				return c.String(429, "rate limit exceeded")
			}
			if err := next(c); err != nil {
				return err
			}
			SetRateLimitHeaders(c, rule.Limit, remaining, resetSec, windowSec)
			c.Stats().Counter("ratelimit_allowed_total").Increment(1)
			return nil
		})
	}
}
