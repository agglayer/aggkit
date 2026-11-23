package bridgesync_test

// These tests exercise the bridge service HTTP handlers on top of a real bridge syncer database
// (SQLite, all the migrations applied) instead of a mocked Bridger. They live in this package, and not in
// bridgeservice, because the seeded syncer is built by an export_test.go helper (the processor is
// unexported); an external test package keeps bridgeservice -> bridgesync from becoming an import cycle.
//
// Scenario: A non-empty address filter that is not a hex address (from_address of
// GET /bridge/v1/bridges, origin_token_address of GET /bridge/v1/token-mappings) used to be dropped by the
// SQL filter builders, so the caller got HTTP 200 with the general, unfiltered page and count as if it
// matched the requested address. It must be an HTTP 400 instead, and a valid address that matches nothing
// must stay a 200 with an empty result.

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice"
	bridgeservicemocks "github.com/agglayer/aggkit/bridgeservice/mocks"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	addressFilterMainnetNetworkID = uint32(0)
	addressFilterL2NetworkID      = uint32(7)
)

var (
	addressFilterSenderA = common.HexToAddress("0xE34aaF64b29273B7D567FCFc40544c014EEe9970")
	addressFilterSenderB = common.HexToAddress("0xd34aaF64b29273B7D567FCFc40544c014EEe9970")
	// addressFilterUnknown is a valid address that is the sender / origin token of nothing
	addressFilterUnknown = common.HexToAddress("0x1111111111111111111111111111111111111111")

	addressFilterTokenA = common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48")
	addressFilterTokenB = common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7")
	addressFilterTokenC = common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F")

	// addressFilterMalformed are non-empty values that are not hex addresses
	addressFilterMalformed = []string{
		"not-an-address",
		"0x123",
		"0x", // the prefix alone
		"0x12345678901234567890123456789012345678",    // hex, but 19 bytes instead of 20
		"0xZZZZaF64b29273B7D567FCFc40544c014EEe9970",  // right length, not hex
		"0xE34aaF64b29273B7D567FCFc40544c014EEe99700", // one char too long
		" ", // whitespace is not "no filter"
		"0xE34aaF64b29273B7D567FCFc40544c014EEe9970 ",      // trailing space
		"'; DROP TABLE bridge; --",                         // not an injection vector, but must not get through either
		"0xE34aaF64b29273B7D567FCFc40544c014EEe9970' OR 1", // valid address followed by SQL
	}
)

// addressFilterBridges are 6 bridges: deposit counts 0, 1 and 3 sent by sender A, 2 and 4 by sender B and 5 by
// nobody known (from_address NULL, as a bridge synced with SyncFromInBridges=false). TxHash is
// deposit_count+1 so the responses can be told apart.
func addressFilterBridges() []*bridgesync.Bridge {
	senders := []*common.Address{
		&addressFilterSenderA, &addressFilterSenderA, &addressFilterSenderB,
		&addressFilterSenderA, &addressFilterSenderB, nil,
	}
	bridges := make([]*bridgesync.Bridge, 0, len(senders))
	for i, sender := range senders {
		bridges = append(bridges, &bridgesync.Bridge{
			BlockNum:           uint64(i + 1),
			FromAddress:        sender,
			TxHash:             common.BigToHash(big.NewInt(int64(i + 1))),
			DestinationNetwork: 10,
			Amount:             big.NewInt(1),
			Metadata:           []byte{},
			DepositCount:       uint32(i),
		})
	}
	return bridges
}

// addressFilterTokenMappings are 3 token mappings with different origin token addresses.
func addressFilterTokenMappings() []*bridgesync.TokenMapping {
	tokens := []common.Address{addressFilterTokenA, addressFilterTokenB, addressFilterTokenC}
	mappings := make([]*bridgesync.TokenMapping, 0, len(tokens))
	for i, token := range tokens {
		mappings = append(mappings, &bridgesync.TokenMapping{
			BlockNum:            uint64(i + 1),
			TxHash:              common.BigToHash(big.NewInt(int64(i + 1))),
			OriginNetwork:       1,
			OriginTokenAddress:  token,
			WrappedTokenAddress: common.BigToAddress(big.NewInt(int64(i + 1))),
			Metadata:            []byte{},
		})
	}
	return mappings
}

// newAddressFilterRouter returns the router of a bridge service whose L1 and L2 bridge syncers are real, seeded
// SQLite-backed BridgeSync instances.
func newAddressFilterRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	bridgeL1 := bridgesync.NewSeededBridgeSyncForTest(t, addressFilterBridges(), addressFilterTokenMappings())
	bridgeL2 := bridgesync.NewSeededBridgeSyncForTest(t, addressFilterBridges(), addressFilterTokenMappings())

	upgradeQuerier := bridgeservicemocks.NewAgglayerManagerUpgradeQuerier(t)
	upgradeQuerier.EXPECT().GetUpgradeBlock(mock.Anything, mock.Anything).Return(uint64(0)).Maybe()

	service := bridgeservice.New(
		&bridgeservice.Config{
			Logger:      log.WithFields("module", "address-filter-test"),
			ReadTimeout: 10 * time.Second,
			NetworkID:   addressFilterL2NetworkID,
		},
		upgradeQuerier,
		bridgeservicemocks.NewL1InfoTreeSyncer(t),
		bridgeservicemocks.NewL2GERSyncer(t),
		bridgeL1,
		bridgeservicemocks.NewClaimer(t),
		bridgeL2,
		bridgeservicemocks.NewClaimer(t),
	)
	router := gin.New()
	service.RegisterRoutes(router)
	return router
}

func addressFilterGet(t *testing.T, router *gin.Engine, endpoint string, query url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/%s?%s", bridgeservice.BridgeV1Prefix, endpoint, query.Encode()), http.NoBody)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// requireBadRequestNamingParam asserts the response is an HTTP 400 whose JSON error names the parameter. The
// status assertion carries the start of the body, so on a regression (HTTP 200 with a page of rows) it shows
// what was served.
func requireBadRequestNamingParam(t *testing.T, w *httptest.ResponseRecorder, param string) {
	t.Helper()
	require.Equal(t, http.StatusBadRequest, w.Code,
		"a malformed %s must be rejected, not ignored; response body (first 300 bytes): %.300s", param, w.Body.String())

	var response map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Contains(t, response["error"], param)
	require.NotContains(t, response, "bridges", "no rows must be served for a rejected request")
	require.NotContains(t, response, "token_mappings", "no rows must be served for a rejected request")
}

func decodeBridges(t *testing.T, w *httptest.ResponseRecorder) *bridgeservicetypes.BridgesResult {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var result bridgeservicetypes.BridgesResult
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	return &result
}

func bridgeTxHashes(result *bridgeservicetypes.BridgesResult) []bridgeservicetypes.Hash {
	hashes := make([]bridgeservicetypes.Hash, 0, len(result.Bridges))
	for _, bridge := range result.Bridges {
		hashes = append(hashes, bridge.TxHash)
	}
	return hashes
}

func txHashesOf(depositCounts ...int) []bridgeservicetypes.Hash {
	hashes := make([]bridgeservicetypes.Hash, 0, len(depositCounts))
	for _, depositCount := range depositCounts {
		hashes = append(hashes, bridgeservicetypes.Hash(common.BigToHash(big.NewInt(int64(depositCount+1))).Hex()))
	}
	return hashes
}

// TestGetBridgesFromAddressFilterOverHTTP: GET /bridge/v1/bridges?from_address=... on a real syncer database,
// for both the L1 and the L2 bridge syncer.
func TestGetBridgesFromAddressFilterOverHTTP(t *testing.T) {
	networks := map[string]uint32{
		"L1 syncer": addressFilterMainnetNetworkID,
		"L2 syncer": addressFilterL2NetworkID,
	}

	for name, networkID := range networks {
		t.Run(name, func(t *testing.T) {
			router := newAddressFilterRouter(t)
			query := func(extra url.Values) url.Values {
				q := url.Values{networkIDParam: []string{strconv.Itoa(int(networkID))}}
				for key, values := range extra {
					q[key] = values
				}
				return q
			}
			get := func(extra url.Values) *httptest.ResponseRecorder {
				return addressFilterGet(t, router, "bridges", query(extra))
			}

			t.Run("a malformed from_address is a 400, not the unfiltered page", func(t *testing.T) {
				for _, malformed := range addressFilterMalformed {
					t.Run(fmt.Sprintf("%q", malformed), func(t *testing.T) {
						requireBadRequestNamingParam(t,
							get(url.Values{fromAddressParam: []string{malformed}}), fromAddressParam)
					})
				}
			})

			t.Run("a malformed from_address is a 400 whatever the other filters are", func(t *testing.T) {
				extras := map[string]url.Values{
					"deposit_count":     {depositCountParam: []string{"1"}},
					"destination ids":   {networkIDsParam: []string{"10"}},
					"page_size":         {pageSizeParam: []string{"2"}},
					"page_number+valid": {pageNumberParam: []string{"1"}, pageSizeParam: []string{"10"}},
				}
				for name, extra := range extras {
					t.Run(name, func(t *testing.T) {
						extra[fromAddressParam] = []string{"not-an-address"}
						requireBadRequestNamingParam(t, get(extra), fromAddressParam)
					})
				}
			})

			t.Run("the rejected requests did not alter the data", func(t *testing.T) {
				for _, malformed := range addressFilterMalformed {
					requireBadRequestNamingParam(t,
						get(url.Values{fromAddressParam: []string{malformed}}), fromAddressParam)
				}
				result := decodeBridges(t, get(nil))
				require.Equal(t, 6, result.Count)
			})

			t.Run("an omitted from_address is the explicit no-filter case", func(t *testing.T) {
				result := decodeBridges(t, get(nil))
				require.Equal(t, 6, result.Count)
				require.Equal(t, txHashesOf(5, 4, 3, 2, 1, 0), bridgeTxHashes(result))
			})

			t.Run("an empty from_address is the explicit no-filter case", func(t *testing.T) {
				result := decodeBridges(t, get(url.Values{fromAddressParam: []string{""}}))
				require.Equal(t, 6, result.Count)
				require.Equal(t, txHashesOf(5, 4, 3, 2, 1, 0), bridgeTxHashes(result))
			})

			t.Run("a valid from_address filters, whatever its case", func(t *testing.T) {
				senderA := map[string]string{
					"checksum":   addressFilterSenderA.Hex(),
					"lower case": "0x" + common.Bytes2Hex(addressFilterSenderA.Bytes()),
				}
				for name, fromAddress := range senderA {
					t.Run(name, func(t *testing.T) {
						result := decodeBridges(t, get(url.Values{fromAddressParam: []string{fromAddress}}))
						require.Equal(t, 3, result.Count)
						require.Equal(t, txHashesOf(3, 1, 0), bridgeTxHashes(result))
						for _, bridge := range result.Bridges {
							require.NotNil(t, bridge.FromAddress)
							require.Equal(t, bridgeservicetypes.Address(addressFilterSenderA.Hex()), *bridge.FromAddress)
						}
					})
				}

				result := decodeBridges(t, get(url.Values{fromAddressParam: []string{addressFilterSenderB.Hex()}}))
				require.Equal(t, 2, result.Count)
				require.Equal(t, txHashesOf(4, 2), bridgeTxHashes(result))
			})

			t.Run("a valid from_address is combined with the other filters", func(t *testing.T) {
				result := decodeBridges(t, get(url.Values{
					fromAddressParam:  []string{addressFilterSenderA.Hex()},
					depositCountParam: []string{"1"},
					pageSizeParam:     []string{"10"},
					pageNumberParam:   []string{"1"},
					networkIDsParam:   []string{"10"},
				}))
				require.Equal(t, 1, result.Count)
				require.Equal(t, txHashesOf(1), bridgeTxHashes(result))
			})

			t.Run("a valid from_address that matches nothing is an empty result, not the unfiltered page", func(t *testing.T) {
				result := decodeBridges(t, get(url.Values{fromAddressParam: []string{addressFilterUnknown.Hex()}}))
				require.Equal(t, 0, result.Count)
				require.Empty(t, result.Bridges)
			})
		})
	}
}

// TestGetTokenMappingsOriginTokenAddressFilterOverHTTP: GET /bridge/v1/token-mappings?origin_token_address=...
// on a real syncer database, which had the same silent drop of a malformed filter as from_address.
func TestGetTokenMappingsOriginTokenAddressFilterOverHTTP(t *testing.T) {
	networks := map[string]uint32{
		"L1 syncer": addressFilterMainnetNetworkID,
		"L2 syncer": addressFilterL2NetworkID,
	}

	for name, networkID := range networks {
		t.Run(name, func(t *testing.T) {
			router := newAddressFilterRouter(t)
			get := func(extra url.Values) *httptest.ResponseRecorder {
				q := url.Values{networkIDParam: []string{strconv.Itoa(int(networkID))}}
				for key, values := range extra {
					q[key] = values
				}
				return addressFilterGet(t, router, "token-mappings", q)
			}
			decode := func(w *httptest.ResponseRecorder) *bridgeservicetypes.TokenMappingsResult {
				require.Equal(t, http.StatusOK, w.Code, w.Body.String())
				var result bridgeservicetypes.TokenMappingsResult
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
				return &result
			}

			t.Run("a malformed origin_token_address is a 400, not the unfiltered page", func(t *testing.T) {
				for _, malformed := range addressFilterMalformed {
					t.Run(fmt.Sprintf("%q", malformed), func(t *testing.T) {
						requireBadRequestNamingParam(t,
							get(url.Values{originTokenAddrParam: []string{malformed}}), originTokenAddrParam)
					})
				}
			})

			t.Run("an omitted or empty origin_token_address is the explicit no-filter case", func(t *testing.T) {
				require.Equal(t, 3, decode(get(nil)).Count)
				require.Equal(t, 3, decode(get(url.Values{originTokenAddrParam: []string{""}})).Count)
			})

			t.Run("a valid origin_token_address filters, whatever its case", func(t *testing.T) {
				for name, origin := range map[string]string{
					"checksum":   addressFilterTokenB.Hex(),
					"lower case": "0x" + common.Bytes2Hex(addressFilterTokenB.Bytes()),
				} {
					t.Run(name, func(t *testing.T) {
						result := decode(get(url.Values{originTokenAddrParam: []string{origin}}))
						require.Equal(t, 1, result.Count)
						require.Len(t, result.TokenMappings, 1)
						require.Equal(t, bridgeservicetypes.Address(addressFilterTokenB.Hex()),
							result.TokenMappings[0].OriginTokenAddress)
					})
				}
			})

			t.Run("a valid origin_token_address that matches nothing is an empty result", func(t *testing.T) {
				result := decode(get(url.Values{originTokenAddrParam: []string{addressFilterUnknown.Hex()}}))
				require.Equal(t, 0, result.Count)
				require.Empty(t, result.TokenMappings)
			})
		})
	}
}

// the query parameter names of the handlers under test (bridgeservice does not export them)
const (
	networkIDParam       = "network_id"
	networkIDsParam      = "network_ids"
	pageNumberParam      = "page_number"
	pageSizeParam        = "page_size"
	depositCountParam    = "deposit_count"
	fromAddressParam     = "from_address"
	originTokenAddrParam = "origin_token_address"
)
