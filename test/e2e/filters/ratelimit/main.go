// E2E fixture filter: same logic as repo root, built as filter.so for suitekit.
// Package main must stay registration-only; unit tests never import pkg/envoy.
package main

import (
	"github.com/ardikabs/envoy-ratelimit-filter/internal/ratelimit"
	"github.com/ardikabs/gonvoy"
	"github.com/ardikabs/gonvoy/pkg/envoy"
)

var sharedStore = ratelimit.NewStore()

func ratelimitWrapper(next gonvoy.Handler) gonvoy.Handler {
	return gonvoy.Handler(func(c gonvoy.Context) error {
		cfg, err := gonvoy.ConfigOf[*ratelimit.Config](c)
		if err != nil || cfg == nil {
			return next(c)
		}
		return ratelimit.RateLimit(cfg, sharedStore)(next)(c)
	})
}

func init() {
	envoy.Handle("ratelimit",
		gonvoy.Use(ratelimitWrapper),
		gonvoy.WithConfig(new(ratelimit.Config)),
		gonvoy.MetricPrefix("ratelimit_"),
	)
}

func main() {}
