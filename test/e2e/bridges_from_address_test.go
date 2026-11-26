package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// bridgesFromAddressTarget is a REST endpoint that serves GET /bridge/v1/bridges and the networks to ask it for.
type bridgesFromAddressTarget struct {
	name       string
	baseURL    string
	networkIDs []uint32
}

// getBridgesRaw performs GET baseURL/bridge/v1/<endpoint>?<query> and returns the status code and the body. The
// typed client always sends a well formed request, so the malformed values of this test need a raw one.
func getBridgesRaw(
	ctx context.Context, t *testing.T, baseURL, endpoint string, query url.Values,
) (int, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/bridge/v1/%s?%s", baseURL, endpoint, query.Encode()), http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// TestBridgesMalformedAddressFilterIsRejected covers a malformed address filter: GET /bridge/v1/bridges silently dropped a
// from_address that was not a hex address, answering HTTP 200 with the general page of bridges as if it were the
// sender's. The same went for origin_token_address of GET /bridge/v1/token-mappings. A malformed address filter
// must be an HTTP 400 naming the parameter, a well formed address that matches nothing must be an empty 200 (not
// the unfiltered page), and an empty value must stay the explicit no-filter case.
//
// It asks the bridge service of aggkit-001 directly (its L1 and its L2 syncer) and, when the env has one, the
// aggkit-proxy, which forwards the status and the body of the bridge service it routes to. The values are not
// compared with what the env holds (which depends on what ran before): what tells the fixed behavior from the
// old one is the status code, whatever the page would have been.
//
// Run it against the anvil env, after building the image the env runs (aggkit:local), with
//
//	make build-docker
//	make test-e2e TEST_RUN='^TestBridgesMalformedAddressFilterIsRejected$'
func TestBridgesMalformedAddressFilterIsRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	require.NotNil(t, testEnv, "testEnv must be set by TestMain")

	targets := []bridgesFromAddressTarget{
		{name: "bridge service", baseURL: bridgeServiceBaseURL, networkIDs: []uint32{0, testEnv.L2.NetworkID}},
	}
	if testEnv.Clients.BridgeServiceProxyURL != "" {
		targets = append(targets, bridgesFromAddressTarget{
			name:       "aggkit-proxy",
			baseURL:    testEnv.Clients.BridgeServiceProxyURL,
			networkIDs: []uint32{testEnv.L2.NetworkID},
		})
	}

	// a sender that never bridged anything: a valid filter that matches nothing
	unknownKey, err := crypto.GenerateKey()
	require.NoError(t, err)
	unknownAddr := crypto.PubkeyToAddress(unknownKey.PublicKey).Hex()

	malformed := []string{"not-an-address", "0x123", "0xZZZZaF64b29273B7D567FCFc40544c014EEe9970"}

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	for _, target := range targets {
		for _, networkID := range target.networkIDs {
			t.Run(fmt.Sprintf("%s network %d", target.name, networkID), func(t *testing.T) {
				networkIDStr := strconv.Itoa(int(networkID))

				for _, value := range malformed {
					status, body := getBridgesRaw(ctx, t, target.baseURL, "bridges", url.Values{
						"network_id": {networkIDStr}, "from_address": {value},
					})
					require.Equal(t, http.StatusBadRequest, status,
						"from_address=%q must be rejected, not ignored; body (first 300 bytes): %.300s", value, body)
					var errResp types.ErrorResponse
					require.NoError(t, json.Unmarshal(body, &errResp), "body: %s", body)
					require.Contains(t, errResp.Error, "from_address")

					status, body = getBridgesRaw(ctx, t, target.baseURL, "token-mappings", url.Values{
						"network_id": {networkIDStr}, "origin_token_address": {value},
					})
					require.Equal(t, http.StatusBadRequest, status,
						"origin_token_address=%q must be rejected, not ignored; body (first 300 bytes): %.300s", value, body)
					require.NoError(t, json.Unmarshal(body, &errResp), "body: %s", body)
					require.Contains(t, errResp.Error, "origin_token_address")
				}

				// a well formed address nobody bridged from: an empty result, not the unfiltered page
				status, body := getBridgesRaw(ctx, t, target.baseURL, "bridges", url.Values{
					"network_id": {networkIDStr}, "from_address": {unknownAddr},
				})
				require.Equal(t, http.StatusOK, status, "body: %s", body)
				var result types.BridgesResult
				require.NoError(t, json.Unmarshal(body, &result), "body: %s", body)
				require.Zero(t, result.Count, "a sender that never bridged has no bridges")
				require.Empty(t, result.Bridges)

				// an empty from_address is still the no-filter case
				status, body = getBridgesRaw(ctx, t, target.baseURL, "bridges", url.Values{
					"network_id": {networkIDStr}, "from_address": {""},
				})
				require.Equal(t, http.StatusOK, status, "body: %s", body)
			})
		}
	}
}
