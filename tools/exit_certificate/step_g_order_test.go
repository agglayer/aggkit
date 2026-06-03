package exit_certificate

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

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

// leafWithDepositCount builds a replayed BridgeLeaf carrying the given deposit count and a metadata
// tag, as it would be captured from the replay of the matching certificate exit.
func leafWithDepositCount(depositCount uint32, metadata []byte) bridgesyncerlite.BridgeLeaf {
	return bridgesyncerlite.BridgeLeaf{DepositCount: depositCount, Metadata: metadata}
}

func TestReorderCertificateByDepositCount(t *testing.T) {
	t.Parallel()

	tokenA := common.HexToAddress("0xaaaa")
	destA := common.HexToAddress("0x1111")
	destB := common.HexToAddress("0x2222")
	destC := common.HexToAddress("0x3333")

	// Certificate order: [A, B, C], metadata tagged by original index.
	exits := []*agglayertypes.BridgeExit{
		erc20Exit(1, tokenA, destA, 100),
		nativeExit(destB, 200),
		erc20Exit(1, tokenA, destC, 300),
	}
	cert := &agglayertypes.Certificate{BridgeExits: exits}

	// Replay assigned deposit counts C(0), A(1), B(2) — leaves are indexed by the original exit
	// position, so leaves[0] is A's, leaves[1] is B's, leaves[2] is C's.
	leaves := []bridgesyncerlite.BridgeLeaf{
		leafWithDepositCount(1, []byte{0xA}),
		leafWithDepositCount(2, []byte{0xB}),
		leafWithDepositCount(0, []byte{0xC}),
	}

	newMeta, err := reorderCertificateByDepositCount(cert, leaves)
	require.NoError(t, err)

	require.Equal(t, destC, cert.BridgeExits[0].DestinationAddress)
	require.Equal(t, destA, cert.BridgeExits[1].DestinationAddress)
	require.Equal(t, destB, cert.BridgeExits[2].DestinationAddress)
	require.Equal(t, [][]byte{{0xC}, {0xA}, {0xB}}, newMeta)
}

func TestReorderCertificateByDepositCountCountMismatch(t *testing.T) {
	t.Parallel()

	cert := &agglayertypes.Certificate{BridgeExits: []*agglayertypes.BridgeExit{
		nativeExit(common.HexToAddress("0x1111"), 100),
	}}
	_, err := reorderCertificateByDepositCount(cert, nil)
	require.ErrorContains(t, err, "!= certificate bridge exit count")
}
