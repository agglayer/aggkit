package apitypes

import (
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// invalidUintValue is a non-numeric placeholder used across parser error test cases below.
const invalidUintValue = "abc"

func ctxWithQuery(t *testing.T, query url.Values) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query.Encode(), nil)
	return c
}

func TestParseRequestFilterValid(t *testing.T) {
	query := url.Values{}
	query.Set("source_network", "1")
	query.Set("origin_network", "0")
	query.Set("destination_network", "10")
	query.Set("status", autoclaimtypes.RequestStatusDryRun.String())
	query.Set("policy_result", autoclaimtypes.PolicyResultApproved.String())
	query.Set("bridge_tx_hash", common.HexToHash("0x1").Hex())
	query.Set("claim_tx_hash", common.HexToHash("0x2").Hex())
	query.Set("from_block", "100")
	query.Set("to_block", "200")
	query.Set("page_number", "2")
	query.Set("page_size", "50")

	filter, err := ParseRequestFilter(ctxWithQuery(t, query))
	require.NoError(t, err)
	require.NotNil(t, filter.SourceNetwork)
	require.Equal(t, uint32(1), *filter.SourceNetwork)
	require.NotNil(t, filter.OriginNetwork)
	require.Equal(t, uint32(10), *filter.DestinationNetwork)
	require.Equal(t, autoclaimtypes.RequestStatusDryRun, *filter.Status)
	require.Equal(t, autoclaimtypes.PolicyResultApproved, *filter.PolicyResult)
	require.NotNil(t, filter.BridgeTxHash)
	require.NotNil(t, filter.ClaimTxHash)
	require.Equal(t, uint64(100), *filter.FromBlock)
	require.Equal(t, uint64(200), *filter.ToBlock)
	require.Equal(t, uint32(2), filter.PageNumber)
	require.Equal(t, uint32(50), filter.PageSize)
}

func TestParseRequestFilterErrors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		key   string
		value string
	}{
		{"source", "source_network", invalidUintValue},
		{"origin", "origin_network", invalidUintValue},
		{"destination", "destination_network", "-1"},
		{"status", "status", "bogus"},
		{"policy", "policy_status", "bogus"},
		{"bridge hash", "bridge_tx_hash", "0x123"},
		{"from block", "from_block", invalidUintValue},
		{"page number", "page_number", invalidUintValue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := url.Values{}
			query.Set(tc.key, tc.value)
			_, err := ParseRequestFilter(ctxWithQuery(t, query))
			require.Error(t, err)
		})
	}
}

func TestParseRequestFilterRejectsOversizedPageSize(t *testing.T) {
	query := url.Values{}
	query.Set("page_size", "100000")
	_, err := ParseRequestFilter(ctxWithQuery(t, query))
	require.ErrorContains(t, err, "page_size")
}

func TestEffectivePageSize(t *testing.T) {
	require.Equal(t, autoclaimtypes.DefaultRequestPageSize, EffectivePageSize(0))
	require.Equal(t, uint32(7), EffectivePageSize(7))
}

func TestNewRequestResponseMapsDecisions(t *testing.T) {
	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	claimTx := common.HexToHash("0xabc")
	request := autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.RequestKey("0:10:5"),
		Status:      autoclaimtypes.RequestStatusConfirmed,
		GlobalIndex: big.NewInt(42),
		ClaimTxHash: &claimTx,
		Bridge: autoclaimtypes.BridgeExit{
			SourceNetwork:      0,
			OriginNetwork:      0,
			DestinationNetwork: 10,
			DepositCount:       5,
			TxHash:             common.HexToHash("0x1"),
			OriginAddress:      common.HexToAddress("0x2"),
			DestinationAddress: common.HexToAddress("0x3"),
			ToAddress:          common.HexToAddress("0x4"),
			TxnSender:          common.HexToAddress("0x5"),
			Amount:             big.NewInt(100),
			Metadata:           []byte{0x01},
		},
		PolicyDecision: &autoclaimtypes.PolicyDecision{
			PolicyName: "api-approve",
			Result:     autoclaimtypes.PolicyResultManual,
			CreatedAt:  now,
			UpdatedAt:  now,
		},
		ManualDecision: &autoclaimtypes.PolicyDecision{
			PolicyName: "manual",
			Result:     autoclaimtypes.PolicyResultApproved,
			Reason:     "ok",
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}

	response := NewRequestResponse(request)
	require.Equal(t, "0:10:5", response.ID)
	require.Equal(t, autoclaimtypes.RequestStatusConfirmed.String(), response.Status)
	require.Equal(t, uint32(0), response.SourceNetwork)
	require.Empty(t, response.LER)
	require.Equal(t, "42", response.GlobalIndex)
	require.Equal(t, "0x01", response.Metadata)
	require.NotNil(t, response.ClaimTxHash)
	require.Equal(t, claimTx.Hex(), *response.ClaimTxHash)
	require.Equal(t, autoclaimtypes.PolicyResultManual.String(), response.PolicyStatus)
	require.NotNil(t, response.PolicyDecision)
	require.NotNil(t, response.ManualDecision)
	require.Equal(t, "ok", response.ManualDecision.Reason)
}

// TestNewRequestResponseMapsSourceNetworkAndLER exercises a rollup-origin request (S06/S07) whose
// SourceNetwork differs from the bridged token's OriginNetwork, and whose LER is set — the two new
// fields this step (S11) surfaces on RequestResponse.
func TestNewRequestResponseMapsSourceNetworkAndLER(t *testing.T) {
	request := autoclaimtypes.AutoClaimRequest{
		Key:    autoclaimtypes.RequestKey("1:0:7"),
		Status: autoclaimtypes.RequestStatusDetected,
		Bridge: autoclaimtypes.BridgeExit{
			SourceNetwork:      1,
			OriginNetwork:      5,
			DestinationNetwork: 0,
			DepositCount:       7,
			TxHash:             common.HexToHash("0x1"),
			OriginAddress:      common.HexToAddress("0x2"),
			DestinationAddress: common.HexToAddress("0x3"),
			ToAddress:          common.HexToAddress("0x4"),
			TxnSender:          common.HexToAddress("0x5"),
			Amount:             big.NewInt(100),
		},
		LER: common.HexToHash("0xdeadbeef"),
	}

	response := NewRequestResponse(request)
	require.Equal(t, "1:0:7", response.ID)
	require.Equal(t, uint32(1), response.SourceNetwork)
	require.Equal(t, uint32(5), response.OriginNetwork)
	require.Equal(t, common.HexToHash("0xdeadbeef").Hex(), response.LER)
}
