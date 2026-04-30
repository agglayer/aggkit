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

	result, err := RunStepF(context.Background(), &Config{}, &agglayertypes.Certificate{})
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

	checks := compareTokenBalances(groups, agglayerEntries)
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

	checks := compareTokenBalances(groups, agglayerEntries)
	require.Len(t, checks, 1)
	require.False(t, checks[0].Match)
	require.Equal(t, "1000", checks[0].CertificateAmount)
	require.Equal(t, "999", checks[0].AgglayerAmount)
	require.Len(t, checks[0].CertificateEntries, 2)
	require.Equal(t, "600", checks[0].CertificateEntries[0].Amount)
	require.Equal(t, "400", checks[0].CertificateEntries[1].Amount)
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

	checks := compareTokenBalances(groups, nil)
	require.Len(t, checks, 1)
	require.False(t, checks[0].Match)
	require.Equal(t, "500", checks[0].CertificateAmount)
	require.Equal(t, "0", checks[0].AgglayerAmount)
	require.Len(t, checks[0].CertificateEntries, 1)
}
