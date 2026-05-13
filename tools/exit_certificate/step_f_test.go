package exit_certificate

import (
	"context"
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestRunStepF_Skipped(t *testing.T) {
	t.Parallel()

	result, err := RunStepF(context.Background(), &Config{}, &agglayertypes.Certificate{}, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Skipped)
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

	result := capCertificateExits(exits, checks)
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

	result := capCertificateExits(exits, checks)
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

	result := capCertificateExits(exits, checks)
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

	result := capCertificateExits(exits, checks)
	require.Empty(t, result)
}

func TestCapCertificateExits_TokenNotInChecksPassesThrough(t *testing.T) {
	t.Parallel()

	addr := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	exits := []*agglayertypes.BridgeExit{
		{TokenInfo: &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: addr}, Amount: big.NewInt(999)},
	}

	result := capCertificateExits(exits, nil)
	require.Len(t, result, 1)
	require.Equal(t, big.NewInt(999), result[0].Amount)
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
	result := capCertificateExits(exits, checks)
	require.Len(t, result, 2)
	require.Equal(t, big.NewInt(600), result[0].Amount)
	require.Equal(t, big.NewInt(100), result[1].Amount) // capped: 700-600=100
}
