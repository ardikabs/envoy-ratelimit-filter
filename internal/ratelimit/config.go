package ratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DurationSec is seconds-precision interval accepting JSON number (seconds)
// or JSON string (Go duration like "60s", "1m", or bare number "60").
type DurationSec time.Duration

func (d *DurationSec) UnmarshalJSON(b []byte) error {
	var f float64
	if err := json.Unmarshal(b, &f); err == nil {
		*d = DurationSec(time.Duration(f * float64(time.Second)))
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("invalid interval %s", string(b))
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty interval")
	}
	if dur, err := time.ParseDuration(s); err == nil {
		*d = DurationSec(dur)
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		*d = DurationSec(time.Duration(f * float64(time.Second)))
		return nil
	}
	return fmt.Errorf("invalid interval %q", s)
}

// HeaderMatch matches one request header by exact value or prefix.
// If both Exact and Prefix are set, Exact wins.
type HeaderMatch struct {
	Name   string `json:"name"`
	Exact  string `json:"exact,omitempty"`
	Prefix string `json:"prefix,omitempty"`
}

// Matches reports whether h satisfies this entry (OR semantics across entries is handled by caller).
func (m HeaderMatch) Matches(h http.Header) bool {
	if m.Name == "" {
		return false
	}
	v := h.Get(m.Name)
	if v == "" {
		return false
	}
	if m.Exact != "" {
		return v == m.Exact
	}
	if m.Prefix != "" {
		return strings.HasPrefix(v, m.Prefix)
	}
	// Name-only entry means presence match.
	return true
}

// Rule is one limit scope.
type Rule struct {
	Limit        int          `json:"limit"`
	Interval     DurationSec  `json:"interval"`
	BucketKey    string       `json:"bucket_key"`
	MatchHeaders []HeaderMatch `json:"match_headers,omitempty"`
}

func (r Rule) valid() bool {
	return r.Limit > 0 && time.Duration(r.Interval) > 0
}

// MatchesHeaders reports whether headers satisfy the rule's match set.
// Empty MatchHeaders means always match (OR across entries otherwise).
func (r Rule) MatchesHeaders(h http.Header) bool {
	if len(r.MatchHeaders) == 0 {
		return true
	}
	for _, m := range r.MatchHeaders {
		if m.Matches(h) {
			return true
		}
	}
	return false
}

// Config holds the global default plus per-route overrides keyed by Envoy route name.
type Config struct {
	Default Rule            `json:"default"`
	Routes  map[string]Rule `json:"routes,omitempty"`
}

// ParseConfig parses and returns config; malformed JSON errors.
// Invalid limits/intervals are not errors here — callers treat invalid rules as no-limiting (fail-open per scope).
func ParseConfig(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Routes == nil {
		c.Routes = map[string]Rule{}
	}
	return &c, nil
}

// Resolve returns the authoritative rule for routeName, or default when missing/invalid.
// scopeKey is routeName for per-route hits, "global" otherwise.
func (c *Config) Resolve(routeName string) (*Rule, string) {
	if c == nil {
		return nil, "global"
	}
	if routeName != "" {
		if r, ok := c.Routes[routeName]; ok && r.valid() {
			rr := r
			return &rr, routeName
		}
	}
	rr := c.Default
	return &rr, "global"
}
