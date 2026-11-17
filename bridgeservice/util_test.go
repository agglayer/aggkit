package bridgeservice

import (
	"encoding/json"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"strings"
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
		{
			name:           "zero",
			queryParams:    "global_index=0",
			expectedResult: big.NewInt(0),
		},
		{
			name:           "max uint64 (rollup origin)",
			queryParams:    "global_index=18446744073709551615",
			expectedResult: new(big.Int).SetUint64(math.MaxUint64),
		},
		{
			name:           "2^64 (mainnet flag, deposit count 0)",
			queryParams:    "global_index=18446744073709551616",
			expectedResult: new(big.Int).Lsh(big.NewInt(1), 64),
		},
		{
			name:           "2^64 + 2^32 - 1 (largest valid index)",
			queryParams:    "global_index=18446744078004518911",
			expectedResult: maxGlobalIndex,
		},
		{
			name:          "2^64 + 2^32 (above the largest valid index)",
			queryParams:   "global_index=18446744078004518912",
			expectedError: "value out of range",
		},
		{
			name:          "negative",
			queryParams:   "global_index=-1",
			expectedError: "only decimal digits are allowed",
		},
		{
			name:          "plus sign",
			queryParams:   "global_index=%2B1",
			expectedError: "only decimal digits are allowed",
		},
		{
			name:          "hex prefix",
			queryParams:   "global_index=0x10",
			expectedError: "only decimal digits are allowed",
		},
		{
			name:          "binary prefix",
			queryParams:   "global_index=0b10",
			expectedError: "only decimal digits are allowed",
		},
		{
			name:          "octal prefix",
			queryParams:   "global_index=0o10",
			expectedError: "only decimal digits are allowed",
		},
		{
			name:          "underscore separator",
			queryParams:   "global_index=1_000",
			expectedError: "only decimal digits are allowed",
		},
		{
			name:          "leading zero",
			queryParams:   "global_index=010",
			expectedError: "leading zeros are not allowed",
		},
		{
			name:          "21 digits",
			queryParams:   "global_index=100000000000000000000",
			expectedError: "too many digits",
		},
		{
			name:          "1000 digits",
			queryParams:   "global_index=" + strings.Repeat("9", 1000),
			expectedError: "too many digits",
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

func TestParseAmountParam(t *testing.T) {
	maxUint256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))

	tests := []struct {
		name           string
		input          string
		expectedResult *big.Int
		expectedError  string
	}{
		{name: "zero", input: "0", expectedResult: big.NewInt(0)},
		{name: "one ether", input: "1000000000000000000", expectedResult: big.NewInt(1000000000000000000)},
		{name: "2^256 - 1", input: maxUint256.String(), expectedResult: maxUint256},
		{name: "2^256", input: new(big.Int).Lsh(big.NewInt(1), 256).String(), expectedError: "value out of range"},
		{name: "79 digits", input: "1" + strings.Repeat("0", 78), expectedError: "too many digits"},
		{name: "1000 digits", input: strings.Repeat("9", 1000), expectedError: "too many digits"},
		{name: "empty", input: "", expectedError: "empty value"},
		{name: "negative", input: "-1", expectedError: "only decimal digits are allowed"},
		{name: "hex prefix", input: "0x10", expectedError: "only decimal digits are allowed"},
		{name: "not a number", input: "abc", expectedError: "only decimal digits are allowed"},
		{name: "leading zero", input: "01", expectedError: "leading zeros are not allowed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseAmountParam(tt.input)
			if tt.expectedError != "" {
				require.ErrorContains(t, err, tt.expectedError)
				require.Nil(t, result)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 0, tt.expectedResult.Cmp(result),
				"expected %s, got %s", tt.expectedResult.String(), result.String())
		})
	}
}
