package ratelimit

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseConfig_GlobalAndRoutes(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{"default":{"limit":2,"interval":"60s","bucket_key":"x-api-key"},"routes":{"premium":{"limit":5,"interval":60,"bucket_key":"x-api-key"}}}`))
	require.NoError(t, err)
	require.Equal(t, 2, cfg.Default.Limit)
	require.Equal(t, "premium", func() string { _, sk := cfg.Resolve("premium"); return sk }())
}
