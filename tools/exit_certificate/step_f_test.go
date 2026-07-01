package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRunStepF_WithBearerToken(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer my-iap-token", r.Header.Get("Authorization"))
		resp := jsonRPCResponse{
			JSONRPC: "2.0", ID: 1,
			Result: json.RawMessage(`{"balances":[]}`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := &Config{
		L2NetworkID: 1,
		Options: Options{
			UseAgglayerAdminToStepFCheck: true,
			AgglayerAdminURL:             server.URL,
			AgglayerAdminToken:           "my-iap-token",
		},
	}
	result, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
}

func TestRunStepF_MissingAdminURL_Error(t *testing.T) {
	t.Parallel()

	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: true}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agglayerAdminURL")
}

func TestRunStepF_DisabledNoLBT_Skips(t *testing.T) {
	t.Parallel()

	// useAgglayerAdminToStepFCheck=false and no LBT data: nothing to compare, so the step is
	// skipped with a benign all-match result and no RPC call (no agglayerAdminURL set).
	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
	result, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.AllMatch)
	require.Nil(t, result.CappedCertificate)
	require.Nil(t, result.TokenBalances)
}

func TestRunStepF_DisabledWithLBT_MatchOffline(t *testing.T) {
	t.Parallel()

	// useAgglayerAdminToStepFCheck=false but LBT data is available: compare LBT (step 0) totals
	// against the certificate bridge-exit sums, with no agglayer query and no agglayerAdminURL.
	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr},
				Amount:             big.NewInt(1000),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	lbt := []LBTEntry{{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "1000"}}

	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
	result, err := RunStepF(context.Background(), cfg, cert, lbt)
	require.NoError(t, err)
	require.True(t, result.AllMatch)
	require.Nil(t, result.TokenBalances)
	require.Len(t, result.Checks, 1)
	require.Equal(t, "1000", result.Checks[0].LBTAmount)
	require.Equal(t, "1000", result.Checks[0].CertificateAmount)
	require.Empty(t, result.Checks[0].AgglayerAmount)
}

func TestRunStepF_DisabledWithLBT_MismatchAborts(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr},
				Amount:             big.NewInt(1000),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	lbt := []LBTEntry{{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "500"}}

	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
	_, err := RunStepF(context.Background(), cfg, cert, lbt)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatch")
}

func TestRunStepF_DisabledWithLBT_MismatchCaps(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr},
				Amount:             big.NewInt(1000),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	lbt := []LBTEntry{{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "500"}}

	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false, IgnoreBalanceMismatch: true}}
	result, err := RunStepF(context.Background(), cfg, cert, lbt)
	require.NoError(t, err)
	require.False(t, result.AllMatch)
	require.NotNil(t, result.CappedCertificate)
}

func TestRunStepF_AllMatch(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0", ID: 1,
			Result: json.RawMessage(`{"balances":[{"originNetwork":0,"originTokenAddress":"0xaAaAaAaaAaAaAaaAaAAAAAAAAaaaAaAaAaaAaaAa","amount":"1000"}]}`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr},
				Amount:             big.NewInt(1000),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	lbt := []LBTEntry{{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "1000"}}

	cfg := &Config{L2NetworkID: 0, Options: Options{UseAgglayerAdminToStepFCheck: true, AgglayerAdminURL: server.URL}}
	result, err := RunStepF(context.Background(), cfg, cert, lbt)
	require.NoError(t, err)
	require.True(t, result.AllMatch)
	require.Nil(t, result.CappedCertificate)
}

func TestRunStepF_MismatchAborts(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0", ID: 1,
			Result: json.RawMessage(`{"balances":[{"originNetwork":0,"originTokenAddress":"0xaAaAaAaaAaAaAaaAaAAAAAAAAaaaAaAaAaaAaaAa","amount":"500"}]}`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr},
				Amount:             big.NewInt(1000),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	cfg := &Config{L2NetworkID: 0, Options: Options{UseAgglayerAdminToStepFCheck: true, AgglayerAdminURL: server.URL}}
	_, err := RunStepF(context.Background(), cfg, cert, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatch")
}

func TestRunStepF_MismatchContinues(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := jsonRPCResponse{
			JSONRPC: "2.0", ID: 1,
			Result: json.RawMessage(`{"balances":[{"originNetwork":0,"originTokenAddress":"0xaAaAaAaaAaAaAaaAaAAAAAAAAaaaAaAaAaaAaaAa","amount":"500"}]}`),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr},
				Amount:             big.NewInt(1000),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	cfg := &Config{
		L2NetworkID: 0,
		Options: Options{
			UseAgglayerAdminToStepFCheck: true,
			AgglayerAdminURL:             server.URL,
			IgnoreBalanceMismatch:        true,
		},
	}
	result, err := RunStepF(context.Background(), cfg, cert, nil)
	require.NoError(t, err)
	require.False(t, result.AllMatch)
	require.NotNil(t, result.CappedCertificate)
}

func TestRunStepF_RPCError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &Config{L2NetworkID: 1, Options: Options{UseAgglayerAdminToStepFCheck: true, AgglayerAdminURL: server.URL}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil)
	require.Error(t, err)
}

func TestGroupBridgeExitsByToken(t *testing.T) {
	t.Parallel()

	addr1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	addr2 := common.HexToAddress("0x2222222222222222222222222222222222222222")

	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr1}, Amount: big.NewInt(100)},
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr1}, Amount: big.NewInt(200)},
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: addr2}, Amount: big.NewInt(500)},
		},
	}

	groups := groupBridgeExitsByToken(cert)

	require.Len(t, groups[tokenKey{0, addr1}], 2)
	require.Len(t, groups[tokenKey{1, addr2}], 1)
}

func TestCompareTokenBalances_AllMatch(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	groups := map[tokenKey][]*agglayertypes.BridgeExit{
		{0, addr}: {
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, DestinationAddress: dest, Amount: big.NewInt(1000)},
		},
	}
	agglayerEntries := []agglayerTokenEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Amount: "1000"},
	}

	checks := compareTokenBalances(groups, agglayerEntries, nil)
	require.Len(t, checks, 1)
	require.True(t, checks[0].Match)
	require.Empty(t, checks[0].CertificateEntries)
}

func TestCompareTokenBalances_Mismatch(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	dest1 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	dest2 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	groups := map[tokenKey][]*agglayertypes.BridgeExit{
		{0, addr}: {
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, DestinationAddress: dest1, DestinationNetwork: 0, Amount: big.NewInt(600)},
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, DestinationAddress: dest2, DestinationNetwork: 0, Amount: big.NewInt(400)},
		},
	}
	agglayerEntries := []agglayerTokenEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Amount: "999"},
	}

	checks := compareTokenBalances(groups, agglayerEntries, nil)
	require.Len(t, checks, 1)
	require.False(t, checks[0].Match)
	require.Equal(t, "1000", checks[0].CertificateAmount)
	require.Equal(t, "999", checks[0].AgglayerAmount)
	require.Len(t, checks[0].CertificateEntries, 2)
	require.Equal(t, "600", checks[0].CertificateEntries[0].Amount)
	require.Equal(t, "400", checks[0].CertificateEntries[1].Amount)
	require.Equal(t, big.NewInt(999), checks[0].RemainingBalance)
}

func TestCompareTokenBalances_MissingInAgglayer(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	groups := map[tokenKey][]*agglayertypes.BridgeExit{
		{0, addr}: {
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, DestinationAddress: dest, Amount: big.NewInt(500)},
		},
	}

	checks := compareTokenBalances(groups, nil, nil)
	require.Len(t, checks, 1)
	require.False(t, checks[0].Match)
	require.Equal(t, "500", checks[0].CertificateAmount)
	require.Equal(t, "0", checks[0].AgglayerAmount)
	require.Len(t, checks[0].CertificateEntries, 1)
	require.Equal(t, big.NewInt(0), checks[0].RemainingBalance)
}

func TestCapCertificateExits_FitsWithinBudget(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(300)},
	}
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(1000)},
	}

	result := capCertificateExits(exits, checks, CapModeByAppearance)
	require.Len(t, result, 2)
	require.Equal(t, big.NewInt(400), result[0].Amount)
	require.Equal(t, big.NewInt(300), result[1].Amount)
}

func TestCapCertificateExits_CapsLastExit(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(600)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
	}
	// Budget covers first exit fully; second must be capped to 300.
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(900)},
	}

	result := capCertificateExits(exits, checks, CapModeByAppearance)
	require.Len(t, result, 2)
	require.Equal(t, big.NewInt(600), result[0].Amount)
	require.Equal(t, big.NewInt(300), result[1].Amount)
}

func TestCapCertificateExits_DropsExitsWhenBudgetExhausted(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(500)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(500)},
	}
	// Budget only covers first exit exactly; second must be dropped.
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(500)},
	}

	result := capCertificateExits(exits, checks, CapModeByAppearance)
	require.Len(t, result, 1)
	require.Equal(t, big.NewInt(500), result[0].Amount)
}

func TestCapCertificateExits_ZeroBudgetDropsAll(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(100)},
	}
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(0)},
	}

	result := capCertificateExits(exits, checks, CapModeByAppearance)
	require.Empty(t, result)
}

func TestCapCertificateExits_TokenNotInChecksPassesThrough(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(999)},
	}

	result := capCertificateExits(exits, nil, CapModeByAppearance)
	require.Len(t, result, 1)
	require.Equal(t, big.NewInt(999), result[0].Amount)
}

// TestCapCertificateExits_ByAmountCapsLargest checks that CapModeByAmount serves the smallest exit
// first, so the largest exit is the one capped/dropped — while the surviving exits are still emitted
// in their original order.
func TestCapCertificateExits_ByAmountCapsLargest(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	// Large exit appears first, small one second. Budget 700.
	newExits := func() []*agglayertypes.BridgeExit {
		return []*agglayertypes.BridgeExit{
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(700)},
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(300)},
		}
	}
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(700)},
	}

	// By amount: the 300 exit is served first (kept full), the 700 exit gets the leftover 400 → capped.
	// Surviving exits stay in original order.
	byAmount := capCertificateExits(newExits(), checks, CapModeByAmount)
	require.Len(t, byAmount, 2)
	require.Equal(t, big.NewInt(400), byAmount[0].Amount) // the 700 exit, capped to leftover
	require.Equal(t, big.NewInt(300), byAmount[1].Amount) // the 300 exit, kept full

	// By appearance: the 700 exit is served first (kept full, budget exhausted), the 300 is dropped.
	byAppearance := capCertificateExits(newExits(), checks, CapModeByAppearance)
	require.Len(t, byAppearance, 1)
	require.Equal(t, big.NewInt(700), byAppearance[0].Amount)
}

// TestCapCertificateExits_ByAmountDropsLargest checks that when the budget is too small the largest
// exit is dropped entirely while the smaller ones survive, still in their original order.
func TestCapCertificateExits_ByAmountDropsLargest(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(800)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(200)},
	}
	// Budget 200: only the small (200) exit fits; the large (800) exit is dropped.
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(200)},
	}

	result := capCertificateExits(exits, checks, CapModeByAmount)
	require.Len(t, result, 1)
	require.Equal(t, big.NewInt(200), result[0].Amount) // the 200 exit, kept; the 800 dropped
}

func TestCapCertificateExits_LBTMinAgglayer(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	// LBT=700, agglayer=800 → min=700; cert has two exits totalling 1000.
	groups := map[tokenKey][]*agglayertypes.BridgeExit{
		{0, addr}: {
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(600)},
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
		},
	}
	checks := compareTokenBalances(groups, []agglayerTokenEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Amount: "800"},
	}, []LBTEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "700"},
	})
	require.Equal(t, big.NewInt(700), checks[0].RemainingBalance)

	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(600)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
	}
	result := capCertificateExits(exits, checks, CapModeByAppearance)
	require.Len(t, result, 2)
	require.Equal(t, big.NewInt(600), result[0].Amount)
	require.Equal(t, big.NewInt(100), result[1].Amount) // capped: 700-600=100
}
