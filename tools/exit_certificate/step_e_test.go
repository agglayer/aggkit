package exit_certificate

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestDecodeBridgeEvent_Valid(t *testing.T) {
	t.Parallel()

	// Construct a valid BridgeEvent ABI-encoded data.
	// Layout: leafType(32) | originNetwork(32) | originAddress(32) | destNetwork(32) |
	//         destAddress(32) | amount(32) | metadataOffset(32) | depositCount(32) |
	//         metadataLength(32) | metadata...
	data := make([]byte, 9*32)

	// leafType = 0
	data[31] = 0
	// originNetwork = 1
	data[63] = 1
	// originAddress = 0xAAAA...
	copy(data[64+12:96], common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA").Bytes())
	// destNetwork = 2
	data[127] = 2
	// destAddress = 0xBBBB...
	copy(data[128+12:160], common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB").Bytes())
	// amount = 1000
	new(big.Int).SetInt64(1000).FillBytes(data[160:192])
	// metadata offset = 256 (8*32)
	new(big.Int).SetInt64(256).FillBytes(data[192:224])
	// depositCount = 42
	new(big.Int).SetInt64(42).FillBytes(data[224:256])
	// metadata length = 0
	new(big.Int).SetInt64(0).FillBytes(data[256:288])

	dataHex := "0x" + common.Bytes2Hex(data)
	dep, err := decodeBridgeEvent(dataHex, "0xa", "0x1234")
	require.NoError(t, err)

	require.Equal(t, uint8(0), dep.LeafType)
	require.Equal(t, uint32(1), dep.OriginNetwork)
	require.Equal(t, common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), dep.OriginAddress)
	require.Equal(t, uint32(2), dep.DestinationNetwork)
	require.Equal(t, common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"), dep.DestinationAddress)
	require.Equal(t, big.NewInt(1000), dep.Amount)
	require.Equal(t, uint32(42), dep.DepositCount)
	require.Equal(t, uint64(10), dep.BlockNumber)
}

func TestDecodeBridgeEvent_DataTooShort(t *testing.T) {
	t.Parallel()

	_, err := decodeBridgeEvent("0x0000", "0x1", "0x1234")
	require.Error(t, err)
	require.Contains(t, err.Error(), "data too short")
}

func TestDecodeClaimEvent_Valid(t *testing.T) {
	t.Parallel()

	// ClaimEvent(uint256 globalIndex, uint32 originNetwork, address originAddress,
	//            address destinationAddress, uint256 amount)
	data := make([]byte, 5*32)

	// globalIndex = (1 << 64) | 42  (mainnet deposit, leaf index 42)
	gi := new(big.Int).Or(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(42))
	gi.FillBytes(data[0:32])
	// originNetwork = 0
	data[63] = 0
	// originAddress = 0xAAAA...
	copy(data[64+12:96], common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA").Bytes())
	// destinationAddress = 0xBBBB...
	copy(data[96+12:128], common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB").Bytes())
	// amount = 5000
	new(big.Int).SetInt64(5000).FillBytes(data[128:160])

	dataHex := "0x" + common.Bytes2Hex(data)
	claim, err := decodeClaimEvent(dataHex)
	require.NoError(t, err)

	require.Equal(t, gi.String(), claim.GlobalIndex.String())
	require.Equal(t, uint32(0), claim.OriginNetwork)
	require.Equal(t, common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), claim.OriginAddress)
	require.Equal(t, common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB"), claim.DestinationAddress)
	require.Equal(t, big.NewInt(5000), claim.Amount)
}

func TestDecodeClaimEvent_DataTooShort(t *testing.T) {
	t.Parallel()

	_, err := decodeClaimEvent("0x0000")
	require.Error(t, err)
	require.Contains(t, err.Error(), "claim data too short")
}

func TestMainnetFlagConstant(t *testing.T) {
	t.Parallel()

	// mainnetFlag should be (1 << 64)
	expected := new(big.Int).Lsh(big.NewInt(1), 64)
	require.Equal(t, expected.String(), mainnetFlag.String())
}

func TestStepE_ClaimedDepositFiltering(t *testing.T) {
	t.Parallel()

	// Simulate claim events where globalIndex has mainnet flag set.
	// GlobalIndex = (1 << 64) | leafIndex
	gi0 := new(big.Int).Or(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(5))
	gi1 := new(big.Int).Or(new(big.Int).Lsh(big.NewInt(1), 64), big.NewInt(10))

	l2ClaimEvents := []L2ClaimEvent{
		{GlobalIndex: gi0},
		{GlobalIndex: gi1},
	}

	// Build claimed set
	leafIndexMask := new(big.Int).SetUint64(0xFFFFFFFF)
	claimedDepositCounts := make(map[uint32]struct{})
	for _, claim := range l2ClaimEvents {
		gi := claim.GlobalIndex
		isMainnet := new(big.Int).And(gi, mainnetFlag).Sign() > 0
		if isMainnet {
			leafIndex := uint32(new(big.Int).And(gi, leafIndexMask).Uint64())
			claimedDepositCounts[leafIndex] = struct{}{}
		}
	}

	require.Contains(t, claimedDepositCounts, uint32(5))
	require.Contains(t, claimedDepositCounts, uint32(10))
	require.NotContains(t, claimedDepositCounts, uint32(0))
}

func TestStepE_MergeCertificateExits(t *testing.T) {
	t.Parallel()

	existingExit := &agglayertypes.BridgeExit{
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      0,
			OriginTokenAddress: common.Address{},
		},
		DestinationNetwork: 0,
		DestinationAddress: common.HexToAddress("0x1111"),
		Amount:             big.NewInt(100),
	}

	newExit := &agglayertypes.BridgeExit{
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      0,
			OriginTokenAddress: common.Address{},
		},
		DestinationNetwork: 0,
		DestinationAddress: common.HexToAddress("0x2222"),
		Amount:             big.NewInt(200),
	}

	certificate := &agglayertypes.Certificate{
		NetworkID:   1,
		BridgeExits: []*agglayertypes.BridgeExit{existingExit},
	}

	allExits := make([]*agglayertypes.BridgeExit, 0, len(certificate.BridgeExits)+1)
	allExits = append(allExits, certificate.BridgeExits...)
	allExits = append(allExits, newExit)

	finalCert := &agglayertypes.Certificate{
		NetworkID:   certificate.NetworkID,
		BridgeExits: allExits,
	}

	require.Len(t, finalCert.BridgeExits, 2)
	require.Equal(t, big.NewInt(100), finalCert.BridgeExits[0].Amount)
	require.Equal(t, big.NewInt(200), finalCert.BridgeExits[1].Amount)
}
