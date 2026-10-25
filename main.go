package main

import (
	"github.com/ardikabs/envoy-ratelimit-filter/internal/ratelimit"
	"github.com/ardikabs/gonvoy"
	"github.com/ardikabs/gonvoy/pkg/envoy"
)

var sharedStore = ratelimit.NewStore()

func ratelimitFilter(next gonvoy.Handler) gonvoy.Handler {
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
		gonvoy.Use(ratelimitFilter),
		gonvoy.WithConfig(new(ratelimit.Config)),
		// Route config replaces the listener default outright (no field
		// merging): a route with its own limit is authoritative for it.
		gonvoy.RouteConfigWins(),
		gonvoy.MetricPrefix("ratelimit_"),
	)
}

func main() {}
