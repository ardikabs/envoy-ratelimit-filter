//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/ardikabs/gonvoy/pkg/suite"
	"github.com/stretchr/testify/require"
)

var RatelimitCases = []suite.TestCase{
	{
		Name:        "RatelimitHeaderDriven",
		FilterName:  "ratelimit",
		Description: "Global limit, per-route override, key isolation, standard headers.",
		Test: func(t *testing.T, kit *suite.TestSuiteKit) {
			stop := kit.StartEnvoy(t)
			defer stop()
			base := kit.GetEnvoyHost()

			getH := func(path string, headers map[string]string) *http.Response {
				req, err := http.NewRequest(http.MethodGet, base+path, nil)
				require.NoError(t, err)
				for k, v := range headers {
					if v != "" {
						req.Header.Set(k, v)
					}
				}
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				return resp
			}

			get := func(path, apiKey string) *http.Response {
				return getH(path, map[string]string{"x-api-key": apiKey})
			}

			t.Run("global N+1 denied with headers", func(t *testing.T) {
				for i := 0; i < 5; i++ {
					resp := get("/", "")
					require.Equal(t, http.StatusOK, resp.StatusCode)
					require.Equal(t, "5", resp.Header.Get("RateLimit-Limit"))
				}
				resp := get("/", "")
				require.Equal(t, 429, resp.StatusCode)
				require.Equal(t, "5", resp.Header.Get("RateLimit-Limit"))
				require.Equal(t, "0", resp.Header.Get("RateLimit-Remaining"))
				require.NotEmpty(t, resp.Header.Get("RateLimit-Reset"))
				require.NotEmpty(t, resp.Header.Get("Retry-After"))
				require.Contains(t, resp.Header.Get("RateLimit-Policy"), "5;w=60")
				require.Contains(t, resp.Header.Get("RateLimit"), "limit=5")
			})

			t.Run("per-route override isolated", func(t *testing.T) {
				key := "route-iso-key"
				for i := 0; i < 2; i++ {
					resp := get("/premium", key)
					require.Equal(t, http.StatusOK, resp.StatusCode)
					require.Equal(t, "2", resp.Header.Get("RateLimit-Limit"))
				}
				resp := get("/premium", key)
				require.Equal(t, 429, resp.StatusCode)
				require.Equal(t, "0", resp.Header.Get("RateLimit-Remaining"))

				// Same key on global scope still allowed (different scope).
				resp2 := get("/", key)
				require.Equal(t, http.StatusOK, resp2.StatusCode)
			})

			t.Run("different keys independent", func(t *testing.T) {
				for i := 0; i < 5; i++ {
					resp := get("/", "iso-a")
					require.Equal(t, http.StatusOK, resp.StatusCode)
				}
				resp := get("/", "iso-a")
				require.Equal(t, 429, resp.StatusCode)
				respB := get("/", "iso-b")
				require.Equal(t, http.StatusOK, respB.StatusCode)
				require.NotEqual(t, "0", respB.Header.Get("RateLimit-Remaining"))
			})

			t.Run("listener keyed rule selected by matcher", func(t *testing.T) {
				hdr := map[string]string{"x-plan": "legacy", "x-api-key": "legacy-key"}
				resp := getH("/", hdr)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "1", resp.Header.Get("RateLimit-Limit"))

				resp = getH("/", hdr)
				require.Equal(t, 429, resp.StatusCode)
				require.Equal(t, "1", resp.Header.Get("RateLimit-Limit"))
				require.Equal(t, "0", resp.Header.Get("RateLimit-Remaining"))

				// Same key without the matcher hits the default instead.
				resp = get("/", "legacy-key")
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "5", resp.Header.Get("RateLimit-Limit"))
			})

			t.Run("per-route multi keyed rules", func(t *testing.T) {
				hdr := map[string]string{"x-tenant-id": "tenant-1"}
				resp := getH("/premium", hdr)
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "1", resp.Header.Get("RateLimit-Limit"))

				resp = getH("/premium", hdr)
				require.Equal(t, 429, resp.StatusCode)
				require.Equal(t, "1", resp.Header.Get("RateLimit-Limit"))

				// Same route without the tenant matcher hits the route default.
				resp = get("/premium", "route-multi-key")
				require.Equal(t, http.StatusOK, resp.StatusCode)
				require.Equal(t, "2", resp.Header.Get("RateLimit-Limit"))
			})

			t.Run("backoff headers parseable", func(t *testing.T) {
				resp := get("/", "backoff-1")
				require.Equal(t, http.StatusOK, resp.StatusCode)
				reset, err := strconv.Atoi(resp.Header.Get("RateLimit-Reset"))
				require.NoError(t, err)
				require.Greater(t, reset, 0)
				require.True(t, strings.Contains(resp.Header.Get("RateLimit-Policy"), ";w="))
				require.Empty(t, resp.Header.Get("Retry-After"))
			})
		},
	},
}

func TestRatelimit(t *testing.T) {
	suite.NewTestSuite(suite.TestSuiteOptions{
		FilterLocation: "./filters/{filter}/{filename}",
	}).Run(t, RatelimitCases)
}
