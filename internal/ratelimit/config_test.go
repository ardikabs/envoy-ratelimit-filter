package ratelimit

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseConfig_BareDefault(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"default":{"limit":2,"interval":"60s","bucket_key":"x-api-key"}}`))
	require.NoError(t, err)
	require.Equal(t, 2, cfg.Default.Limit)
	require.Equal(t, "x-api-key", cfg.Default.BucketKey)
	require.True(t, cfg.Default.valid())
}

func TestParseConfig_IntervalForms(t *testing.T) {
	num, err := ParseConfig([]byte(`{"default":{"limit":5,"interval":60}}`))
	require.NoError(t, err)
	require.True(t, num.Default.valid())

	bare, err := ParseConfig([]byte(`{"default":{"limit":5,"interval":"60"}}`))
	require.NoError(t, err)
	require.True(t, bare.Default.valid())

	dur, err := ParseConfig([]byte(`{"default":{"limit":5,"interval":"1m"}}`))
	require.NoError(t, err)
	require.True(t, dur.Default.valid())
}

func TestResolve_SelectsFirstMatchingRule(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s","bucket_key":"x-api-key"},
		"rules": {
			"free": {"limit":10,"interval":"60s","bucket_key":"x-api-key","match_headers":[{"name":"x-plan","exact":"free"}]},
			"tenant": {"limit":50,"interval":"60s","bucket_key":"x-tenant-id","match_headers":[{"name":"x-tenant-id"}]}
		}
	}`))
	require.NoError(t, err)

	rule, scope := cfg.Resolve("api", http.Header{"X-Plan": []string{"free"}})
	require.Equal(t, 10, rule.Limit)
	require.Equal(t, "api/free", scope)

	rule, scope = cfg.Resolve("api", http.Header{"X-Tenant-Id": []string{"t1"}})
	require.Equal(t, 50, rule.Limit)
	require.Equal(t, "api/tenant", scope)

	rule, scope = cfg.Resolve("api", http.Header{})
	require.Equal(t, 100, rule.Limit)
	require.Equal(t, "api/default", scope)
}

func TestResolve_SkipsInvalidRules(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s"},
		"rules": {
			"bad-limit": {"limit":0,"interval":"60s","match_headers":[{"name":"x-plan","exact":"x"}]}
		}
	}`))
	require.NoError(t, err)

	rule, scope := cfg.Resolve("", http.Header{"X-Plan": []string{"x"}})
	require.Equal(t, 100, rule.Limit)
	require.Equal(t, "global/default", scope)
}

func TestResolve_SameBucketRuleRequiresMatchers(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s","bucket_key":"x-api-key"},
		"rules": {
			"v2": {"limit":1,"interval":"60s","bucket_key":"x-api-key"},
			"cased": {"limit":1,"interval":"60s","bucket_key":"X-API-KEY"}
		}
	}`))
	require.NoError(t, err)

	// Same bucket as default (even differing only by case) without matchers:
	// skipped, default is always referred.
	rule, scope := cfg.Resolve("api", http.Header{"X-Api-Key": []string{"k1"}})
	require.Equal(t, 100, rule.Limit)
	require.Equal(t, "api/default", scope)
}

func TestResolve_BucketPresenceSelectsRule(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s","bucket_key":"x-api-key"},
		"rules": {
			"tenant": {"limit":50,"interval":"60s","bucket_key":"x-tenant-id"}
		}
	}`))
	require.NoError(t, err)

	// Matcherless rule: bucket header present selects it.
	rule, scope := cfg.Resolve("api", http.Header{"X-Tenant-Id": []string{"t1"}})
	require.Equal(t, 50, rule.Limit)
	require.Equal(t, "api/tenant", scope)

	// Bucket header absent: falls through to default, not the tenant rule.
	rule, scope = cfg.Resolve("api", http.Header{"X-Api-Key": []string{"k1"}})
	require.Equal(t, 100, rule.Limit)
	require.Equal(t, "api/default", scope)
}

func TestResolve_DeterministicOrder(t *testing.T) {
	// Two rules matching the same request: sorted-first key wins, every time.
	cfg, err := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s"},
		"rules": {
			"zebra": {"limit":1,"interval":"60s","match_headers":[{"name":"x-flag","exact":"on"}]},
			"apple": {"limit":2,"interval":"60s","match_headers":[{"name":"x-flag","exact":"on"}]}
		}
	}`))
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		rule, scope := cfg.Resolve("r", http.Header{"X-Flag": []string{"on"}})
		require.Equal(t, 2, rule.Limit)
		require.Equal(t, "r/apple", scope)
	}
}

func TestResolve_ReservedDefaultKeyIgnored(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"default": {"limit":100,"interval":"60s"},
		"rules": {"default": {"limit":1,"interval":"60s","match_headers":[{"name":"x-a","exact":"b"}]}}
	}`))
	require.NoError(t, err)

	rule, _ := cfg.Resolve("r", http.Header{"X-A": []string{"b"}})
	require.Equal(t, 100, rule.Limit)
}

func TestConfig_InvalidDefaultIsFailOpen(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"default":{"limit":0,"interval":"60s"}}`))
	require.NoError(t, err)
	require.False(t, cfg.Default.valid())
}
