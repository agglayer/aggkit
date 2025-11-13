package flows

import (
	"math/big"
	"os"
	"testing"

	"github.com/agglayer/aggkit/aggsender/config"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

func TestZKEVMSupportExploratory(t *testing.T) {
	t.Skip("exploratory test")
	l2URL := os.Getenv("L2URL")
	require.NotEmpty(t, l2URL)
	ethClient, err := ethclient.Dial(l2URL)
	require.NoError(t, err)
	flow := &baseFlow{}
	flow.AddZKEVMSupport(
		config.SupportLegacyZKEVMConfig{
			Enabled:      true,
			L2BridgeAddr: common.HexToAddress("0x1348947e282138d8f377b467F7D9c2EB0F335d1f"),
		},
		ethClient,
	)
	require.NotNil(t, flow.zkEVMStatus.l2Client)
	require.Equal(t, uint64(0), flow.zkEVMStatus.etrogActivationBlock)

	bn, err := flow.GetEtrogActivationBlockFromBlockRange(t.Context(), 0, 50001)
	require.NoError(t, err)
	require.Equal(t, uint64(0x105a), bn)
}
func TestZKEVMSUpportGlobalIndex(t *testing.T) {
	v, ok := big.NewInt(0).SetString("18446744073709551619", 10)
	require.True(t, ok)
	mainnetFlag, rollupIndex, leafIndex, err := bridgesync.DecodeGlobalIndex(v)

	require.NoError(t, err)
	t.Logf("mainnet=%v rollup=%d leaf=%d", mainnetFlag, rollupIndex, leafIndex)
	require.Equal(t, true, mainnetFlag)
	require.Equal(t, uint32(0), rollupIndex)
}
