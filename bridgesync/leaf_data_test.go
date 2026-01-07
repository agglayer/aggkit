package bridgesync

import (
	"math/big"
	"testing"

	aggkitabi "github.com/agglayer/aggkit/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestDecodeForwardLETLeaves(t *testing.T) {
	largeAmount := new(big.Int)
	largeAmount.SetString("123456789012345678901234567890", 10)

	testCases := []struct {
		name           string
		inputLeaves    []LeafData
		expectedLeaves []LeafData
		errorMsg       string
		useRawBytes    bool
		rawBytes       []byte
	}{
		{
			name: "successfully decode single leaf",
			inputLeaves: []LeafData{
				{
					LeafType:           1,
					OriginNetwork:      5,
					OriginAddress:      common.HexToAddress("0x1234567890123456789012345678901234567890"),
					DestinationNetwork: 10,
					DestinationAddress: common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
					Amount:             big.NewInt(1000000),
					Metadata:           []byte("test metadata"),
				},
			},
		},
		{
			name: "successfully decode multiple leaves",
			inputLeaves: []LeafData{
				{
					LeafType:           0,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x2222222222222222222222222222222222222222"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("first leaf"),
				},
				{
					LeafType:           1,
					OriginNetwork:      3,
					OriginAddress:      common.HexToAddress("0x3333333333333333333333333333333333333333"),
					DestinationNetwork: 4,
					DestinationAddress: common.HexToAddress("0x4444444444444444444444444444444444444444"),
					Amount:             big.NewInt(200),
					Metadata:           []byte("second leaf"),
				},
				{
					LeafType:           2,
					OriginNetwork:      5,
					OriginAddress:      common.HexToAddress("0x5555555555555555555555555555555555555555"),
					DestinationNetwork: 6,
					DestinationAddress: common.HexToAddress("0x6666666666666666666666666666666666666666"),
					Amount:             big.NewInt(300),
					Metadata:           []byte("third leaf"),
				},
			},
		},
		{
			name: "decode leaf with empty metadata",
			inputLeaves: []LeafData{
				{
					LeafType:           0,
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
					Amount:             big.NewInt(999),
					Metadata:           []byte{},
				},
			},
		},
		{
			name: "decode leaf with large amount",
			inputLeaves: []LeafData{
				{
					LeafType:           255,        // Max uint8
					OriginNetwork:      4294967295, // Max uint32
					OriginAddress:      common.HexToAddress("0xffffffffffffffffffffffffffffffffffffffff"),
					DestinationNetwork: 4294967295, // Max uint32
					DestinationAddress: common.HexToAddress("0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"),
					Amount:             largeAmount,
					Metadata:           []byte("large amount test"),
				},
			},
		},
		{
			name:        "decode empty array",
			inputLeaves: []LeafData{},
		},
		{
			name:        "fail on empty bytes",
			useRawBytes: true,
			rawBytes:    []byte{},
			errorMsg:    "encoded bytes are empty",
		},
		{
			name:        "fail on invalid encoded data",
			useRawBytes: true,
			rawBytes:    []byte{0x00, 0x01, 0x02, 0x03, 0x04},
			errorMsg:    "failed to unpack data",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var encodedBytes []byte
			var expectedLeaves []LeafData

			if tc.useRawBytes {
				encodedBytes = tc.rawBytes
			} else {
				encodedBytes = encodeLeafDataArray(t, tc.inputLeaves)
				expectedLeaves = tc.inputLeaves
			}

			decodedLeaves, err := decodeForwardLETLeaves(encodedBytes)

			if tc.errorMsg != "" {
				require.ErrorContains(t, err, tc.errorMsg)
				require.Nil(t, decodedLeaves)
			} else {
				require.NoError(t, err)
				require.Len(t, decodedLeaves, len(expectedLeaves))
				for i, expected := range expectedLeaves {
					verifyLeafData(t, expected, decodedLeaves[i])
				}
			}
		})
	}
}

// encodeLeafDataArray encodes a slice of LeafData using Solidity ABI encoding
// This simulates what the smart contract does with abi.encode(newLeaves)
func encodeLeafDataArray(t *testing.T, leaves []LeafData) []byte {
	t.Helper()

	encodedBytes, err := aggkitabi.EncodeABIStructArray(leaves)
	require.NoError(t, err)

	return encodedBytes
}

// verifyLeafData compares two LeafData structs for equality
func verifyLeafData(t *testing.T, expected, actual LeafData) {
	t.Helper()

	require.Equal(t, expected.LeafType, actual.LeafType, "LeafType mismatch")
	require.Equal(t, expected.OriginNetwork, actual.OriginNetwork, "OriginNetwork mismatch")
	require.Equal(t, expected.OriginAddress, actual.OriginAddress, "OriginAddress mismatch")
	require.Equal(t, expected.DestinationNetwork, actual.DestinationNetwork, "DestinationNetwork mismatch")
	require.Equal(t, expected.DestinationAddress, actual.DestinationAddress, "DestinationAddress mismatch")
	require.Equal(t, 0, expected.Amount.Cmp(actual.Amount), "Amount mismatch: expected %s, got %s",
		expected.Amount.String(), actual.Amount.String())
	require.Equal(t, expected.Metadata, actual.Metadata, "Metadata mismatch")
}
