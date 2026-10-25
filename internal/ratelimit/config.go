package ratelimit

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
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

// Rule is one limit scope: a default or one named keyed scenario.
type Rule struct {
	Limit        int           `json:"limit"`
	Interval     DurationSec   `json:"interval"`
	BucketKey    string        `json:"bucket_key"`
	MatchHeaders []HeaderMatch `json:"match_headers,omitempty"`
}

func (r Rule) valid() bool {
	return r.Limit > 0 && time.Duration(r.Interval) > 0
}

// MatchesHeaders reports whether headers satisfy the rule's match set
// (OR across entries). Empty MatchHeaders means always match.
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

// Config is a keyed set of limit rules: the single typed plugin config.
//
//   - "default" is the reserved fallback. It applies to every request that
//     matches no other rule (and to every route without a per-route
//     override). Its match_headers, if any, are ignored.
//   - Every other key names one scenario (e.g. "free-plan", "per-tenant").
//     A keyed rule must carry a valid limit/interval; invalid entries are
//     skipped. Selection, in sorted key order: explicit match_headers (when
//     declared) must match; otherwise the bucket_key header's presence
//     selects the rule — but only when the bucket partitions differently
//     from the default's (comparison is case-insensitive). A rule sharing
//     the default's bucket must declare match_headers; without them it is
//     skipped and default is referred.
//
// Per-route overrides use the same struct via Envoy's typed_per_filter_config
// and replace the listener config outright (RouteConfigWins).
type Config struct {
	Default Rule            `json:"default"`
	Rules   map[string]Rule `json:"rules,omitempty"`
}

// DefaultRuleKey is the reserved fallback key. A "default" entry inside
// Rules is ignored to protect the reserved word.
const DefaultRuleKey = "default"

func (c Config) valid() bool {
	return c.Default.valid()
}

// ParseConfig parses and returns config; malformed JSON errors.
// Invalid limits/intervals are not errors here — callers treat invalid
// configs as no-limiting (fail-open).
func ParseConfig(data []byte) (*Config, error) {
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, err
	}
	if c.Rules == nil {
		c.Rules = map[string]Rule{}
	}
	return &c, nil
}

// Resolve picks the authoritative rule for a request: the first keyed rule
// (sorted key order) that is valid, declares matchers, and matches h — else
// the default. scopeKey identifies the counter as route/ruleKey.
func (c *Config) Resolve(route string, h http.Header) (*Rule, string) {
	if c == nil {
		return nil, "global/" + DefaultRuleKey
	}
	if route == "" {
		route = "global"
	}
	keys := make([]string, 0, len(c.Rules))
	for k := range c.Rules {
		if k == DefaultRuleKey {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r := c.Rules[k]
		if !r.valid() {
			continue
		}
		if len(r.MatchHeaders) > 0 {
			if r.MatchesHeaders(h) {
				rr := r
				return &rr, route + "/" + k
			}
			continue
		}
		// No matchers: the bucket_key header's presence selects the rule,
		// but only when it partitions differently from the default. A rule
		// sharing the default's bucket (header names are case-insensitive)
		// cannot distinguish itself by presence, so it must declare
		// match_headers; otherwise it is skipped and default is referred.
		if r.BucketKey == "" || strings.EqualFold(r.BucketKey, c.Default.BucketKey) {
			continue
		}
		if h.Get(r.BucketKey) == "" {
			continue
		}
		rr := r
		return &rr, route + "/" + k
	}
	rr := c.Default
	return &rr, route + "/" + DefaultRuleKey
}
