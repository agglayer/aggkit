package bridgesync

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/legacy/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/bridgel2sovereignchain"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	logger "github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestBuildAppender(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	blockNum := uint64(1)

	bridgeV2Abi, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
	require.NoError(t, err)

	bridgeSovereignChainABI, err := bridgel2sovereignchain.Bridgel2sovereignchainMetaData.GetAbi()
	require.NoError(t, err)

	tests := []struct {
		name           string
		eventSignature common.Hash
		callFrame      Call
		logBuilder     func() (types.Log, error)
	}{
		{
			name:           "bridgeEventSignature appender",
			eventSignature: bridgeEventSignature,
			callFrame:      Call{To: bridgeAddr, Input: BridgeAssetMethodID},
			logBuilder: func() (types.Log, error) {
				event, err := bridgeV2Abi.EventByID(bridgeEventSignature)
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
		{
			name:           "claimEventSignaturePreEtrog appender",
			eventSignature: claimEventSignaturePreEtrog,
			callFrame:      Call{To: bridgeAddr},
			logBuilder: func() (types.Log, error) {
				bridgeV1Abi, err := polygonzkevmbridge.PolygonzkevmbridgeMetaData.GetAbi()
				require.NoError(t, err)

				event, err := bridgeV1Abi.EventByID(claimEventSignaturePreEtrog)
				if err != nil {
					return types.Log{}, err
				}

				index := uint32(5)
				originNetwork := uint32(6)
				originAddress := common.HexToAddress("0x20")
				destinationAddress := common.HexToAddress("0x30")
				amount := big.NewInt(10)
				data, err := event.Inputs.Pack(
					index, originNetwork,
					originAddress, destinationAddress, amount)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{claimEventSignaturePreEtrog},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "claimEventSignature appender",
			eventSignature: claimEventSignature,
			callFrame:      Call{To: bridgeAddr},
			logBuilder: func() (types.Log, error) {
				event, err := bridgeV2Abi.EventByID(claimEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				globalIndex := big.NewInt(5)
				originNetwork := uint32(6)
				originAddress := common.HexToAddress("0x20")
				destinationAddress := common.HexToAddress("0x30")
				amount := big.NewInt(10)
				data, err := event.Inputs.Pack(
					globalIndex, originNetwork,
					originAddress, destinationAddress, amount)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{claimEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "tokenMappingEventSignature appender",
			eventSignature: tokenMappingEventSignature,
			callFrame:      Call{To: bridgeAddr},
			logBuilder: func() (types.Log, error) {
				event, err := bridgeV2Abi.EventByID(tokenMappingEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				originNetwork := uint32(10)
				originTokenAddress := common.HexToAddress("0x20")
				wrappedTokenAddress := common.HexToAddress("0x30")
				metadata := []byte{0x40}
				data, err := event.Inputs.Pack(
					originNetwork, originTokenAddress,
					wrappedTokenAddress, metadata)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{tokenMappingEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "setSovereignTokenAddress appender",
			eventSignature: setSovereignTokenEventSignature,
			callFrame:      Call{To: bridgeAddr},
			logBuilder: func() (types.Log, error) {
				event, err := bridgeSovereignChainABI.EventByID(setSovereignTokenEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				originNetwork := uint32(15)
				originTokenAddress := common.HexToAddress("0x25")
				sovereignTokenAddress := common.HexToAddress("0x35")
				isNotMintable := true
				data, err := event.Inputs.Pack(
					originNetwork, originTokenAddress,
					sovereignTokenAddress, isNotMintable)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{setSovereignTokenEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "legacyTokenMigration appender",
			eventSignature: migrateLegacyTokenEventSignature,
			callFrame:      Call{To: bridgeAddr},
			logBuilder: func() (types.Log, error) {
				event, err := bridgeSovereignChainABI.EventByID(migrateLegacyTokenEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				senderAddr := common.HexToAddress("0x5")
				legacyTokenAddr := common.HexToAddress("0x10")
				updatedTokenAddr := common.HexToAddress("0x20")
				amount := big.NewInt(150)
				data, err := event.Inputs.Pack(
					senderAddr, legacyTokenAddr,
					updatedTokenAddr, amount)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{migrateLegacyTokenEventSignature},
					Data:   data,
				}
				return l, nil
			},
		},
		{
			name:           "removeLegacySovereignTokenAddress appender",
			eventSignature: removeLegacySovereignTokenEventSignature,
			callFrame:      Call{To: bridgeAddr},
			logBuilder: func() (types.Log, error) {
				event, err := bridgeSovereignChainABI.EventByID(removeLegacySovereignTokenEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				sovereignTokenAddr := common.HexToAddress("0x5")
				data, err := event.Inputs.Pack(sovereignTokenAddr)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{removeLegacySovereignTokenEventSignature},
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
				Call(mock.Anything, debugTraceTxEndpoint, mock.Anything, mock.Anything).
				Run(func(result any, method string, args ...any) {
					arg, ok := result.(*Call)
					require.True(t, ok)
					*arg = tt.callFrame
				}).
				Return(nil).
				Maybe()

			bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, ethClient)
			require.NoError(t, err)

			logger := logger.WithFields("module", "test")
			appenderMap, err := buildAppender(ethClient, bridgeAddr, false, bridgeContractV2, logger)
			require.NoError(t, err)
			require.NotNil(t, appenderMap)

			block := &sync.EVMBlock{EVMBlockHeader: sync.EVMBlockHeader{Num: blockNum}}

			appenderFunc, exists := appenderMap[tt.eventSignature]
			require.True(t, exists)

			err = appenderFunc(block, log)
			require.NoError(t, err)
			require.Len(t, block.Events, 1)
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
	found, err := findCall(root, bridgeAddr, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, bridgeAddr, found.To)

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
	found, err = findCall(root, bridgeAddr, nil, logger)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, bridgeAddr, found.To)
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
	found, err := findCall(rootCall, bridgeAddr, func(call Call) (bool, error) {
		// Simulate tryDecodeClaimCalldata behavior
		if len(call.Input) < methodIDLength {
			return false, fmt.Errorf("input too short")
		}
		methodID := call.Input[:methodIDLength]

		isClaimInvoked := bytes.Equal(methodID, claimAssetEtrogMethodID) || bytes.Equal(methodID, claimMessageEtrogMethodID)

		return isClaimInvoked, nil
	}, logger)

	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, bridgeAddr, found.To)
	// Note: DFS traversal processes calls in reverse order (stack), so it finds claimMessage first
	require.Equal(t, claimMessageEtrogMethodID, []byte(found.Input[:4])) // Should find the first claim method in DFS order
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

func TestTryDecodeClaimCalldata(t *testing.T) {
	c := &Claim{}
	fromAddr := common.HexToAddress("0x20")
	logger := logger.WithFields("module", "test")

	// Short input should return false, error
	found, err := c.tryDecodeClaimCalldata(fromAddr, []byte{0x01, 0x02, 0x03}, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "input too short: 3 bytes")
	require.False(t, found)

	// Unknown method ID should return false, nil (not error anymore)
	input := make([]byte, methodIDLength)
	copy(input, []byte{0xaa, 0xbb, 0xcc, 0xdd})
	found, err = c.tryDecodeClaimCalldata(fromAddr, input, logger)
	require.NoError(t, err) // Should not return error anymore
	require.False(t, found)

	// Test getProxiedTokensManager method ID (38b8fbbb)
	getProxiedTokensManagerID := []byte{0x38, 0xb8, 0xfb, 0xbb}
	found, err = c.tryDecodeClaimCalldata(fromAddr, getProxiedTokensManagerID, logger)
	require.NoError(t, err) // Should not return error
	require.False(t, found) // Should return false (not a claim method)

	// Valid method ID (simulate claimAssetEtrogMethodID)
	copy(input, claimAssetEtrogMethodID)
	// The rest of the input is not valid ABI, so it will error on unpack
	found, err = c.tryDecodeClaimCalldata(fromAddr, input, logger)
	require.Error(t, err)
	require.False(t, found)
}

func TestSetClaimCalldataFromRoot(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	logger := logger.WithFields("module", "test")

	// Case 1: Root call successful, valid internal call
	rootCall := &Call{
		To:  common.HexToAddress("0x01"),
		Err: nil,
		Calls: []Call{
			{
				To:    bridgeAddr,
				From:  common.HexToAddress("0x20"),
				Err:   nil,
				Input: append(claimAssetEtrogMethodID, []byte{0x00, 0x01, 0x02, 0x03}...), // not valid ABI, but triggers methodID match
			},
		},
	}

	claim := &Claim{}
	err := claim.setClaimCalldataFromRoot(rootCall, bridgeAddr, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "length insufficient")

	// Case 2: Root call reverted
	rootCall = &Call{
		To:  bridgeAddr,
		Err: strPtr("reverted"),
	}

	claim = &Claim{}
	err = claim.setClaimCalldataFromRoot(rootCall, bridgeAddr, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// Case 3: All internal calls reverted
	rootCall = &Call{
		To:  common.HexToAddress("0x01"),
		Err: nil,
		Calls: []Call{
			{
				To:  bridgeAddr,
				Err: strPtr("reverted"),
			},
		},
	}

	claim = &Claim{}
	err = claim.setClaimCalldataFromRoot(rootCall, bridgeAddr, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// Case 4: No matching call
	rootCall = &Call{
		To:    common.HexToAddress("0x01"),
		Err:   nil,
		Calls: []Call{},
	}

	claim = &Claim{}
	err = claim.setClaimCalldataFromRoot(rootCall, bridgeAddr, logger)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestTxnSenderField(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	blockNum := uint64(1)
	expectedTxnSender := common.HexToAddress("0x1234567890123456789012345678901234567890")

	bridgeV2Abi, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
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
						From:  common.HexToAddress("0x20"),
						Err:   nil,
						Input: BridgeAssetMethodID,
					},
				},
			},
			expectedTxnSender: expectedTxnSender,
			logBuilder: func() (types.Log, error) {
				event, err := bridgeV2Abi.EventByID(bridgeEventSignature)
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
		{
			name:           "claimEventSignature with TxnSender",
			eventSignature: claimEventSignature,
			callFrame: Call{
				To:   common.HexToAddress("0x01"),
				From: expectedTxnSender,
				Err:  nil,
				Calls: []Call{
					{
						To:    bridgeAddr,
						From:  common.HexToAddress("0x20"),
						Err:   nil,
						Input: BridgeAssetMethodID,
					},
				},
			},
			expectedTxnSender: expectedTxnSender,
			logBuilder: func() (types.Log, error) {
				event, err := bridgeV2Abi.EventByID(claimEventSignature)
				if err != nil {
					return types.Log{}, err
				}

				globalIndex := big.NewInt(5)
				originNetwork := uint32(6)
				originAddress := common.HexToAddress("0x20")
				destinationAddress := common.HexToAddress("0x30")
				amount := big.NewInt(10)
				data, err := event.Inputs.Pack(
					globalIndex, originNetwork,
					originAddress, destinationAddress, amount)
				if err != nil {
					return types.Log{}, err
				}

				l := types.Log{
					Topics: []common.Hash{claimEventSignature},
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
				Call(mock.Anything, debugTraceTxEndpoint, mock.Anything, mock.Anything).
				Run(func(result any, method string, args ...any) {
					arg, ok := result.(*Call)
					require.True(t, ok)
					*arg = tt.callFrame
				}).
				Return(nil).
				Maybe()

			bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, ethClient)
			require.NoError(t, err)

			logger := logger.WithFields("module", "test")
			appenderMap, err := buildAppender(ethClient, bridgeAddr, false, bridgeContractV2, logger)
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

func strPtr(s string) *string {
	return &s
}
