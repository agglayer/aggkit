package bridgeservice

import (
	"encoding/json"
	"math/big"
	"net/http"
	"net/url"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseNetworkIDSliceParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		queryParams    string
		expectedResult []uint32
		expectedError  string
	}{
		{
			name:           "valid network IDs within limit",
			queryParams:    "network_ids=1&network_ids=2&network_ids=3",
			expectedResult: []uint32{1, 2, 3},
		},
		{
			name:           "exactly 5 network IDs (at limit)",
			queryParams:    "network_ids=1&network_ids=2&network_ids=3&network_ids=4&network_ids=5",
			expectedResult: []uint32{1, 2, 3, 4, 5},
		},
		{
			name:          "too many network IDs (exceeds limit)",
			queryParams:   "network_ids=1&network_ids=2&network_ids=3&network_ids=4&network_ids=5&network_ids=6",
			expectedError: "too many network IDs provided: maximum 5 allowed, got 6",
		},
		{
			name:          "invalid network ID",
			queryParams:   "network_ids=1&network_ids=abc",
			expectedError: "invalid network ID 'abc':",
		},
		{
			name:           "empty parameter",
			queryParams:    "",
			expectedResult: []uint32{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(nil)
			c.Request = &http.Request{
				URL: &url.URL{RawQuery: tt.queryParams},
			}

			result, err := parseNetworkIDSliceParam(c, networkIDsParam)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedResult, result)
			}
		})
	}
}

func TestParseBigIntQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name           string
		queryParams    string
		expectedResult *big.Int
		expectedError  string
	}{
		{
			name:           "valid number",
			queryParams:    "global_index=1000000",
			expectedResult: big.NewInt(1000000),
		},
		{
			name:           "empty parameter",
			queryParams:    "",
			expectedResult: nil,
		},
		{
			name:          "invalid input",
			queryParams:   "global_index=invalid",
			expectedError: "invalid global_index parameter, it should be a numeric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(nil)
			c.Request = &http.Request{
				URL: &url.URL{RawQuery: tt.queryParams},
			}

			result, err := parseBigIntQuery(c)

			if tt.expectedError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
				assert.Nil(t, result)
			} else {
				require.NoError(t, err)
				if tt.expectedResult == nil {
					assert.Nil(t, result)
				} else {
					require.NotNil(t, result)
					assert.Equal(t, 0, tt.expectedResult.Cmp(result),
						"expected %s, got %s", tt.expectedResult.String(), result.String())
				}
			}
		})
	}
}

// TestNewBridgeResponse_GlobalIndexMarshalsAsQuotedString verifies that NewBridgeResponse
// produces a BridgeResponse whose global_index marshals as a quoted JSON string, even for an
// L1-origin deposit whose global index (networkID==0, depositCount==0 encodes to 2^64) exceeds
// JavaScript's Number.MAX_SAFE_INTEGER. Before this fix, GlobalIndex was *big.Int, whose
// MarshalJSON emits bare decimal digits despite the swagger declaring global_index as a string,
// silently corrupting the value for any JSON.parse consumer. Both the presence of the quoted
// form and the absence of the unquoted form are asserted, since asserting only the former would
// still pass if the bare-number form were also present.
func TestNewBridgeResponse_GlobalIndexMarshalsAsQuotedString(t *testing.T) {
	bridge := &bridgesync.Bridge{
		OriginNetwork:      0,
		DestinationNetwork: 1,
		OriginAddress:      common.HexToAddress("0x1"),
		DestinationAddress: common.HexToAddress("0x2"),
		Amount:             common.Big0,
		DepositCount:       0,
		TxnSender:          common.HexToAddress("0x3"),
		ToAddress:          common.HexToAddress("0x4"),
	}

	// networkID=0 (mainnet) and etrogL1UpgradeBlock=0 (disabled) with bridge.DepositCount=0
	// yields bridgesync.GenerateGlobalIndexForNetworkID(0, 0) == 2^64 == 18446744073709551616.
	response := NewBridgeResponse(bridge, 0, 0)

	data, err := json.Marshal(response)
	require.NoError(t, err)

	require.Contains(t, string(data), `"global_index":"18446744073709551616"`,
		"global_index must marshal as a quoted string")
	require.NotContains(t, string(data), `"global_index":18446744073709551616`,
		"global_index must never marshal as a bare JSON number")
}
