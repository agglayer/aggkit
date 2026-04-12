package bridgesync

import (
	"bytes"
	"fmt"
	"math/big"
	"os"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/etherman"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var (
	claimAssetEtrogMethodID   = common.Hex2Bytes("ccaa2d11")
	claimMessageEtrogMethodID = common.Hex2Bytes("f5efcd79")
)

// mainnet:
// case https://etherscan.io/tx/0x8db8e288d25102b64d8a37ad05769817d1b43f0384dd05da075d24d2cee9cb65 (bn: 19566985) -> fix
// case: https://etherscan.io/tx/0x0b276867aa22d1c162c2700d35c500a124a6a953c7b24931a1d3efc63f7cd4ab  (bn: 22770713)
func TestExtractTxnAddressesExploratory(t *testing.T) {
	t.Skip("Skipping exploratory test")
	ctx := t.Context()
	l1url := os.Getenv("L1URL")
	ethRawClient, err := ethclient.Dial(l1url)
	require.NoError(t, err)
	ethClient := etherman.NewDefaultEthClient(ethRawClient, ethRawClient.Client(), nil)
	bridgeAddr := common.HexToAddress("0x2a3dd3eb832af982ec71669e178424b10dca2ede")
	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethRawClient)
	require.NoError(t, err)
	logger := logger.WithFields("module", "test")
	bn := big.NewInt(0).SetUint64(22770713)
	handler := buildBridgeEventHandler(ctx, agglayerBridge, bridgeAddr, ethClient, true, logger)
	filterQuery := ethereum.FilterQuery{
		Addresses: []common.Address{bridgeAddr},
		FromBlock: bn,
		ToBlock:   bn,
	}
	logs, err := ethClient.FilterLogs(t.Context(), filterQuery)
	require.NoError(t, err)
	foundCalls, rootCall, err := extractCallData(ethClient, common.HexToAddress("0x2a3dd3eb832af982ec71669e178424b10dca2ede"),
		common.HexToHash("0x0b276867aa22d1c162c2700d35c500a124a6a953c7b24931a1d3efc63f7cd4ab"),
		logger.WithFields("module", "test"), nil)
	require.NoError(t, err)
	require.NotNil(t, foundCalls)
	require.NotNil(t, rootCall)
	showListPtrCall(t, foundCalls)
	showListCall(t, rootCall.Calls, 0)
	showLogs(t, logs, &bridgeEventSignature)
	for _, vLog := range logs {
		if vLog.Topics[0] == bridgeEventSignature {
			err := handler(&sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: bn.Uint64()}}, vLog)
			require.NoError(t, err)
		}
	}
}
func showLogs(t *testing.T, logs []types.Log, equalTo *common.Hash) {
	t.Helper()
	for i, vLog := range logs {
		if equalTo != nil && vLog.Topics[0] != *equalTo {
			continue
		}
		fmt.Printf("Log %d: index: %d, Address:%s, Topics: +%v, BlockNumber:%d, TxHash:%s\n", i,
			vLog.Index, vLog.Address, vLog.Topics, vLog.BlockNumber, vLog.TxHash.Hex())
	}
}
func showCall(call *Call, nestedLevel int) {
	nestedPrefixStr := string(bytes.Repeat([]byte("*"), nestedLevel))

	hash := crypto.Keccak256(call.Input)
	fmt.Printf("%s Root Call To: %s From: %s Input Hash: %s\n", nestedPrefixStr, call.To.Hex(), call.From.Hex(),
		common.Bytes2Hex(hash))
	fmt.Printf("%s -- Input: %s\n", nestedPrefixStr, common.Bytes2Hex(call.Input))
	params, err := ExtractParamFromCallData(call.Input)
	if err == nil {
		fmt.Printf("%s  ---- Params: LeafType: %d DestNetwork: %d DestAddress: %s Amount: %s Token: %s Input: %s\n",
			nestedPrefixStr, params.LeafType, params.DestinationNetwork, params.DestinationAddress.Hex(), params.Amount.String(), params.Token.Hex(),
			common.Bytes2Hex(call.Input))
	} else {
		fmt.Printf("%s  ---- ???\n", nestedPrefixStr)
	}
}
func showListCall(t *testing.T, calls []Call, nestedLevel int) {
	t.Helper()
	for _, call := range calls {
		showCall(&call, nestedLevel)
		if len(call.Calls) > 0 {
			showListCall(t, call.Calls, nestedLevel+1)
		}
	}
}
func showListPtrCall(t *testing.T, calls []*Call) {
	t.Helper()
	for _, call := range calls {
		showCall(call, 0)
	}
}

// This case is https://etherscan.io/tx/0x280334ea89e49380d29e3c3931b9217bf699eaa7fa23e126c74a05eea1258503
// 2 calls everything is the same except the token address but is translated to event so doesn't match the call and the event
// this case is solved because the From is the same for both calls
func TestExtractCallDataCaseNotMatchingExploratory(t *testing.T) {
	t.Skip("Skipping exploratory test")
	l1url := os.Getenv("L1URL")
	ethRawClient, err := ethclient.Dial(l1url)
	require.NoError(t, err)
	ethClient := etherman.NewDefaultEthClient(ethRawClient, ethRawClient.Client(), nil)
	foundCalls, rootCall, err := extractCallData(ethClient, common.HexToAddress("0x2a3dd3eb832af982ec71669e178424b10dca2ede"),
		common.HexToHash("0x280334ea89e49380d29e3c3931b9217bf699eaa7fa23e126c74a05eea1258503"),
		logger.WithFields("module", "test"), nil)
	require.NoError(t, err)
	fmt.Printf("rootCall To: %s From: %s\n", rootCall.To.Hex(), rootCall.From.Hex())
	showListPtrCall(t, foundCalls)
	amount, ok := big.NewInt(0).SetString("3308702758450298978558701", 10)
	require.True(t, ok)
	txnSender, err := ExtractFromAddrFromCalls(foundCalls, &agglayerbridge.AgglayerbridgeBridgeEvent{
		LeafType:           bridgeLeafTypeAsset,
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0x3CF5Ed527DB2E08e5DdD5A2c692Dc5Ae35778D46"),
		Amount:             amount,
		OriginAddress:      common.HexToAddress("0x25722Cd432d02895d9BE45f5dEB60fc479c8781E"),
	})
	require.NoError(t, err)
	fmt.Printf("Txn Sender: %s\n", txnSender.Hex())
}

func TestExtractCallDataCaseMessageExploratory(t *testing.T) {
	t.Skip("Skipping exploratory test")
	l1url := os.Getenv("L1URL")
	ethRawClient, err := ethclient.Dial(l1url)
	require.NoError(t, err)
	ethClient := etherman.NewDefaultEthClient(ethRawClient, ethRawClient.Client(), nil)
	foundCalls, rootCall, err := extractCallData(ethClient, common.HexToAddress("0x2a3dd3eb832af982ec71669e178424b10dca2ede"),
		common.HexToHash("0x84a7e20778bd35231bfaefdcbb4ada9169b08658db49d69d38e3f467a799db38"),
		logger.WithFields("module", "test"), nil)
	require.NoError(t, err)
	fmt.Printf("rootCall To: %s From: %s\n", rootCall.To.Hex(), rootCall.From.Hex())
	for _, call := range foundCalls {
		fmt.Printf("Root Call To: %s From: %s\n", call.To.Hex(), call.From.Hex())
	}
	txnSender, err := ExtractFromAddrFromCalls(foundCalls, &agglayerbridge.AgglayerbridgeBridgeEvent{
		LeafType:           bridgeLeafTypeAsset,
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0x679606F3b37c49946F5AA7774a37f03387c7f264"),
		Amount:             big.NewInt(10000000000000000),
	})
	require.NoError(t, err)
	fmt.Printf("Txn Sender: %s\n", txnSender.Hex())
}

func TestExtractTxnSenderFromCalls(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr1 := common.HexToAddress("0x20")
	fromAddr2 := common.HexToAddress("0x30")
	callFromAddr1 := &Call{
		To:    bridgeAddr,
		From:  fromAddr1,
		Err:   nil,
		Input: BridgeAssetMethodID,
	}
	callFromAddr2 := &Call{
		To:    bridgeAddr,
		From:  fromAddr2,
		Err:   nil,
		Input: BridgeAssetMethodID,
	}
	event1 := &agglayerbridge.AgglayerbridgeBridgeEvent{
		LeafType:           bridgeLeafTypeAsset,
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0x30"),
		Amount:             big.NewInt(100),
	}
	tests := []struct {
		name       string
		callFrames []*Call
		event      *agglayerbridge.AgglayerbridgeBridgeEvent
		expectAddr common.Address
		expectErr  string
	}{
		{
			name:       "single matching call",
			callFrames: []*Call{callFromAddr1},
			event:      event1,
			expectAddr: fromAddr1,
		},
		{
			name:       "no matching call",
			callFrames: []*Call{},
			event:      event1,
			expectErr:  "no calls found",
		},
		{
			name:       "multiple calls same from",
			callFrames: []*Call{callFromAddr1, callFromAddr1},
			event:      event1,
			expectAddr: fromAddr1,
		},
		{
			name:       "multiple calls not same from,bad input data",
			callFrames: []*Call{callFromAddr1, callFromAddr2},
			event:      event1,
			expectErr:  " unpack inputs call data",
		},
		{
			name: "case: not same from, no match token and origin address",
			callFrames: []*Call{
				{
					To:    bridgeAddr,
					From:  common.HexToAddress("0x047E0b64743071b897A6177F1796E98b4C3f344E"),
					Input: common.Hex2Bytes("cd58657900000000000000000000000000000000000000000000000000000000000000010000000000000000000000003cf5ed527db2e08e5ddd5a2c692dc5ae35778d4600000000000000000000000000000000000000000000000000038d7ea4c680000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000c00000000000000000000000000000000000000000000000000000000000000000"),
				},
				{
					To:    bridgeAddr,
					From:  common.HexToAddress("0x047E0b64743071b897A6177F1796E98b4C3f344E"),
					Input: common.Hex2Bytes("cd586579000000000000000000000000000000000000000000000000000000000000000100000000000000000000000025722cd432d02895d9be45f5deb60fc479c87810000000000000000000000003cf5ed527db2e08e5ddd5a2c692dc5ae35778d4600000000000000000000000000000000000000000000000000038d7ea4c68000000000000000000000000000000000000000000000000000000000000000000"),
				},
			},
			event: &agglayerbridge.AgglayerbridgeBridgeEvent{
				LeafType:           bridgeLeafTypeAsset,
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x679606F3b37c49946F5AA7774a37f03387c7f264"),
				Amount:             big.NewInt(10000000000000000),
			},
			expectAddr: common.HexToAddress("0x047E0b64743071b897A6177F1796E98b4C3f344E"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txnSender, err := ExtractFromAddrFromCalls(tt.callFrames, tt.event)
			if tt.expectErr != "" {
				require.ErrorContains(t, err, tt.expectErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectAddr, txnSender)
			}
		})
	}
}

func TestBuildAppender(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	blockNum := uint64(1)

	bridgeL2Abi, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)

	ethClient := mocks.NewEthClienter(t)
	// txReceipt To is not bridgeAddr, so must call debugTrace
	mockClientCallGetTransactionByHash(t, ethClient,
		common.HexToHash("0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"),
		testAddress, "0x0000000000000000000000000000000000000000000")

	ethClient.EXPECT().
		Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
		Run(func(result any, method string, args ...any) {
			arg, ok := result.(*Call)
			require.True(t, ok)
			*arg = Call{To: bridgeAddr, Input: BridgeAssetMethodID}
		}).
		Return(nil).
		Maybe()

	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethClient)
	require.NoError(t, err)

	agglayerBridgeL2, err := agglayerbridgel2.NewAgglayerbridgel2(bridgeAddr, ethClient)
	require.NoError(t, err)

	bridgeDeployment := &bridgeDeployment{
		agglayerBridge:   agglayerBridge,
		agglayerBridgeL2: agglayerBridgeL2,
	}

	tests := []struct {
		name                 string
		eventSignature       common.Hash
		deploymentKind       BridgeDeployment
		logsCount            int
		buildQuerierMockFunc func() *BridgeQuerierMock
		logBuilder           func() (types.Log, error)
		expectedErr          string
	}{
		{
			name:           "bridgeEventSignature appender",
			eventSignature: bridgeEventSignature,
			deploymentKind: NonSovereignChain,
			logsCount:      1,
			logBuilder: func() (types.Log, error) {
				event, err := bridgeL2Abi.EventByID(bridgeEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				leafType := bridgetypes.LeafTypeAsset
				originNetwork := uint32(10)
				originAddress := common.HexToAddress("0x20")
				destinationNetwork := uint32(20)
				destinationAddress := common.HexToAddress("0x30")
				amount := big.NewInt(100)
				metadata := []byte{0x40}
				depositCount := uint32(1)
				data, err := event.Inputs.Pack(
					leafType, originNetwork, originAddress,
					destinationNetwork, destinationAddress,
					amount, metadata, depositCount)
				if err != nil {
					return types.Log{}, err
				}
				l := types.Log{
					Topics: []common.Hash{bridgeEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "backwardLETSignature appender",
			eventSignature: backwardLETEventSignature,
			deploymentKind: SovereignChain,
			logsCount:      1,
			logBuilder: func() (types.Log, error) {
				event, err := bridgeL2Abi.EventByID(backwardLETEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				previousDepositCount := big.NewInt(10)
				previousRoot := common.HexToHash("0xdeadbeef")
				newDepositCount := big.NewInt(8)
				newRoot := common.HexToHash("0x5ca1e")
				data, err := event.Inputs.Pack(previousDepositCount, previousRoot, newDepositCount, newRoot)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{backwardLETEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "forwardLETSignature appender",
			eventSignature: forwardLETEventSignature,
			deploymentKind: SovereignChain,
			logsCount:      1,
			logBuilder: func() (types.Log, error) {
				event, err := bridgeL2Abi.EventByID(forwardLETEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				previousDepositCount := big.NewInt(15)
				previousRoot := common.HexToHash("0xdeadbeef15")
				newDepositCount := big.NewInt(20)
				newRoot := common.HexToHash("0x5ca1e20")
				newLeaves := []byte("leavesdata")
				data, err := event.Inputs.Pack(previousDepositCount, previousRoot, newDepositCount, newRoot, newLeaves)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{forwardLETEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "unknown deployment kind",
			deploymentKind: 100,
			logBuilder:     func() (types.Log, error) { return types.Log{}, nil },
			expectedErr:    "unsupported bridge deployment kind: 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, err := tt.logBuilder()
			require.NoError(t, err)

			logger := logger.WithFields("module", "test")
			bridgeDeployment.kind = tt.deploymentKind
			appenderMap, err := buildAppender(t.Context(), ethClient, bridgeAddr, false, bridgeDeployment, logger)
			if tt.expectedErr == "" {
				require.NoError(t, err)
				require.NotNil(t, appenderMap)
			} else {
				require.ErrorContains(t, err, tt.expectedErr)
			}

			if tt.expectedErr == "" {
				block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}
				appenderFunc, exists := appenderMap[tt.eventSignature]
				require.True(t, exists)

				err = appenderFunc(block, log)
				require.NoError(t, err)
				require.Equal(t, tt.logsCount, len(block.Events))
			}
		})
	}
}

func TestFindCall(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	logger := logger.WithFields("module", "test")

	// Simple direct call
	root := Call{
		To:   bridgeAddr,
		From: fromAddr,
		Err:  nil,
	}
	founds, err := findCall(root, bridgeAddr, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, founds)
	require.Equal(t, bridgeAddr, founds[0].To)

	// Reverted call should be skipped
	root = Call{
		To:   bridgeAddr,
		From: fromAddr,
		Err:  strPtr("reverted"),
	}
	_, err = findCall(root, bridgeAddr, nil, logger)
	require.Error(t, err)

	// Nested call, only inner is not reverted
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
	founds, err = findCall(root, bridgeAddr, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, founds)
	require.Equal(t, bridgeAddr, founds[0].To)
}

func TestFindCallWithMixedMethods(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	logger := logger.WithFields("module", "test")

	// Test case: Transaction with mixed method calls
	// First call: getProxiedTokensManager (unrecognized)
	// Second call: claimAsset (recognized)
	// Third call: claimMessage (recognized)
	rootCall := Call{
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0x38, 0xb8, 0xfb, 0xbb}, // getProxiedTokensManager
			},
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: claimAssetEtrogMethodID, // claimAsset
			},
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: claimMessageEtrogMethodID, // claimMessage
			},
		},
	}

	// Test that findCall continues searching and finds the first valid claim method
	founds, err := findCall(rootCall, bridgeAddr, func(call Call) (bool, error) {
		// Simulate tryDecodeClaimCalldata behavior
		if len(call.Input) < methodIDLength {
			return false, fmt.Errorf("input too short")
		}
		methodID := call.Input[:methodIDLength]

		isClaimInvoked := bytes.Equal(methodID, claimAssetEtrogMethodID) || bytes.Equal(methodID, claimMessageEtrogMethodID)

		return isClaimInvoked, nil
	}, logger)

	require.NoError(t, err)
	require.NotNil(t, founds)
	require.Equal(t, bridgeAddr, founds[0].To)
	// Note: DFS traversal processes calls in reverse order (stack), so it finds claimMessage first
	require.Equal(t, claimMessageEtrogMethodID, []byte(founds[0].Input[:4])) // Should find the first claim method in DFS order
}

func TestFindCallWithOnlyUnrecognizedMethods(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	fromAddr := common.HexToAddress("0x20")
	logger := logger.WithFields("module", "test")

	// Test case: Transaction with only unrecognized method calls
	rootCall := Call{
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []Call{
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0x38, 0xb8, 0xfb, 0xbb}, // getProxiedTokensManager
			},
			{
				To:    bridgeAddr,
				From:  fromAddr,
				Err:   nil,
				Input: []byte{0xaa, 0xbb, 0xcc, 0xdd}, // unrecognized method
			},
		},
	}

	// Test that findCall returns not found when no valid claim methods exist
	found, err := findCall(rootCall, bridgeAddr, func(call Call) (bool, error) {
		// Simulate tryDecodeClaimCalldata behavior
		if len(call.Input) < 4 {
			return false, fmt.Errorf("input too short")
		}
		methodID := call.Input[:4]

		if bytes.Equal(methodID, claimAssetEtrogMethodID) || bytes.Equal(methodID, claimMessageEtrogMethodID) {
			return true, nil
		}

		// Unrecognized method ID - return false, nil to continue searching
		return false, nil
	}, logger)

	require.Error(t, err)
	require.Nil(t, found)
	require.Contains(t, err.Error(), "not found")
}

func TestTxnSenderField(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	blockNum := uint64(1)
	expectedTxnSender := common.HexToAddress("0x1234567890123456789012345678901234567890")

	agglayerBridgeABI, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)

	tests := []struct {
		name              string
		eventSignature    common.Hash
		callFrame         Call
		logBuilder        func() (types.Log, error)
		expectedTxnSender common.Address
	}{
		{
			name:           "bridgeEventSignature with TxnSender",
			eventSignature: bridgeEventSignature,
			callFrame: Call{
				To:   common.HexToAddress("0x01"),
				From: expectedTxnSender,
				Err:  nil,
				Calls: []Call{
					{
						To:    bridgeAddr,
						From:  expectedTxnSender,
						Err:   nil,
						Input: BridgeMessageMethodID,
					},
				},
			},
			expectedTxnSender: expectedTxnSender,
			logBuilder: func() (types.Log, error) {
				event, err := agglayerBridgeABI.EventByID(bridgeEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				leafType := uint8(1)
				originNetwork := uint32(10)
				originAddress := common.HexToAddress("0x20")
				destinationNetwork := uint32(20)
				destinationAddress := common.HexToAddress("0x30")
				amount := big.NewInt(100)
				metadata := []byte{0x40}
				depositCount := uint32(1)
				data, err := event.Inputs.Pack(
					leafType, originNetwork, originAddress,
					destinationNetwork, destinationAddress,
					amount, metadata, depositCount)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{bridgeEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log, err := tt.logBuilder()
			require.NoError(t, err)

			ethClient := mocks.NewEthClienter(t)

			// Add this to satisfy contract.GasTokenAddress call
			ethClient.EXPECT().
				CallContract(
					mock.Anything,
					mock.Anything,
					mock.Anything,
				).
				Return(common.LeftPadBytes(common.HexToAddress("0x3c351e10").Bytes(), 32), nil).
				Maybe()

			ethClient.EXPECT().
				Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
				Run(func(result any, method string, args ...any) {
					arg, ok := result.(*Call)
					require.True(t, ok)
					*arg = tt.callFrame
				}).
				Return(nil).
				Maybe()

			ethClient.EXPECT().
				Call(mock.Anything, "eth_getTransactionByHash", mock.Anything).Return(nil).
				Run(func(result any, method string, args ...any) {
					arg, ok := result.(*Transaction)
					require.True(t, ok)
					arg.FromRaw = tt.callFrame.From.Hex()
				}).
				Return(nil).
				Maybe()
			agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridgeAddr, ethClient)
			require.NoError(t, err)

			logger := logger.WithFields("module", "test")
			bridgeDeployment := &bridgeDeployment{
				kind:           NonSovereignChain,
				agglayerBridge: agglayerBridge,
			}
			appenderMap, err := buildAppender(t.Context(), ethClient, bridgeAddr, false, bridgeDeployment, logger)
			require.NoError(t, err)
			require.NotNil(t, appenderMap)

			block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}

			appenderFunc, exists := appenderMap[tt.eventSignature]
			require.True(t, exists)

			err = appenderFunc(block, log)
			require.NoError(t, err)
			require.Len(t, block.Events, 1)

			// Check TxnSender field
			event, ok := block.Events[0].(Event)
			require.True(t, ok, "Expected block.Events[0] to be of type Event")
			if event.Bridge != nil {
				require.Equal(t, tt.expectedTxnSender, event.Bridge.TxnSender, "Bridge TxnSender should match expected value")
			}
		})
	}
}

func TestExtractTxnAddresses(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	txHash := common.HexToHash("0xabcde12345abcde12345abcde12345abcde12345abcde12345abcde12345abcd")

	// Helper function to create address pointers
	addrPtr := func(addr common.Address) *common.Address {
		return &addr
	}

	tests := []struct {
		name                         string
		logEvent                     *agglayerbridge.AgglayerbridgeBridgeEvent
		responseDebugTrace           *Call
		responseDebugTraceError      error
		responseTransactionHash      *Transaction
		responseTransactionHashError error
		expectedTxnSender            common.Address
		expectedFrom                 *common.Address
		expectedTo                   common.Address
		expectErr                    string
	}{
		{
			name: "messageLeaf: error eth_getTransactionByHash",
			logEvent: &agglayerbridge.AgglayerbridgeBridgeEvent{
				LeafType:           bridgeLeafTypeMessage,
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x30"),
				Amount:             big.NewInt(100),
			},
			responseTransactionHashError: fmt.Errorf("RPC error"),
			expectErr:                    "RPC error",
		},
		{
			name: "messageLeaf: successful extraction with to address",
			logEvent: &agglayerbridge.AgglayerbridgeBridgeEvent{
				LeafType:           bridgeLeafTypeMessage,
				OriginAddress:      common.HexToAddress("0x40"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x30"),
				Amount:             big.NewInt(100),
			},
			responseTransactionHash: &Transaction{
				FromRaw: "0x1111111111111111111111111111111111111111",
				To:      "0x2222222222222222222222222222222222222222",
			},
			expectedTxnSender: common.HexToAddress("0x1111111111111111111111111111111111111111"),
			expectedFrom:      addrPtr(common.HexToAddress("0x40")),
			expectedTo:        common.HexToAddress("0x2222222222222222222222222222222222222222"),
		},
		{
			name: "assetLeaf: error can't find From from calls",
			logEvent: &agglayerbridge.AgglayerbridgeBridgeEvent{
				LeafType:           bridgeLeafTypeAsset,
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x30"),
				Amount:             big.NewInt(100),
			},
			responseDebugTrace: &Call{
				Calls: []Call{
					{
						To:    bridgeAddr,
						From:  common.HexToAddress("0x20"),
						Input: BridgeMessageMethodID,
					},
					{
						To:    bridgeAddr,
						From:  common.HexToAddress("0x25"),
						Input: BridgeMessageMethodID,
					},
				},
			},
			expectErr: "failed to extract",
		},
		{
			name: "assetLeaf: successful extraction with to address from rootCall",
			logEvent: &agglayerbridge.AgglayerbridgeBridgeEvent{
				LeafType:           bridgeLeafTypeAsset,
				OriginAddress:      common.HexToAddress("0x50"),
				DestinationNetwork: 1,
				DestinationAddress: common.HexToAddress("0x30"),
				Amount:             big.NewInt(100),
			},
			responseDebugTrace: &Call{
				From: common.HexToAddress("0x3333333333333333333333333333333333333333"),
				To:   common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Calls: []Call{
					{
						To:    bridgeAddr,
						From:  common.HexToAddress("0x50"),
						Input: append(BridgeAssetMethodID, make([]byte, 100)...),
					},
				},
			},
			expectedTxnSender: common.HexToAddress("0x3333333333333333333333333333333333333333"),
			expectedFrom:      addrPtr(common.HexToAddress("0x50")),
			expectedTo:        common.HexToAddress("0x4444444444444444444444444444444444444444"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ethClient := mocks.NewEthClienter(t)
			ctx := t.Context()
			logger := logger.WithFields("module", "test")
			ethClient.EXPECT().
				Call(mock.Anything, "eth_getTransactionByHash", mock.Anything).
				Return(tt.responseTransactionHashError).
				Run(func(result any, method string, args ...any) {
					arg, ok := result.(*Transaction)
					require.True(t, ok)
					if tt.responseTransactionHash != nil {
						*arg = *tt.responseTransactionHash
					}
				}).
				Maybe()
			ethClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
				Run(func(result any, method string, args ...any) {
					arg, ok := result.(*Call)
					require.True(t, ok)
					*arg = *tt.responseDebugTrace
				}).Return(nil).
				Maybe()

			txnSender, from, to, err := ExtractTxnAddresses(ctx, ethClient,
				bridgeAddr, txHash, tt.logEvent, logger, true)
			if tt.expectErr != "" {
				require.ErrorContains(t, err, tt.expectErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expectedTxnSender, txnSender)
				require.Equal(t, tt.expectedFrom, from)
				require.Equal(t, tt.expectedTo, to)
			}
		})
	}
}

func TestBridgeCallParams_String(t *testing.T) {
	params := &bridgeCallParams{
		LeafType:           1,
		DestinationNetwork: 42,
		DestinationAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		Amount:             big.NewInt(1000),
		Token:              common.HexToAddress("0xbeefdeadbeefdeadbeefdeadbeefdeadbeefdead"),
	}
	expectedStr := "LeafType: 1, DestinationNetwork: 42, DestinationAddress: 0x1234567890AbcdEF1234567890aBcdef12345678, Amount: 1000, Token: 0xbeEFdeaDBeefDeadBEeFDeAdbEeFDeaDbeefdEad"
	require.Equal(t, expectedStr, params.String())

	var nilParams *bridgeCallParams
	require.Equal(t, "<nil>", nilParams.String())
}

func TestBridgeCallParams_Equal(t *testing.T) {
	params := &bridgeCallParams{
		LeafType:           1,
		DestinationNetwork: 42,
		DestinationAddress: common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		Amount:             big.NewInt(1000),
		Token:              common.HexToAddress("0xbeefdeadbeefdeadbeefdeadbeefdeadbeefdead"),
	}
	logEvent := &agglayerbridge.AgglayerbridgeBridgeEvent{
		LeafType:           params.LeafType,
		DestinationNetwork: params.DestinationNetwork,
		DestinationAddress: params.DestinationAddress,
		Amount:             params.Amount,
	}
	require.True(t, params.Equal(logEvent))
}

const bridgeAssetCallData = "0xcd58657900000000000000000000000000000000000000000000000000000000000000140000000000000000000000005480f3152748809495bd56c14eab4a622aa3a19b00000000000000000000000000000000000000000000000000b1a2bc2ec500000000000000000000000000002dc70fb75b88d2eb4715bc06e1595e6d97c34dff000000000000000000000000000000000000000000000000000000000000000100000000000000000000000000000000000000000000000000000000000000c00000000000000000000000000000000000000000000000000000000000000000"
const bridegeMessageCallData = "0x240ff3780000000000000000000000000000000000000000000000000000000000000001000000000000000000000000afb88881e53589f5e6eb1cc27e9207cc7f03023f0000000000000000000000000000000000000000000000000000000000000001000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000000205dea582a050bb15ca16e0820c023b97f85553fb47d8a86c0c21a5d86bca2631f"

func TestExtractParamFromCallData(t *testing.T) {
	_, err := ExtractParamFromCallData([]byte{0x01, 0x02})
	require.ErrorContains(t, err, "too short")

	_, err = ExtractParamFromCallData([]byte{0x01, 0x02, 0x3, 0x4})
	require.ErrorContains(t, err, "failed to get method")

	calldata := common.FromHex(bridgeAssetCallData)
	_, err = ExtractParamFromCallData(calldata)
	require.NoError(t, err)

	data, err := ExtractParamFromCallData(common.FromHex(bridegeMessageCallData))
	require.NoError(t, err)
	require.Equal(t, &bridgeCallParams{
		LeafType:           1,
		DestinationNetwork: 1,
		DestinationAddress: common.HexToAddress("0xafb88881e53589f5e6eb1cc27e9207cc7f03023f"),
		Amount:             big.NewInt(0),
	}, data)
}

func strPtr(s string) *string {
	return &s
}
