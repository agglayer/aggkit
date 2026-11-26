package client

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequireNoNullEntries(t *testing.T) {
	type entry struct{ value int }

	require.NoError(t, requireNoNullEntries[entry]("entries", nil))
	require.NoError(t, requireNoNullEntries("entries", []*entry{}))
	require.NoError(t, requireNoNullEntries("entries", []*entry{{value: 1}, {value: 2}}))

	err := requireNoNullEntries("entries", []*entry{{value: 1}, nil, nil})
	require.ErrorIs(t, err, ErrInvalidResponse)
	require.ErrorContains(t, err, "entries[1] is null", "the first null entry is reported")
}

// TestClient_RejectsNullEntries verifies every method decoding a list of objects fails the whole
// response when the list holds a null entry, wherever it is, and accepts valid and empty lists
func TestClient_RejectsNullEntries(t *testing.T) {
	ctx := t.Context()
	calls := []struct {
		name string
		// field is the JSON name of the list
		field string
		call  func(c *Client) error
	}{
		{"GetBridges", "bridges", func(c *Client) error {
			_, err := c.GetBridges(ctx, GetBridgesParams{NetworkID: 1})
			return err
		}},
		{"GetClaims", "claims", func(c *Client) error {
			_, err := c.GetClaims(ctx, GetClaimsParams{NetworkID: 1})
			return err
		}},
		{"GetUnsetClaims", "unset_claims", func(c *Client) error {
			_, err := c.GetUnsetClaims(ctx, GetUnsetClaimsParams{})
			return err
		}},
		{"GetSetClaims", "set_claims", func(c *Client) error {
			_, err := c.GetSetClaims(ctx, GetSetClaimsParams{})
			return err
		}},
		{"GetTokenMappings", "token_mappings", func(c *Client) error {
			_, err := c.GetTokenMappings(ctx, GetTokenMappingsParams{NetworkID: 1})
			return err
		}},
		{"GetLegacyTokenMigrations", "legacy_token_migrations", func(c *Client) error {
			_, err := c.GetLegacyTokenMigrations(ctx, GetLegacyTokenMigrationsParams{NetworkID: 1})
			return err
		}},
		{"GetSettlements", "settlements", func(c *Client) error {
			_, err := c.GetSettlements(ctx, GetSettlementsParams{})
			return err
		}},
		{"GetRemoveGEREvents", "remove_ger_events", func(c *Client) error {
			_, err := c.GetRemoveGEREvents(ctx, GetRemoveGEREventsParams{})
			return err
		}},
		{"GetClaimsByGER", "claims", func(c *Client) error {
			_, err := c.GetClaimsByGER(ctx, 1, "0x01")
			return err
		}},
		{"GetBridgesByContent", "bridges", func(c *Client) error {
			_, err := c.GetBridgesByContent(ctx, GetBridgesByContentParams{NetworkID: 1})
			return err
		}},
	}

	for _, tt := range calls {
		t.Run(tt.name, func(t *testing.T) {
			bodies := map[string]string{
				"null entry":                       fmt.Sprintf(`{"%s":[null],"count":1}`, tt.field),
				"null entry after a valid one":     fmt.Sprintf(`{"%s":[{},null],"count":2}`, tt.field),
				"null entry before a valid one":    fmt.Sprintf(`{"%s":[null,{}],"count":2}`, tt.field),
				"null entry among many valid ones": fmt.Sprintf(`{"%s":[{},{},null,{}],"count":4}`, tt.field),
			}
			for name, body := range bodies {
				t.Run(name, func(t *testing.T) {
					server := serveBody(t, body)
					err := tt.call(New(Config{BaseURL: server.URL}))
					require.ErrorIs(t, err, ErrInvalidResponse)
					require.ErrorContains(t, err, tt.field)
				})
			}

			for name, body := range map[string]string{
				"valid entries": fmt.Sprintf(`{"%s":[{},{}],"count":2}`, tt.field),
				"empty list":    fmt.Sprintf(`{"%s":[],"count":0}`, tt.field),
				"null list":     fmt.Sprintf(`{"%s":null,"count":0}`, tt.field),
				"absent list":   `{"count":0}`,
			} {
				t.Run(name, func(t *testing.T) {
					server := serveBody(t, body)
					require.NoError(t, tt.call(New(Config{BaseURL: server.URL})))
				})
			}
		})
	}
}

// TestClient_NullEntriesAreOnlyInvalidOnSuccess verifies an error status keeps being reported as
// such, not as an invalid response
func TestClient_NullEntriesAreOnlyInvalidOnSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"claims":[null],"count":1}`))
	}))
	t.Cleanup(server.Close)

	_, err := New(Config{BaseURL: server.URL}).GetClaims(t.Context(), GetClaimsParams{NetworkID: 1})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidResponse)
	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr)
	require.Equal(t, http.StatusServiceUnavailable, statusErr.StatusCode)
}

// serveBody starts a server answering every request with HTTP 200 and body
func serveBody(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}
