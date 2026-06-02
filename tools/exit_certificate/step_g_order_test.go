package exit_certificate

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestBridgesFromBlock(t *testing.T) {
	t.Parallel()

	bridges := []shadowForkBridge{
		{BlockNum: 5, DepositCount: 0},
		{BlockNum: 100, DepositCount: 1},
		{BlockNum: 101, DepositCount: 2},
	}
	got := bridgesFromBlock(bridges, 100)
	require.Len(t, got, 2)
	require.Equal(t, uint32(1), got[0].DepositCount)
	require.Equal(t, uint32(2), got[1].DepositCount)
}

func erc20Exit(originNet uint32, originAddr, dest common.Address, amount int64) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: originNet, OriginTokenAddress: originAddr},
		DestinationNetwork: 0,
		DestinationAddress: dest,
		Amount:             big.NewInt(amount),
	}
}

func nativeExit(dest common.Address, amount int64) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		TokenInfo:          nil,
		DestinationNetwork: 0,
		DestinationAddress: dest,
		Amount:             big.NewInt(amount),
	}
}

// replayedBridge builds a shadowForkBridge as it would be recovered from the shadow-fork for the
// given exit content and deposit count.
func replayedBridge(
	originNet uint32, originAddr, dest common.Address, amount int64, depositCount uint32,
) shadowForkBridge {
	return shadowForkBridge{
		BlockNum:           uint64(1000 + depositCount),
		OriginNetwork:      originNet,
		OriginAddress:      originAddr,
		DestinationNetwork: 0,
		DestinationAddress: dest,
		Amount:             big.NewInt(amount),
		DepositCount:       depositCount,
	}
}

func TestReorderCertificateExits(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xaaaa")
	destA := common.HexToAddress("0x1111")
	destB := common.HexToAddress("0x2222")
	destC := common.HexToAddress("0x3333")

	// Certificate order: [A-erc20, B-native, C-erc20], metadata tagged by original index.
	exits := []*agglayertypes.BridgeExit{
		erc20Exit(1, tokenA, destA, 100),
		nativeExit(destB, 200),
		erc20Exit(1, tokenA, destC, 300),
	}
	metadatas := [][]byte{{0xA}, {0xB}, {0xC}}
	cert := &agglayertypes.Certificate{BridgeExits: exits}

	// Shadow-fork deposit order is C(0), A(1), B(2) — note native B emits origin (0,0x0).
	replayed := []shadowForkBridge{
		replayedBridge(1, tokenA, destC, 300, 0),
		replayedBridge(1, tokenA, destA, 100, 1),
		replayedBridge(0, common.Address{}, destB, 200, 2),
	}

	newMeta, err := reorderCertificateExits(cert, metadatas, replayed, 0, common.Address{})
	require.NoError(t, err)

	require.Equal(t, destC, cert.BridgeExits[0].DestinationAddress)
	require.Equal(t, destA, cert.BridgeExits[1].DestinationAddress)
	require.Equal(t, destB, cert.BridgeExits[2].DestinationAddress)
	require.Equal(t, [][]byte{{0xC}, {0xA}, {0xB}}, newMeta)
}

func TestReorderCertificateExitsDuplicateExits(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xaaaa")
	dest := common.HexToAddress("0x1111")

	// Two identical exits — they produce identical leaves, so any assignment is valid as long as
	// the function does not error and preserves the count.
	exits := []*agglayertypes.BridgeExit{
		erc20Exit(1, tokenA, dest, 100),
		erc20Exit(1, tokenA, dest, 100),
	}
	metadatas := [][]byte{{0x1}, {0x2}}
	cert := &agglayertypes.Certificate{BridgeExits: exits}

	replayed := []shadowForkBridge{
		replayedBridge(1, tokenA, dest, 100, 0),
		replayedBridge(1, tokenA, dest, 100, 1),
	}

	newMeta, err := reorderCertificateExits(cert, metadatas, replayed, 0, common.Address{})
	require.NoError(t, err)
	require.Len(t, cert.BridgeExits, 2)
	require.Len(t, newMeta, 2)
}

func TestReorderCertificateExitsCountMismatch(t *testing.T) {
	t.Parallel()

	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		nativeExit(common.HexToAddress("0x1111"), 100),
	}}
	_, err := reorderCertificateExits(cert, [][]byte{nil}, nil, 0, common.Address{})
	require.ErrorContains(t, err, "!= certificate bridge exit count")
}

func TestReorderCertificateExitsNoMatch(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xaaaa")
	dest := common.HexToAddress("0x1111")
	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		erc20Exit(1, tokenA, dest, 100),
	}}
	// Replayed bridge has a different amount → no content match.
	replayed := []shadowForkBridge{replayedBridge(1, tokenA, dest, 999, 0)}
	_, err := reorderCertificateExits(cert, [][]byte{nil}, replayed, 0, common.Address{})
	require.ErrorContains(t, err, "no certificate bridge exit matches")
}
