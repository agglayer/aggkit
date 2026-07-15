package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

	outputDir := t.TempDir()
	cfg := &Config{
		L2NetworkID: 1,
		Options: Options{
			UseAgglayerAdminToStepFCheck: true,
			AgglayerAdminURL:             server.URL,
			AgglayerAdminToken:           "my-iap-token",
			OutputDir:                    outputDir,
		},
	}
	result, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	// The raw agglayer LBT is dumped to the output dir whenever the admin endpoint is queried.
	require.FileExists(t, filepath.Join(outputDir, fileStepFAgglayerLBT))
}

// TestRunStepF_EmptyOutputDir_SkipsLBTDump guards against the LBT dump landing in the process's
// working directory when OutputDir is unset (a programmatically built Config, never a loaded one).
func TestRunStepF_EmptyOutputDir_SkipsLBTDump(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
		},
	}
	result, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NoFileExists(t, fileStepFAgglayerLBT)
}

func TestRunStepF_MissingAdminURL_Error(t *testing.T) {
	t.Parallel()

	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: true}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "agglayerAdminURL")
}

func TestRunStepF_DisabledNoLBT_Skips(t *testing.T) {
	t.Parallel()

	// useAgglayerAdminToStepFCheck=false and no LBT data: nothing to compare, so the step is
	// skipped with a benign all-match result and no RPC call (no agglayerAdminURL set).
	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
	result, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil, nil)
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
	result, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
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
	_, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
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
	result, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
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
	result, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
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
	_, err := RunStepF(context.Background(), cfg, cert, nil, nil)
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
	result, err := RunStepF(context.Background(), cfg, cert, nil, nil)
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
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, nil, nil)
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

	checks, err := compareTokenBalances(groups, agglayerEntries, nil, nil, nil)
	require.NoError(t, err)
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

	checks, err := compareTokenBalances(groups, agglayerEntries, nil, nil, nil)
	require.NoError(t, err)
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

	checks, err := compareTokenBalances(groups, nil, nil, nil, nil)
	require.NoError(t, err)
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

	result, err := capCertificateExits(exits, checks, CapModeByAppearance)
	require.NoError(t, err)
	require.Len(t, result, 2)
	// Untouched exits are returned as the original pointers (sameExits relies on this).
	require.Same(t, exits[0], result[0])
	require.Same(t, exits[1], result[1])
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

	result, err := capCertificateExits(exits, checks, CapModeByAppearance)
	require.NoError(t, err)
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

	result, err := capCertificateExits(exits, checks, CapModeByAppearance)
	require.NoError(t, err)
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

	result, err := capCertificateExits(exits, checks, CapModeByAppearance)
	require.NoError(t, err)
	require.Empty(t, result)
}

func TestCapCertificateExits_TokenNotInChecksPassesThrough(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(999)},
	}

	result, err := capCertificateExits(exits, nil, CapModeByAppearance)
	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, big.NewInt(999), result[0].Amount)
}

// TestCapCertificateExits_NoneAllowsNoOp checks that CapModeNone passes the exits through untouched
// when they all fit within the budget.
func TestCapCertificateExits_NoneAllowsNoOp(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(300)},
	}
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(700)},
	}

	result, err := capCertificateExits(exits, checks, CapModeNone)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Same(t, exits[0], result[0])
	require.Same(t, exits[1], result[1])
}

// TestCapCertificateExits_NoneFailsOnTrim checks that CapModeNone returns errCapForbidden instead of
// capping an exit that exceeds the budget.
func TestCapCertificateExits_NoneFailsOnTrim(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(600)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
	}
	// Budget covers the first exit fully; the second would have to be capped to 300.
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(900)},
	}

	_, err := capCertificateExits(exits, checks, CapModeNone)
	require.ErrorIs(t, err, errCapForbidden)
}

// TestCapCertificateExits_NoneFailsOnDrop checks that CapModeNone returns errCapForbidden instead of
// dropping an exit whose token budget is exhausted.
func TestCapCertificateExits_NoneFailsOnDrop(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(100)},
	}
	checks := []TokenBalanceCheck{
		{OriginNetwork: 0, OriginTokenAddress: addr.Hex(), RemainingBalance: big.NewInt(0)},
	}

	_, err := capCertificateExits(exits, checks, CapModeNone)
	require.ErrorIs(t, err, errCapForbidden)
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
	byAmount, err := capCertificateExits(newExits(), checks, CapModeByAmount)
	require.NoError(t, err)
	require.Len(t, byAmount, 2)
	require.Equal(t, big.NewInt(400), byAmount[0].Amount) // the 700 exit, capped to leftover
	require.Equal(t, big.NewInt(300), byAmount[1].Amount) // the 300 exit, kept full

	// By appearance: the 700 exit is served first (kept full, budget exhausted), the 300 is dropped.
	byAppearance, err := capCertificateExits(newExits(), checks, CapModeByAppearance)
	require.NoError(t, err)
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

	result, err := capCertificateExits(exits, checks, CapModeByAmount)
	require.NoError(t, err)
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
	checks, err := compareTokenBalances(groups, []agglayerTokenEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Amount: "800"},
	}, []LBTEntry{
		{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "700"},
	}, nil, nil)
	require.NoError(t, err)
	require.Equal(t, big.NewInt(700), checks[0].RemainingBalance)

	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(600)},
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(400)},
	}
	result, err := capCertificateExits(exits, checks, CapModeByAppearance)
	require.NoError(t, err)
	require.Len(t, result, 2)
	require.Equal(t, big.NewInt(600), result[0].Amount)
	require.Equal(t, big.NewInt(100), result[1].Amount) // capped: 700-600=100
}

// TestRunStepF_PrefundMatchedStillCapsToLBT checks that even when every check matches thanks to
// the genesis pre-fund discount, Step F still produces a capped certificate trimming the native
// exits to the LBT: the pre-funded amount has no agglayer collateral and cannot be bridged out.
func TestRunStepF_PrefundMatchedStillCapsToLBT(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	// Native exits: 300 genuinely bridged + 700 genesis pre-fund = 1000 raw; LBT covers 300.
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(300)},
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(700)},
	}}
	lbt := []LBTEntry{
		{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "300"},
	}
	cfg := &Config{Options: Options{
		UseAgglayerAdminToStepFCheck: false,
		GenesisPrefundETHWei:         "700",
		CapMode:                      CapModeByAmount,
	}}

	result, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
	require.NoError(t, err)
	require.True(t, result.AllMatch) // 1000 − 700 == 300 → the check itself matches
	// ...but the certificate is still capped: the 700 pre-fund exit (the largest) is dropped.
	require.NotNil(t, result.CappedCertificate)
	require.Len(t, result.CappedCertificate.BridgeExits, 1)
	require.Equal(t, big.NewInt(300), result.CappedCertificate.BridgeExits[0].Amount)
}

// TestRunStepF_NoPrefundNoCapOnAllMatch checks the allMatch fast path stays cap-free when no
// genesis pre-fund is declared.
func TestRunStepF_NoPrefundNoCapOnAllMatch(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(300)},
	}}
	lbt := []LBTEntry{
		{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "300"},
	}
	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false, CapMode: CapModeByAmount}}

	result, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
	require.NoError(t, err)
	require.True(t, result.AllMatch)
	require.Nil(t, result.CappedCertificate)
}

// TestRunStepF_CapModeNoneFailsOnPrefundCap checks that with capMode=none the genesis pre-fund
// trim (which caps the native exits even on allMatch) fails instead of capping.
func TestRunStepF_CapModeNoneFailsOnPrefundCap(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	// Native exits: 300 genuinely bridged + 700 genesis pre-fund = 1000 raw; LBT covers 300.
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(300)},
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(700)},
	}}
	lbt := []LBTEntry{
		{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "300"},
	}
	cfg := &Config{Options: Options{
		UseAgglayerAdminToStepFCheck: false,
		GenesisPrefundETHWei:         "700",
		CapMode:                      CapModeNone,
	}}

	_, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
	require.ErrorIs(t, err, errCapForbidden)
}

// TestRunStepF_CapModeNoneFailsOnMismatchCap checks that a mismatch with ignoreBalanceMismatch=true
// (which normally produces a capped certificate) fails under capMode=none instead of capping.
func TestRunStepF_CapModeNoneFailsOnMismatchCap(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	// Certificate claims 500 but the LBT only covers 300 → mismatch requiring a trim.
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(500)},
	}}
	lbt := []LBTEntry{
		{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "300"},
	}
	cfg := &Config{Options: Options{
		UseAgglayerAdminToStepFCheck: false,
		IgnoreBalanceMismatch:        true,
		CapMode:                      CapModeNone,
	}}

	_, err := RunStepF(context.Background(), cfg, cert, lbt, nil)
	require.ErrorIs(t, err, errCapForbidden)
}

func TestDiscountGenesisPrefund(t *testing.T) {
	t.Parallel()

	token := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	tokenKeyNonNative := tokenKey{OriginNetwork: 1, OriginTokenAddress: token}

	// Unset prefund → certificate amount unchanged.
	require.Equal(t, big.NewInt(1000), discountGenesisPrefund(big.NewInt(1000), nativeTokenKey, nil))

	// Discounts only the native token; other tokens untouched.
	require.Equal(t, big.NewInt(700),
		discountGenesisPrefund(big.NewInt(1000), nativeTokenKey, big.NewInt(300)))
	require.Equal(t, big.NewInt(1000),
		discountGenesisPrefund(big.NewInt(1000), tokenKeyNonNative, big.NewInt(300)))

	// Prefund larger than the certificate sum floors at 0 (never negative).
	require.Equal(t, big.NewInt(0),
		discountGenesisPrefund(big.NewInt(1000), nativeTokenKey, big.NewInt(4000)))

	// Zero prefund is a no-op.
	require.Equal(t, big.NewInt(1000),
		discountGenesisPrefund(big.NewInt(1000), nativeTokenKey, big.NewInt(0)))
}

// TestCompareTokenBalances_GenesisPrefundDiscount checks the native certificate sum is discounted
// by the declared genesis pre-fund before the three-way comparison, and other tokens are untouched.
func TestCompareTokenBalances_GenesisPrefundDiscount(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	// Native exits: 300 genuinely bridged + 700 genesis pre-fund = 1000 total.
	groups := map[tokenKey][]*agglayertypes.BridgeExit{
		nativeTokenKey: {
			{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(1000)},
		},
	}
	agglayerEntries := []agglayerTokenEntry{
		{OriginNetwork: 0, OriginTokenAddress: common.Address{}, Amount: "300"},
	}
	lbt := []LBTEntry{
		{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "300"},
	}

	checks, err := compareTokenBalances(groups, agglayerEntries, lbt, big.NewInt(700), nil)
	require.NoError(t, err)
	require.Len(t, checks, 1)
	require.True(t, checks[0].Match)
	require.Equal(t, "300", checks[0].CertificateAmount) // discounted sum is what gets compared
	// The cap budget stays min(agglayer, lbt): the genuinely bridged amount, not the raw cert sum.
	require.Equal(t, big.NewInt(300), checks[0].RemainingBalance)
}

func TestDiscountSkippedSCLocked(t *testing.T) {
	t.Parallel()

	token := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	k := tokenKey{OriginNetwork: 1, OriginTokenAddress: token}
	other := tokenKey{OriginNetwork: 2, OriginTokenAddress: token}

	// nil map (skipSCLockedValue disabled) → amount unchanged.
	require.Equal(t, big.NewInt(1000), discountSkippedSCLocked(big.NewInt(1000), k, "lbt", nil))

	skipped := map[tokenKey]*big.Int{k: big.NewInt(300)}

	// Discounts only the token with an omitted amount; other tokens untouched.
	require.Equal(t, big.NewInt(700), discountSkippedSCLocked(big.NewInt(1000), k, "lbt", skipped))
	require.Equal(t, big.NewInt(1000), discountSkippedSCLocked(big.NewInt(1000), other, "lbt", skipped))

	// An omitted amount larger than the balance floors at 0 (never negative).
	require.Equal(t, big.NewInt(0),
		discountSkippedSCLocked(big.NewInt(100), k, "lbt", skipped))

	// A zero omitted amount is a no-op.
	require.Equal(t, big.NewInt(1000),
		discountSkippedSCLocked(big.NewInt(1000), k, "lbt", map[tokenKey]*big.Int{k: big.NewInt(0)}))
}

// TestCompareTokenBalances_SkippedSCLockedDiscount checks the omitted SC-locked amount is discounted
// from both the LBT and agglayer amounts before the three-way comparison, recorded in the check, and
// excluded from the cap budget; tokens without an omitted amount are untouched.
func TestCompareTokenBalances_SkippedSCLockedDiscount(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	skippedTok := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	plainTok := common.HexToAddress("0xCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCCC")
	skippedKey := tokenKey{OriginNetwork: 1, OriginTokenAddress: skippedTok}
	plainKey := tokenKey{OriginNetwork: 1, OriginTokenAddress: plainTok}

	// skippedTok: certificate holds the EOA share (300); 700 SC-locked was omitted from it.
	// plainTok: fully covered by the certificate (500), no omission.
	groups := map[tokenKey][]*agglayertypes.BridgeExit{
		skippedKey: {
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: skippedTok},
				DestinationAddress: dest, Amount: big.NewInt(300)},
		},
		plainKey: {
			{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: plainTok},
				DestinationAddress: dest, Amount: big.NewInt(500)},
		},
	}
	agglayerEntries := []agglayerTokenEntry{
		{OriginNetwork: 1, OriginTokenAddress: skippedTok, Amount: "1000"},
		{OriginNetwork: 1, OriginTokenAddress: plainTok, Amount: "500"},
	}
	lbt := []LBTEntry{
		{WrappedTokenAddress: skippedTok, OriginNetwork: 1, OriginTokenAddress: skippedTok, Balance: "1000"},
		{WrappedTokenAddress: plainTok, OriginNetwork: 1, OriginTokenAddress: plainTok, Balance: "500"},
	}
	skipped := map[tokenKey]*big.Int{skippedKey: big.NewInt(700)}

	checks, err := compareTokenBalances(groups, agglayerEntries, lbt, nil, skipped)
	require.NoError(t, err)
	require.Len(t, checks, 2)

	byAddr := map[string]TokenBalanceCheck{}
	for _, c := range checks {
		byAddr[c.OriginTokenAddress] = c
	}

	skippedCheck := byAddr[skippedTok.Hex()]
	require.True(t, skippedCheck.Match)
	require.Equal(t, "300", skippedCheck.CertificateAmount)
	require.Equal(t, "300", skippedCheck.AgglayerAmount) // 1000 − 700 omitted
	require.Equal(t, "300", skippedCheck.LBTAmount)      // 1000 − 700 omitted
	require.Equal(t, "700", skippedCheck.SkippedSCLockedAmount)
	// The cap budget excludes the left-behind funds: they must never be bridged out.
	require.Equal(t, big.NewInt(300), skippedCheck.RemainingBalance)

	plainCheck := byAddr[plainTok.Hex()]
	require.True(t, plainCheck.Match)
	require.Equal(t, "500", plainCheck.LBTAmount)
	require.Empty(t, plainCheck.SkippedSCLockedAmount)
	require.Equal(t, big.NewInt(500), plainCheck.RemainingBalance)
}

// TestRunStepF_SkipSCLockedOffline checks the offline LBT comparison end to end: a certificate that
// omits the SC-locked exits matches once the omitted amounts (Step C) are discounted from the LBT —
// and mismatches when the flag is off (same inputs, no discount).
func TestRunStepF_SkipSCLockedOffline(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	cert := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			{
				TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: addr},
				Amount:             big.NewInt(300),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	lbt := []LBTEntry{{OriginNetwork: 1, OriginTokenAddress: addr, Balance: "1000"}}
	scLocked := []SCLockedValue{
		{OriginNetwork: 1, OriginTokenAddress: addr, PendingSCLockedBalance: "700"},
	}

	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false, SkipSCLockedValue: true}}
	result, err := RunStepF(context.Background(), cfg, cert, lbt, scLocked)
	require.NoError(t, err)
	require.True(t, result.AllMatch)
	require.Nil(t, result.CappedCertificate)
	require.Len(t, result.Checks, 1)
	require.Equal(t, "300", result.Checks[0].LBTAmount) // 1000 − 700 omitted
	require.Equal(t, "300", result.Checks[0].CertificateAmount)
	require.Equal(t, "700", result.Checks[0].SkippedSCLockedAmount)

	// Same inputs with the flag disabled: the SC-locked values are ignored, no discount → mismatch.
	cfgOff := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false}}
	_, err = RunStepF(context.Background(), cfgOff, cert, lbt, scLocked)
	require.Error(t, err)
	require.Contains(t, err.Error(), "mismatch")
}

// TestRunStepF_SkipSCLockedAgglayerMode checks the discount through the agglayer (three-way) path.
func TestRunStepF_SkipSCLockedAgglayerMode(t *testing.T) {
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
				Amount:             big.NewInt(300),
				DestinationAddress: common.HexToAddress("0xBBBB"),
			},
		},
	}
	lbt := []LBTEntry{{OriginNetwork: 0, OriginTokenAddress: addr, Balance: "1000"}}
	scLocked := []SCLockedValue{
		{OriginNetwork: 0, OriginTokenAddress: addr, PendingSCLockedBalance: "700"},
	}

	cfg := &Config{L2NetworkID: 0, Options: Options{
		UseAgglayerAdminToStepFCheck: true,
		AgglayerAdminURL:             server.URL,
		SkipSCLockedValue:            true,
	}}
	result, err := RunStepF(context.Background(), cfg, cert, lbt, scLocked)
	require.NoError(t, err)
	require.True(t, result.AllMatch)
	require.Len(t, result.Checks, 1)
	require.Equal(t, "300", result.Checks[0].AgglayerAmount) // 1000 − 700 omitted
	require.Equal(t, "300", result.Checks[0].LBTAmount)
	require.Equal(t, "700", result.Checks[0].SkippedSCLockedAmount)
}

// TestRunStepF_SkipSCLockedComposesWithPrefund checks the two discounts compose: the genesis
// pre-fund is discounted from the native certificate sum and the omitted SC-locked amount from the
// LBT — and the pre-fund capping on allMatch still applies against the reduced budget.
func TestRunStepF_SkipSCLockedComposesWithPrefund(t *testing.T) {
	t.Parallel()

	dest := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
	// Native exits: 300 genuinely bridged + 700 genesis pre-fund = 1000 raw.
	// LBT holds 1000, of which 700 SC-locked was omitted from the certificate.
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(300)},
		{TokenInfo: &agglayertypes.TokenInfo{}, DestinationAddress: dest, Amount: big.NewInt(700)},
	}}
	lbt := []LBTEntry{
		{WrappedTokenAddress: common.Address{}, OriginNetwork: 0, OriginTokenAddress: common.Address{}, Balance: "1000"},
	}
	scLocked := []SCLockedValue{
		{OriginNetwork: 0, OriginTokenAddress: common.Address{}, PendingSCLockedBalance: "700"},
	}
	cfg := &Config{Options: Options{
		UseAgglayerAdminToStepFCheck: false,
		GenesisPrefundETHWei:         "700",
		SkipSCLockedValue:            true,
		CapMode:                      CapModeByAmount,
	}}

	result, err := RunStepF(context.Background(), cfg, cert, lbt, scLocked)
	require.NoError(t, err)
	require.True(t, result.AllMatch) // cert 1000 − 700 prefund == lbt 1000 − 700 omitted
	// The pre-fund exit still cannot be bridged out: capped to the reduced budget (300).
	require.NotNil(t, result.CappedCertificate)
	require.Len(t, result.CappedCertificate.BridgeExits, 1)
	require.Equal(t, big.NewInt(300), result.CappedCertificate.BridgeExits[0].Amount)
}

// TestRunStepF_SkipSCLockedBadPendingBalanceAborts checks an unparseable Step C amount fails loudly
// instead of silently leaving the token's budget undiscounted.
func TestRunStepF_SkipSCLockedBadPendingBalanceAborts(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	scLocked := []SCLockedValue{
		{OriginNetwork: 1, OriginTokenAddress: addr, PendingSCLockedBalance: "not-a-number"},
	}
	cfg := &Config{Options: Options{UseAgglayerAdminToStepFCheck: false, SkipSCLockedValue: true}}
	_, err := RunStepF(context.Background(), cfg, &agglayertypes.Certificate{}, []LBTEntry{
		{OriginNetwork: 1, OriginTokenAddress: addr, Balance: "1000"},
	}, scLocked)
	require.Error(t, err)
	require.Contains(t, err.Error(), "pending SC-locked balance")
}
