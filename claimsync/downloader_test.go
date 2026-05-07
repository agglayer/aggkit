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
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFindCall(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	lg := logger.WithFields("module", "test")

	// Simple direct call
	root := Call{
		Type: CallTypeCall,
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
		Type: CallTypeCall,
		To:   bridgeAddr,
		From: fromAddr,
		Err:  strPtr("reverted"),
	}
	_, err = findCall(root, bridgeAddr, nil, lg)
	require.Error(t, err)

	// Nested calls: one valid, one reverted — only valid is returned
	root = Call{
		Type: CallTypeCall,
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				Type: CallTypeCall,
				To:   bridgeAddr,
				From: fromAddr,
				Err:  nil,
			},
			{
				Type: CallTypeCall,
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

func TestFindCallSkipsNonCallFrameTypes(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	lg := logger.WithFields("module", "test")

	root := Call{
		Type: CallTypeCall,
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Calls: []Call{
			{Type: CallTypeDelegateCall, To: bridgeAddr, From: fromAddr},
			{Type: CallTypeStaticCall, To: bridgeAddr, From: fromAddr},
			{Type: CallTypeCallCode, To: bridgeAddr, From: fromAddr},
			{To: bridgeAddr, From: fromAddr},
		},
	}

	found, err := findCall(root, bridgeAddr, nil, lg)
	require.ErrorContains(t, err, "not found")
	require.Nil(t, found)

	root.Calls = append(root.Calls, Call{Type: CallTypeCall, To: bridgeAddr, From: fromAddr})
	found, err = findCall(root, bridgeAddr, nil, lg)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, CallTypeCall, found[0].Type)
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
		Type: CallTypeCall,
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				Type:  CallTypeCall,
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0x38, 0xb8, 0xfb, 0xbb}, // unrecognized
			},
			{
				Type:  CallTypeCall,
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: claimAssetEtrogMethodID,
			},
			{
				Type:  CallTypeCall,
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
		Type: CallTypeCall,
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				Type:  CallTypeCall,
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0x38, 0xb8, 0xfb, 0xbb},
			},
			{
				Type:  CallTypeCall,
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

func TestSetClaimCalldataFromRootFiltersDelegateCall(t *testing.T) {
	bridgeAddr := common.HexToAddress("0xb81d6e")
	callerAddr := common.HexToAddress("0xca11e6")
	lg := logger.WithFields("module", "test")

	bridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)

	globalIndex := big.NewInt(900)
	originNetwork := uint32(69)
	originAddress := common.HexToAddress("0xffaaffaa")
	destinationAddress := common.HexToAddress("0x123456789")
	amount := big.NewInt(3)

	type variant struct {
		proofLocal   [tree.DefaultHeight][common.HashLength]byte
		proofRollup  [tree.DefaultHeight][common.HashLength]byte
		proofLocalH  tree.Proof
		proofRollupH tree.Proof
		mer          common.Hash
		rer          common.Hash
		destNetwork  uint32
		metadata     []byte
	}

	newVariant := func(proofLocalHex, proofRollupHex, merHex, rerHex string,
		destNetwork uint32, metadata []byte) variant {
		var v variant
		v.proofLocal[5] = common.HexToHash(proofLocalHex)
		v.proofLocalH[5] = common.HexToHash(proofLocalHex)
		v.proofRollup[4] = common.HexToHash(proofRollupHex)
		v.proofRollupH[4] = common.HexToHash(proofRollupHex)
		v.mer = common.HexToHash(merHex)
		v.rer = common.HexToHash(rerHex)
		v.destNetwork = destNetwork
		v.metadata = metadata
		return v
	}

	claimFor := func(v variant) Claim {
		return Claim{
			GlobalIndex:         new(big.Int).Set(globalIndex),
			OriginNetwork:       originNetwork,
			OriginAddress:       originAddress,
			DestinationAddress:  destinationAddress,
			Amount:              new(big.Int).Set(amount),
			MainnetExitRoot:     v.mer,
			RollupExitRoot:      v.rer,
			ProofLocalExitRoot:  v.proofLocalH,
			ProofRollupExitRoot: v.proofRollupH,
			DestinationNetwork:  v.destNetwork,
			Metadata:            v.metadata,
			GlobalExitRoot:      crypto.Keccak256Hash(v.mer.Bytes(), v.rer.Bytes()),
		}
	}

	encode := func(v variant) []byte {
		data, packErr := bridgeABI.Pack(
			"claimAsset",
			v.proofLocal,
			v.proofRollup,
			globalIndex,
			v.mer,
			v.rer,
			originNetwork,
			originAddress,
			v.destNetwork,
			destinationAddress,
			amount,
			v.metadata,
		)
		require.NoError(t, packErr)
		return data
	}

	seed := func() Claim {
		return Claim{
			GlobalIndex:        new(big.Int).Set(globalIndex),
			OriginNetwork:      originNetwork,
			OriginAddress:      originAddress,
			DestinationAddress: destinationAddress,
			Amount:             new(big.Int).Set(amount),
		}
	}

	legit := newVariant("0xbeef", "0xa1fa", "0x5ca1e", "0xdead", 0, []byte{})
	malicious := newVariant("0xcafe", "0xbabe", "0xf00d", "0xb105", 42, []byte{0xde, 0xad, 0xbe, 0xef})
	legitClaim := claimFor(legit)

	legitFrame := Call{Type: CallTypeCall, To: bridgeAddr, Input: encode(legit)}
	delegateFrame := Call{Type: CallTypeDelegateCall, To: bridgeAddr, Input: encode(malicious)}
	maliciousCallFrame := Call{Type: CallTypeCall, To: bridgeAddr, Input: encode(malicious)}

	t.Run("call wins regardless of sibling order", func(t *testing.T) {
		tests := []struct {
			name  string
			calls []Call
		}{
			{name: "delegate first", calls: []Call{delegateFrame, legitFrame}},
			{name: "delegate last", calls: []Call{legitFrame, delegateFrame}},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				actual := seed()
				root := &Call{Type: CallTypeCall, To: callerAddr, Calls: tt.calls}
				require.NoError(t, setClaimCalldataFromRoot(&actual, root, bridgeAddr, lg))
				require.Equal(t, legitClaim, actual)
			})
		}
	})

	t.Run("delegate-only trace is ignored", func(t *testing.T) {
		actual := seed()
		root := &Call{Type: CallTypeCall, To: callerAddr, Calls: []Call{delegateFrame}}

		require.ErrorContains(t, setClaimCalldataFromRoot(&actual, root, bridgeAddr, lg), "not found")
		require.Equal(t, seed(), actual)
	})

	t.Run("duplicate identical calls are accepted", func(t *testing.T) {
		actual := seed()
		root := &Call{Type: CallTypeCall, To: callerAddr, Calls: []Call{legitFrame, legitFrame}}

		require.NoError(t, setClaimCalldataFromRoot(&actual, root, bridgeAddr, lg))
		require.Equal(t, legitClaim, actual)
	})

	t.Run("distinct matching calls are ambiguous", func(t *testing.T) {
		actual := seed()
		root := &Call{Type: CallTypeCall, To: callerAddr, Calls: []Call{legitFrame, maliciousCallFrame}}

		require.ErrorContains(t, setClaimCalldataFromRoot(&actual, root, bridgeAddr, lg), "ambiguous claim calldata")
		require.Equal(t, seed(), actual)
	})
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
				Type:  CallTypeCall,
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
	appenderMap, err := buildAppender(ethClient, bridgeAddr, deployment, lg)
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
			*arg = Call{Type: CallTypeCall, To: bridgeAddr, From: common.HexToAddress("0x01"), Input: claimAssetInput}
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
