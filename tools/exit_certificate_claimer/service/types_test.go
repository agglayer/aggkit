package claimer

import (
	"math/big"
	"testing"

	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestProofToHex(t *testing.T) {
	t.Parallel()

	var proof treetypes.Proof
	proof[0] = common.HexToHash("0x01")
	proof[1] = common.HexToHash("0x02")

	out := proofToHex(proof)
	require.Len(t, out, int(treetypes.DefaultHeight))
	require.Equal(t, common.HexToHash("0x01").Hex(), out[0])
	require.Equal(t, common.HexToHash("0x02").Hex(), out[1])
	// Unset siblings render as the zero hash.
	require.Equal(t, common.Hash{}.Hex(), out[2])
}

func TestBigToString(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0", bigToString(nil))
	require.Equal(t, "0", bigToString(new(big.Int)))
	require.Equal(t, "12345", bigToString(big.NewInt(12345)))
}

func TestAddrHex(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0x0000000000000000000000000000000000000000", addrHex(common.Address{}))
	addr := common.HexToAddress("0x0b68058e5b2592b1f472adfe106305295a332a7c")
	require.Equal(t, addr.Hex(), addrHex(addr))
}

func TestMetadataHex(t *testing.T) {
	t.Parallel()

	require.Equal(t, "0x", metadataHex(nil))
	require.Equal(t, "0x", metadataHex([]byte{}))
	require.Equal(t, "0xabcd", metadataHex([]byte{0xab, 0xcd}))
}
