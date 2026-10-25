package ratelimit

import (
	"fmt"
	"net/http"
	"time"

	"github.com/ardikabs/gonvoy"
)

const sharedBucket = "__shared__"

// SetRateLimitHeaders sets the exact standard headers (overwrites any upstream values).
// Retry-After is intentionally excluded: it is only set on 429 responses.
func SetRateLimitHeaders(c gonvoy.Context, limit, remaining, resetSec, windowSec int) {
	if windowSec < 1 {
		windowSec = 1
	}
	if resetSec < 1 {
		resetSec = 1
	}

	c.Response().Header.Set("RateLimit-Limit", itoa(limit))
	c.Response().Header.Set("RateLimit-Remaining", itoa(remaining))
	c.Response().Header.Set("RateLimit-Reset", itoa(resetSec))
	c.Response().Header.Set("RateLimit-Policy", fmt.Sprintf("%d;w=%d", limit, windowSec))
	c.Response().Header.Set("RateLimit", fmt.Sprintf("limit=%d, remaining=%d, reset=%d", limit, remaining, resetSec))
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// RateLimit returns a middleware enforcing cfg against store.
// cfg is the already-merged rule for this route: the listener default,
// or the route's typed_per_filter_config override (RouteConfigWins).
// Counters stay per (route, bucket): scope is the Envoy route name so two
// routes with identical limits still count independently.
// Fail-open: any invalid config or panic passes through via next(c).
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
			var hdr http.Header
			if req := c.Request(); req != nil {
				hdr = req.Header
			}
			routeName, _ := c.Attr("xds.route_name", "")
			rule, scope := cfg.Resolve(routeName, hdr)
			if rule == nil || !rule.valid() {
				return next(c)
			}
			// Note: no Matches re-check here. Resolve already selected the
			// authoritative rule (first matching keyed rule, else default),
			// and default applies unconditionally.
			bucket := ""
			if rule.BucketKey != "" {
				bucket = c.Request().Header.Get(rule.BucketKey)
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
				c.Response().Header.Set("Retry-After", itoa(resetSec))
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
