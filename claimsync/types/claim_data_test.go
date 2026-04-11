package types

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestDecodePreEtrogCalldata_Valid(t *testing.T) {
	bridgeV1ABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)

	globalIndex := uint32(10)
	originNetwork := uint32(5)
	originAddress := common.HexToAddress("0x0a0a")
	amount := big.NewInt(150)
	destinationAddr := common.HexToAddress("0x0b0b")

	proof := treetypes.Proof{}
	for i := range treetypes.DefaultHeight {
		for j := range common.HashLength {
			proof[i] = common.HexToHash(fmt.Sprintf("%x", (j+1)%common.HashLength))
		}
	}

	expectedClaim := &Claim{
		GlobalIndex:        new(big.Int).SetUint64(uint64(globalIndex)),
		MainnetExitRoot:    common.HexToHash("0xdead"),
		RollupExitRoot:     common.HexToHash("0xbeef"),
		DestinationNetwork: uint32(6),
		Metadata:           common.Hex2Bytes("c001"),
		ProofLocalExitRoot: proof,
	}
	expectedClaim.GlobalExitRoot = crypto.Keccak256Hash(expectedClaim.MainnetExitRoot.Bytes(), expectedClaim.RollupExitRoot.Bytes())

	claimAssetInput, err := bridgeV1ABI.Pack("claimAsset",
		expectedClaim.ProofLocalExitRoot,
		globalIndex,
		expectedClaim.MainnetExitRoot,
		expectedClaim.RollupExitRoot,
		originNetwork,
		originAddress,
		expectedClaim.DestinationNetwork,
		destinationAddr,
		amount,
		expectedClaim.Metadata,
	)
	require.NoError(t, err)

	claimAssetData, err := bridgeV1ABI.Methods["claimAsset"].Inputs.Unpack(claimAssetInput[4:])
	require.NoError(t, err)

	actualClaim := &Claim{GlobalIndex: new(big.Int).SetUint64(uint64(globalIndex))}
	isFound, err := actualClaim.DecodePreEtrogCalldata(claimAssetData)
	require.NoError(t, err)
	require.True(t, isFound)
	require.Equal(t, expectedClaim, actualClaim)
}

func TestDecodePreEtrogCalldata(t *testing.T) {
	var (
		globalIndex            = uint32(12345)
		mainnetExitRoot        = common.HexToHash("0x11")
		rollupExitRoot         = common.HexToHash("0x22")
		metadata               = []byte("mock metadata")
		destinationNetwork     = uint32(1)
		invalidTypePlaceholder = "invalidType"
	)

	tests := []struct {
		name              string
		data              []any
		expectedIsDecoded bool
		expectError       bool
	}{
		{
			name: "Valid calldata",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{}, // Proof
				globalIndex, // GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()),  // RollupExitRoot
				uint32(1),        // OriginNetwork (not used)
				common.Address{}, // OriginTokenAddress (not used)
				destinationNetwork,
				common.Address{}, // DestinationAddress (not used)
				big.NewInt(0),    // Amount (not used)
				metadata,
			},
			expectedIsDecoded: true,
			expectError:       false,
		},
		{
			name: "Mismatched GlobalIndex",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				uint32(99999), // Wrong GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       false,
		},
		{
			name: "Invalid GlobalIndex Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				invalidTypePlaceholder, // Invalid GlobalIndex type
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid Proof Type",
			data: []any{
				invalidTypePlaceholder, // Invalid Proof type
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid MainnetExitRoot Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				invalidTypePlaceholder, // Invalid MainnetExitRoot type
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid RollupExitRoot Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				invalidTypePlaceholder, // Invalid RollupExitRoot type
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid DestinationNetwork Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				invalidTypePlaceholder, // Invalid DestinationNetwork type
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid Metadata Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(1),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				123, // Invalid metadata type (should be []byte)
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &Claim{
				GlobalIndex:        new(big.Int).SetUint64(uint64(globalIndex)),
				MainnetExitRoot:    common.Hash{},
				RollupExitRoot:     common.Hash{},
				DestinationNetwork: 0,
				Metadata:           nil,
			}

			match, err := claim.DecodePreEtrogCalldata(tt.data)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expectedIsDecoded, match)
		})
	}
}

func TestDecodeEtrogCalldata(t *testing.T) {
	var (
		globalIndex            = big.NewInt(12345)
		mainnetExitRoot        = common.HexToHash("0x11")
		rollupExitRoot         = common.HexToHash("0x22")
		metadata               = []byte("mock metadata")
		destinationNetwork     = uint32(1)
		invalidTypePlaceholder = "invalidType"
	)

	tests := []struct {
		name              string
		data              []any
		expectedIsDecoded bool
		expectError       bool
	}{
		{
			name: "Valid calldata",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{}, // ProofLocalExitRoot
				[treetypes.DefaultHeight][common.HashLength]byte{}, // ProofRollupExitRoot
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()),  // RollupExitRoot
				uint32(0),          // OriginNetwork (not used)
				common.Address{},   // OriginAddress (not used)
				destinationNetwork, // DestinationNetwork
				common.Address{},   // DestinationAddress (not used)
				big.NewInt(0),      // Amount (not used)
				metadata,
			},
			expectedIsDecoded: true,
			expectError:       false,
		},
		{
			name: "Mismatched GlobalIndex",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				[treetypes.DefaultHeight][common.HashLength]byte{},
				big.NewInt(99999), // Wrong GlobalIndex
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       false,
		},
		{
			name: "Invalid GlobalIndex Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				[treetypes.DefaultHeight][common.HashLength]byte{},
				invalidTypePlaceholder, // Invalid GlobalIndex type
				mainnetExitRoot.Bytes(),
				rollupExitRoot.Bytes(),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid LocalExitRoot Proof Type",
			data: []any{
				invalidTypePlaceholder, // Invalid ProofLocalExitRoot type
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid RollupExitRoot Proof Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				invalidTypePlaceholder, // Invalid RollupExitRoot proof type
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid MainnetExitRoot Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				invalidTypePlaceholder, // MainnetExitRoot
				[common.HashLength]byte(rollupExitRoot.Bytes()), // RollupExitRoot
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid RollupExitRoot Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()), // MainnetExitRoot
				invalidTypePlaceholder,                           // RollupExitRoot
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid DestinationNetwork Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				invalidTypePlaceholder, // DestinationNetwork
				common.Address{},
				big.NewInt(0),
				metadata,
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
		{
			name: "Invalid Metadata Type",
			data: []any{
				[treetypes.DefaultHeight][common.HashLength]byte{},
				[treetypes.DefaultHeight][common.HashLength]byte{},
				globalIndex,
				[common.HashLength]byte(mainnetExitRoot.Bytes()),
				[common.HashLength]byte(rollupExitRoot.Bytes()),
				uint32(0),
				common.Address{},
				destinationNetwork,
				common.Address{},
				big.NewInt(0),
				123, // Invalid metadata type (should be []byte)
			},
			expectedIsDecoded: false,
			expectError:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &Claim{GlobalIndex: globalIndex}

			isDecoded, err := claim.DecodeEtrogCalldata(tt.data)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.expectedIsDecoded, isDecoded)
		})
	}
}

func TestClaim_String(t *testing.T) {
	t.Run("nil GlobalIndex and Amount", func(t *testing.T) {
		c := &Claim{}
		s := c.String()
		require.Contains(t, s, "GlobalIndex: nil")
		require.Contains(t, s, "Amount: nil")
	})

	t.Run("with GlobalIndex and Amount set", func(t *testing.T) {
		c := &Claim{
			BlockNum:           10,
			BlockPos:           2,
			TxHash:             common.HexToHash("0xaabb"),
			GlobalIndex:        big.NewInt(42),
			OriginNetwork:      1,
			OriginAddress:      common.HexToAddress("0x1111"),
			DestinationAddress: common.HexToAddress("0x2222"),
			Amount:             big.NewInt(1000),
			DestinationNetwork: 3,
			IsMessage:          true,
			BlockTimestamp:     9999,
			Type:               ClaimEvent,
		}
		s := c.String()
		require.Contains(t, s, "BlockNum: 10")
		require.Contains(t, s, "BlockPos: 2")
		require.Contains(t, s, "GlobalIndex: 42")
		require.Contains(t, s, "Amount: 1000")
		require.Contains(t, s, "OriginNetwork: 1")
		require.Contains(t, s, "DestinationNetwork: 3")
		require.Contains(t, s, "IsMessage: true")
		require.Contains(t, s, "BlockTimestamp: 9999")
		require.Contains(t, s, fmt.Sprintf("Type: %s", ClaimEvent))
	})
}

func TestSetClaim_String(t *testing.T) {
	t.Run("nil GlobalIndex", func(t *testing.T) {
		s := (&SetClaim{}).String()
		require.Contains(t, s, "GlobalIndex: nil")
	})

	t.Run("with all fields set", func(t *testing.T) {
		sc := &SetClaim{
			BlockNum:    5,
			BlockPos:    1,
			TxHash:      common.HexToHash("0xccdd"),
			GlobalIndex: big.NewInt(7),
			CreatedAt:   12345,
		}
		s := sc.String()
		require.Contains(t, s, "BlockNum: 5")
		require.Contains(t, s, "BlockPos: 1")
		require.Contains(t, s, "GlobalIndex: 7")
		require.Contains(t, s, "CreatedAt: 12345")
	})
}
