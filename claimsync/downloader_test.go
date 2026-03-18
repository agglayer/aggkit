package claimsync

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	claimtypemocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	"github.com/agglayer/aggkit/db"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFindCall(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	lg := logger.WithFields("module", "test")

	// Simple direct call
	root := Call{
		To:   bridgeAddr,
		From: fromAddr,
		Err:  nil,
	}
	founds, err := findCall(root, bridgeAddr, nil, lg)
	require.NoError(t, err)
	require.NotNil(t, founds)
	require.Equal(t, bridgeAddr, founds[0].To)

	// Reverted root call must be skipped — returns ErrNotFound
	root = Call{
		To:   bridgeAddr,
		From: fromAddr,
		Err:  strPtr("reverted"),
	}
	_, err = findCall(root, bridgeAddr, nil, lg)
	require.Error(t, err)

	// Nested calls: one valid, one reverted — only valid is returned
	root = Call{
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				To:   bridgeAddr,
				From: fromAddr,
				Err:  nil,
			},
			{
				To:   bridgeAddr,
				From: fromAddr,
				Err:  strPtr("reverted"),
			},
		},
	}
	founds, err = findCall(root, bridgeAddr, nil, lg)
	require.NoError(t, err)
	require.Len(t, founds, 1)
	require.Equal(t, bridgeAddr, founds[0].To)
}

func TestFindCallWithMixedMethods(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	lg := logger.WithFields("module", "test")

	// Transaction with three sub-calls:
	//   1. unrecognized method
	//   2. claimAsset (recognized)
	//   3. claimMessage (recognized)
	rootCall := Call{
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0x38, 0xb8, 0xfb, 0xbb}, // unrecognized
			},
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: claimAssetEtrogMethodID,
			},
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: claimMessageEtrogMethodID,
			},
		},
	}

	isClaimMethod := func(call Call) (bool, error) {
		if len(call.Input) < methodIDLength {
			return false, fmt.Errorf("input too short")
		}
		methodID := call.Input[:methodIDLength]
		return bytes.Equal(methodID, claimAssetEtrogMethodID) || bytes.Equal(methodID, claimMessageEtrogMethodID), nil
	}

	founds, err := findCall(rootCall, bridgeAddr, isClaimMethod, lg)
	require.NoError(t, err)
	require.NotNil(t, founds)
	// DFS uses a stack so claimMessage (pushed last) is found first
	require.Equal(t, claimMessageEtrogMethodID, []byte(founds[0].Input[:4]))
}

func TestFindCallWithOnlyUnrecognizedMethods(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	lg := logger.WithFields("module", "test")

	rootCall := Call{
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0x38, 0xb8, 0xfb, 0xbb},
			},
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0xaa, 0xbb, 0xcc, 0xdd},
			},
		},
	}

	found, err := findCall(rootCall, bridgeAddr, func(call Call) (bool, error) {
		if len(call.Input) < 4 {
			return false, fmt.Errorf("input too short")
		}
		methodID := call.Input[:4]
		if bytes.Equal(methodID, claimAssetEtrogMethodID) || bytes.Equal(methodID, claimMessageEtrogMethodID) {
			return true, nil
		}
		return false, nil
	}, lg)

	require.Error(t, err)
	require.Nil(t, found)
	require.Contains(t, err.Error(), "not found")
}

func TestTryDecodeClaimCalldata(t *testing.T) {
	lg := logger.WithFields("module", "test")
	globalIndex := big.NewInt(42)

	agglayerBridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)

	packClaimInputs := func(method string, gi *big.Int) []byte {
		data, packErr := agglayerBridgeABI.Methods[method].Inputs.Pack(
			[tree.DefaultHeight][common.HashLength]byte{}, // smtProofLocalExitRoot
			[tree.DefaultHeight][common.HashLength]byte{}, // smtProofRollupExitRoot
			gi,
			[common.HashLength]byte{}, // mainnetExitRoot
			[common.HashLength]byte{}, // rollupExitRoot
			uint32(10),
			common.Address{},
			uint32(0),
			common.Address{},
			big.NewInt(100),
			[]byte{},
		)
		require.NoError(t, packErr)
		return data
	}

	claimAssetInput := append(append([]byte{}, claimAssetEtrogMethodID...), packClaimInputs("claimAsset", globalIndex)...)
	claimMessageInput := append(append([]byte{}, claimMessageEtrogMethodID...), packClaimInputs("claimMessage", globalIndex)...)
	wrongGlobalInput := append(append([]byte{}, claimAssetEtrogMethodID...), packClaimInputs("claimAsset", big.NewInt(999))...)

	tests := []struct {
		name          string
		input         []byte
		globalIndex   *big.Int
		expectedFound bool
		expectedMsg   bool
		expectErr     bool
	}{
		{
			name:      "input too short",
			input:     []byte{0x01, 0x02},
			expectErr: true,
		},
		{
			name:          "unrecognized method ID",
			input:         []byte{0xaa, 0xbb, 0xcc, 0xdd},
			globalIndex:   globalIndex,
			expectedFound: false,
		},
		{
			name:          "claimAsset matching globalIndex",
			input:         claimAssetInput,
			globalIndex:   globalIndex,
			expectedFound: true,
			expectedMsg:   false,
		},
		{
			name:          "claimMessage matching globalIndex",
			input:         claimMessageInput,
			globalIndex:   globalIndex,
			expectedFound: true,
			expectedMsg:   true,
		},
		{
			name:          "claimAsset non-matching globalIndex",
			input:         wrongGlobalInput,
			globalIndex:   globalIndex,
			expectedFound: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claim := &Claim{GlobalIndex: tt.globalIndex}
			found, err := tryDecodeClaimCalldata(claim, tt.input, lg)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedFound, found)
				if tt.expectedFound {
					require.Equal(t, tt.expectedMsg, claim.IsMessage)
				}
			}
		})
	}
}

func TestBuildAppender(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	blockNum := uint64(1)
	lg := logger.WithFields("module", "test")

	l2ABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)

	agglayerBridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)

	claimGlobalIndex := big.NewInt(100)

	// Build claimAsset calldata that matches the ClaimEvent globalIndex
	claimAssetCalldata, err := agglayerBridgeABI.Methods["claimAsset"].Inputs.Pack(
		[tree.DefaultHeight][common.HashLength]byte{},
		[tree.DefaultHeight][common.HashLength]byte{},
		claimGlobalIndex,
		[common.HashLength]byte{},
		[common.HashLength]byte{},
		uint32(10),
		common.Address{},
		uint32(0),
		common.Address{},
		big.NewInt(50),
		[]byte{},
	)
	require.NoError(t, err)
	claimAssetInput := append(append([]byte{}, claimAssetEtrogMethodID...), claimAssetCalldata...)

	ethClient := mocks.NewEthClienter(t)
	// Only called for the claimEventSignature subtest
	ethClient.EXPECT().
		Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
		Run(func(result any, method string, args ...any) {
			arg, ok := result.(*Call)
			require.True(t, ok)
			*arg = Call{
				To:    bridgeAddr,
				From:  common.HexToAddress("0x01"),
				Input: claimAssetInput,
			}
		}).
		Return(nil).
		Maybe()

	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethClient)
	require.NoError(t, err)
	agglayerBridgeL2, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, ethClient)
	require.NoError(t, err)

	deployment := &bridgeDeployment{
		kind:             NonSovereignChain,
		agglayerBridge:   agglayerBridge,
		agglayerBridgeL2: agglayerBridgeL2,
	}
	querier := claimtypemocks.NewClaimQuerier(t)
	querier.EXPECT().GetBoundaryBlockForClaimType(mock.Anything, mock.Anything, mock.Anything).
		Return(uint64(0), db.ErrNotFound).Maybe()

	appenderMap, err := buildAppender(t.Context(), ethClient, querier, bridgeAddr, deployment, lg)
	require.NoError(t, err)
	require.NotNil(t, appenderMap)

	tests := []struct {
		name           string
		eventSignature common.Hash
		logsCount      int
		logBuilder     func(t *testing.T) types.Log
	}{
		{
			name:           "claimEventSignature",
			eventSignature: claimEventSignature,
			logsCount:      1,
			logBuilder: func(t *testing.T) types.Log {
				t.Helper()
				event, err := agglayerBridgeABI.EventByID(claimEventSignature)
				require.NoError(t, err)
				data, err := event.Inputs.Pack(
					claimGlobalIndex, uint32(10), common.Address{}, common.Address{}, big.NewInt(50),
				)
				require.NoError(t, err)
				return types.Log{
					Topics: []common.Hash{claimEventSignature},
					Data:   data,
				}
			},
		},
		{
			name:           "detailedClaimEventSignature",
			eventSignature: detailedClaimEventSignature,
			logsCount:      1,
			logBuilder: func(t *testing.T) types.Log {
				t.Helper()
				detailedEvent, err := l2ABI.EventByID(detailedClaimEventSignature)
				require.NoError(t, err)

				var nonIndexed abi.Arguments
				for _, inp := range detailedEvent.Inputs {
					if !inp.Indexed {
						nonIndexed = append(nonIndexed, inp)
					}
				}
				// Non-indexed order: smtProofLocalExitRoot, smtProofRollupExitRoot,
				// mainnetExitRoot, rollupExitRoot, leafType, originNetwork,
				// originTokenAddress, destinationNetwork, amount, metadata
				data, err := nonIndexed.Pack(
					[tree.DefaultHeight][common.HashLength]byte{},
					[tree.DefaultHeight][common.HashLength]byte{},
					[common.HashLength]byte{},
					[common.HashLength]byte{},
					uint8(0),
					uint32(10),
					common.Address{},
					uint32(0),
					big.NewInt(100),
					[]byte{},
				)
				require.NoError(t, err)

				destAddr := common.HexToAddress("0x30")
				return types.Log{
					Topics: []common.Hash{
						detailedClaimEventSignature,
						common.BigToHash(big.NewInt(200)),    // globalIndex (indexed)
						common.BytesToHash(destAddr.Bytes()), // destinationAddress (indexed)
					},
					Data: data,
				}
			},
		},
		{
			name:           "unsetClaimEventSignature",
			eventSignature: unsetClaimEventSignature,
			logsCount:      1,
			logBuilder: func(t *testing.T) types.Log {
				t.Helper()
				event, err := l2ABI.EventByID(unsetClaimEventSignature)
				require.NoError(t, err)
				data, err := event.Inputs.Pack(
					common.HexToHash("0xdeadbeef"), // unsetGlobalIndex (bytes32)
					common.HexToHash("0x5ca1e"),    // newUnsetGlobalIndexHashChain (bytes32)
				)
				require.NoError(t, err)
				return types.Log{
					Topics: []common.Hash{unsetClaimEventSignature},
					Data:   data,
				}
			},
		},
		{
			name:           "setClaimEventSignature",
			eventSignature: setClaimEventSignature,
			logsCount:      1,
			logBuilder: func(t *testing.T) types.Log {
				t.Helper()
				event, err := l2ABI.EventByID(setClaimEventSignature)
				require.NoError(t, err)
				data, err := event.Inputs.Pack(
					common.HexToHash("0xfeedcafe"), // globalIndex (bytes32)
				)
				require.NoError(t, err)
				return types.Log{
					Topics: []common.Hash{setClaimEventSignature},
					Data:   data,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := tt.logBuilder(t)
			appenderFunc, exists := appenderMap[tt.eventSignature]
			require.True(t, exists)

			block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}
			err := appenderFunc(block, log)
			require.NoError(t, err)
			require.Equal(t, tt.logsCount, len(block.Events))
		})
	}
}

func strPtr(s string) *string {
	return &s
}
