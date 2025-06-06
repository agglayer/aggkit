package converters

import (
	"math/big"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestGetBridgeExits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		bridges       []bridgesync.Bridge
		expectedExits []*agglayertypes.BridgeExit
	}{
		{
			name: "Single bridge",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
			},
			expectedExits: []*agglayertypes.BridgeExit{
				{
					LeafType: agglayertypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           crypto.Keccak256([]byte("metadata")),
				},
			},
		},
		{
			name: "Multiple bridges",
			bridges: []bridgesync.Bridge{
				{
					LeafType:           agglayertypes.LeafTypeAsset.Uint8(),
					OriginNetwork:      1,
					OriginAddress:      common.HexToAddress("0x123"),
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           []byte("metadata"),
				},
				{
					LeafType:           agglayertypes.LeafTypeMessage.Uint8(),
					OriginNetwork:      3,
					OriginAddress:      common.HexToAddress("0x789"),
					DestinationNetwork: 4,
					DestinationAddress: common.HexToAddress("0xabc"),
					Amount:             big.NewInt(200),
					Metadata:           []byte("data"),
				},
			},
			expectedExits: []*agglayertypes.BridgeExit{
				{
					LeafType: agglayertypes.LeafTypeAsset,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      1,
						OriginTokenAddress: common.HexToAddress("0x123"),
					},
					DestinationNetwork: 2,
					DestinationAddress: common.HexToAddress("0x456"),
					Amount:             big.NewInt(100),
					Metadata:           crypto.Keccak256([]byte("metadata")),
				},
				{
					LeafType: agglayertypes.LeafTypeMessage,
					TokenInfo: &agglayertypes.TokenInfo{
						OriginNetwork:      3,
						OriginTokenAddress: common.HexToAddress("0x789"),
					},
					DestinationNetwork: 4,
					DestinationAddress: common.HexToAddress("0xabc"),
					Amount:             big.NewInt(200),
					Metadata:           crypto.Keccak256([]byte("data")),
				},
			},
		},
		{
			name:          "No bridges",
			bridges:       []bridgesync.Bridge{},
			expectedExits: []*agglayertypes.BridgeExit{},
		},
	}

	for _, tt := range tests {
		tt := tt

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			converter := &BridgeExitConverter{}
			exits := converter.ConvertToBridgeExits(tt.bridges)

			require.Equal(t, tt.expectedExits, exits)
		})
	}
}
