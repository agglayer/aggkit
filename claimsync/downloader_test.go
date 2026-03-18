package claimsync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/polygonzkevmbridge"
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

// --- BridgeDeployment.String() ---

func TestBridgeDeploymentString(t *testing.T) {
	require.Equal(t, "NonSovereignChain", NonSovereignChain.String())
	require.Equal(t, "SovereignChain", SovereignChain.String())
	require.Equal(t, "Unknown", Unknown.String())
	require.Equal(t, "Unknown", BridgeDeployment(99).String())
}

// --- resolveBridgeDeployment ---

func TestResolveBridgeDeployment(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	ctx := context.Background()

	// ABI-encoded zero-value returns: address(0) and uint32(0) are both 32 zero bytes
	validReturn := make([]byte, 32)
	revertErr := errors.New("execution reverted")

	tests := []struct {
		name         string
		setupMock    func(c *mocks.EthClienter)
		expectedKind BridgeDeployment
		expectErr    bool
	}{
		{
			name: "SovereignChain: BridgeManager succeeds",
			setupMock: func(c *mocks.EthClienter) {
				c.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(validReturn, nil).Once()
			},
			expectedKind: SovereignChain,
		},
		{
			name: "NonSovereignChain: BridgeManager reverts, LastUpdatedDepositCount succeeds",
			setupMock: func(c *mocks.EthClienter) {
				c.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, revertErr).Once()
				c.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(validReturn, nil).Once()
			},
			expectedKind: NonSovereignChain,
		},
		{
			name: "Unknown: both calls revert",
			setupMock: func(c *mocks.EthClienter) {
				c.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, revertErr).Once()
				c.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, revertErr).Once()
			},
			expectedKind: Unknown,
		},
		{
			name: "error: BridgeManager returns unexpected error",
			setupMock: func(c *mocks.EthClienter) {
				c.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
					Return(nil, errors.New("connection refused")).Once()
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ethClient := mocks.NewEthClienter(t)
			tt.setupMock(ethClient)

			deployment, err := resolveBridgeDeployment(ctx, bridgeAddr, ethClient)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectedKind, deployment.kind)
		})
	}
}

// --- buildClaimEventHandler edge cases ---

// buildClaimEventLog packs a valid etrog ClaimEvent log for the given globalIndex.
func buildClaimEventLog(t *testing.T, globalIndex *big.Int, txHash common.Hash, blockNum uint64) types.Log {
	t.Helper()
	agglayerBridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := agglayerBridgeABI.EventByID(claimEventSignature)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(globalIndex, uint32(1), common.Address{}, common.Address{}, big.NewInt(10))
	require.NoError(t, err)
	return types.Log{
		Topics:      []common.Hash{claimEventSignature},
		Data:        data,
		TxHash:      txHash,
		BlockNumber: blockNum,
	}
}

func TestBuildClaimEventHandler_BoundarySkip(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	lg := logger.WithFields("module", "test")
	txHash := common.HexToHash("0xABCD")
	blockNum := uint64(5)

	ethClient := mocks.NewEthClienter(t)
	agglayerBridgeContract, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethClient)
	require.NoError(t, err)

	querier := claimtypemocks.NewClaimQuerier(t)
	// Boundary is at block 5 — log is also at block 5, so it should be skipped
	querier.EXPECT().GetBoundaryBlockForClaimType(mock.Anything, mock.Anything, DetailedClaimEvent).
		Return(blockNum, nil)

	handler := buildClaimEventHandler(t.Context(), agglayerBridgeContract, ethClient, querier, bridgeAddr, true, lg)

	block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}
	log := buildClaimEventLog(t, big.NewInt(100), txHash, blockNum)

	err = handler(block, log)
	require.NoError(t, err)
	require.Empty(t, block.Events, "ClaimEvent should be skipped when at or after DetailedClaimEvent boundary")
}

func TestBuildClaimEventHandler_SameTxDetailedSkip(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	lg := logger.WithFields("module", "test")
	txHash := common.HexToHash("0xABCD")
	blockNum := uint64(3)

	ethClient := mocks.NewEthClienter(t)
	agglayerBridgeContract, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethClient)
	require.NoError(t, err)

	querier := claimtypemocks.NewClaimQuerier(t)
	querier.EXPECT().GetBoundaryBlockForClaimType(mock.Anything, mock.Anything, DetailedClaimEvent).
		Return(uint64(0), db.ErrNotFound)

	handler := buildClaimEventHandler(t.Context(), agglayerBridgeContract, ethClient, querier, bridgeAddr, true, lg)

	// Block already has a DetailedClaimEvent for the same tx
	block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}
	block.Events = append(block.Events, Event{Claim: &Claim{
		Type:   DetailedClaimEvent,
		TxHash: txHash,
	}})

	log := buildClaimEventLog(t, big.NewInt(100), txHash, blockNum)

	err = handler(block, log)
	require.NoError(t, err)
	require.Len(t, block.Events, 1, "ClaimEvent should be skipped; DetailedClaimEvent for same tx already present")
	event, ok := block.Events[0].(Event)
	require.True(t, ok)
	require.Equal(t, DetailedClaimEvent, event.Claim.Type)
}

// --- buildDetailedClaimEventHandler: removes ClaimEvent for same tx ---

func TestBuildDetailedClaimEventHandler_RemovesClaimEvent(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	txHash := common.HexToHash("0xDEAD")

	ethClient := mocks.NewEthClienter(t)
	agglayerBridgeL2Contract, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, ethClient)
	require.NoError(t, err)

	handler := buildDetailedClaimEventHandler(agglayerBridgeL2Contract)

	l2ABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)

	detailedEvent, err := l2ABI.EventByID(detailedClaimEventSignature)
	require.NoError(t, err)

	var nonIndexed abi.Arguments
	for _, inp := range detailedEvent.Inputs {
		if !inp.Indexed {
			nonIndexed = append(nonIndexed, inp)
		}
	}
	data, err := nonIndexed.Pack(
		[tree.DefaultHeight][common.HashLength]byte{},
		[tree.DefaultHeight][common.HashLength]byte{},
		[common.HashLength]byte{},
		[common.HashLength]byte{},
		uint8(0),
		uint32(1),
		common.Address{},
		uint32(0),
		big.NewInt(50),
		[]byte{},
	)
	require.NoError(t, err)

	log := types.Log{
		Topics: []common.Hash{
			detailedClaimEventSignature,
			common.BigToHash(big.NewInt(42)),             // globalIndex (indexed)
			common.BytesToHash(common.Address{}.Bytes()), // destinationAddress (indexed)
		},
		Data:   data,
		TxHash: txHash,
	}

	// Block already contains a ClaimEvent for the same tx
	block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: 1}}
	block.Events = append(block.Events, Event{Claim: &Claim{
		Type:   ClaimEvent,
		TxHash: txHash,
	}})

	err = handler(block, log)
	require.NoError(t, err)
	require.Len(t, block.Events, 1)
	ev, ok := block.Events[0].(Event)
	require.True(t, ok)
	require.Equal(t, DetailedClaimEvent, ev.Claim.Type,
		"ClaimEvent should be replaced by DetailedClaimEvent for the same tx")
}

// --- buildClaimEventHandlerPreEtrog ---

func TestBuildClaimEventHandlerPreEtrog_OK(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	lg := logger.WithFields("module", "test")
	globalIndex := uint32(77)
	txHash := common.HexToHash("0xBEEF")

	// Build pre-etrog ClaimEvent log
	preEtrogABI, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := preEtrogABI.EventByID(claimEventSignaturePreEtrog)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(
		globalIndex,
		uint32(1),
		common.Address{},
		common.Address{},
		big.NewInt(10),
	)
	require.NoError(t, err)
	logEntry := types.Log{
		Topics: []common.Hash{claimEventSignaturePreEtrog},
		Data:   data,
		TxHash: txHash,
	}

	// Build valid pre-etrog claimAsset calldata
	claimAssetCalldata, err := preEtrogABI.Methods["claimAsset"].Inputs.Pack(
		[tree.DefaultHeight][common.HashLength]byte{},
		globalIndex,
		[common.HashLength]byte{},
		[common.HashLength]byte{},
		uint32(1),
		common.Address{},
		uint32(0),
		common.Address{},
		big.NewInt(10),
		[]byte{},
	)
	require.NoError(t, err)
	claimAssetInput := append(append([]byte{}, claimAssetPreEtrogMethodID...), claimAssetCalldata...)

	ethClient := mocks.NewEthClienter(t)
	ethClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
		Run(func(result any, method string, args ...any) {
			arg, ok := result.(*Call)
			require.True(t, ok)
			*arg = Call{To: bridgeAddr, From: common.HexToAddress("0x01"), Input: claimAssetInput}
		}).
		Return(nil)

	legacyBridge, err := polygonzkevmbridge.NewPolygonzkevmbridge(bridgeAddr, ethClient)
	require.NoError(t, err)

	handler := buildClaimEventHandlerPreEtrog(legacyBridge, ethClient, bridgeAddr, true, lg)

	block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: 1}}
	err = handler(block, logEntry)
	require.NoError(t, err)
	require.Len(t, block.Events, 1)

	ev, ok := block.Events[0].(Event)
	require.True(t, ok)
	claim := ev.Claim
	require.Equal(t, new(big.Int).SetUint64(uint64(globalIndex)), claim.GlobalIndex)
	require.Equal(t, txHash, claim.TxHash)
}
