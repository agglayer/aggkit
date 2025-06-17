package bridgesync

import (
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/bridgel2sovereignchain"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
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
		callFrame      call
		logBuilder     func() (types.Log, error)
	}{
		{
			name:           "bridgeEventSignature appender",
			eventSignature: bridgeEventSignature,
			callFrame:      call{To: bridgeAddr},
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
			callFrame:      call{To: bridgeAddr},
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
			callFrame:      call{To: bridgeAddr},
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
			callFrame:      call{To: bridgeAddr},
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
			callFrame:      call{To: bridgeAddr},
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
			callFrame:      call{To: bridgeAddr},
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
			callFrame:      call{To: bridgeAddr},
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
				Call(&tt.callFrame, debugTraceTxEndpoint, mock.Anything, mock.Anything).
				Return(nil).
				Maybe()

			bridgeContractV2, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeAddr, ethClient)
			require.NoError(t, err)

			appenderMap, err := buildAppender(ethClient, bridgeAddr, false, bridgeContractV2)
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

	// Simple direct call
	root := call{
		To:   bridgeAddr,
		From: fromAddr,
		Err:  nil,
	}
	found, err := findCall(root, bridgeAddr, nil)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, bridgeAddr, found.To)

	// Reverted call should be skipped
	root = call{
		To:   bridgeAddr,
		From: fromAddr,
		Err:  strPtr("reverted"),
	}
	_, err = findCall(root, bridgeAddr, nil)
	require.Error(t, err)

	// Nested call, only inner is not reverted
	root = call{
		To:   common.HexToAddress("0x01"),
		From: fromAddr,
		Err:  nil,
		Calls: []call{
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
	found, err = findCall(root, bridgeAddr, nil)
	require.NoError(t, err)
	require.NotNil(t, found)
	require.Equal(t, bridgeAddr, found.To)
}

func TestTryDecodeClaimCalldata(t *testing.T) {
	c := &Claim{}
	fromAddr := common.HexToAddress("0x20")

	// Short input should return false, nil
	found, err := c.tryDecodeClaimCalldata(fromAddr, []byte{0x01, 0x02, 0x03})
	require.NoError(t, err)
	require.False(t, found)

	// Unknown method ID should return false, nil
	input := make([]byte, methodIDLength)
	copy(input, []byte{0xaa, 0xbb, 0xcc, 0xdd})
	found, err = c.tryDecodeClaimCalldata(fromAddr, input)
	require.NoError(t, err)
	require.False(t, found)

	// Valid method ID (simulate claimAssetEtrogMethodID)
	copy(input, claimAssetEtrogMethodID)
	// The rest of the input is not valid ABI, so it will error on unpack
	found, err = c.tryDecodeClaimCalldata(fromAddr, input)
	require.Error(t, err)
	require.False(t, found)
}

func TestSetClaimCalldata(t *testing.T) {
	bridgeAddr := common.HexToAddress("0x10")
	txHash := common.HexToHash("0x1234")
	client := &mocks.RPCClienter{}

	// Case 1: Root call successful, valid internal call
	rootCall := &call{
		To:  common.HexToAddress("0x01"),
		Err: nil,
		Calls: []call{
			{
				To:    bridgeAddr,
				From:  common.HexToAddress("0x20"),
				Err:   nil,
				Input: append(claimAssetEtrogMethodID, []byte{0x00, 0x01, 0x02, 0x03}...), // not valid ABI, but triggers methodID match
			},
		},
	}
	client.On("Call", mock.Anything, debugTraceTxEndpoint, txHash, mock.Anything).Run(func(args mock.Arguments) {
		arg, ok := args.Get(0).(*call)
		require.True(t, ok)
		*arg = *rootCall
	}).Return(nil)

	claim := &Claim{}
	err := claim.setClaimCalldata(client, bridgeAddr, txHash)
	require.Error(t, err)
	require.Contains(t, err.Error(), "length insufficient")

	// Case 2: Root call reverted
	rootCall = &call{
		To:  bridgeAddr,
		Err: strPtr("reverted"),
	}
	client.On("Call", mock.Anything, debugTraceTxEndpoint, txHash, mock.Anything).Run(func(args mock.Arguments) {
		arg, ok := args.Get(0).(*call)
		require.True(t, ok)
		*arg = *rootCall
	}).Return(nil)

	claim = &Claim{}
	err = claim.setClaimCalldata(client, bridgeAddr, txHash)
	require.Error(t, err)
	require.Contains(t, err.Error(), "root call reverted")

	// Case 3: All internal calls reverted
	rootCall = &call{
		To:  common.HexToAddress("0x01"),
		Err: nil,
		Calls: []call{
			{
				To:  bridgeAddr,
				Err: strPtr("reverted"),
			},
		},
	}
	client.On("Call", mock.Anything, debugTraceTxEndpoint, txHash, mock.Anything).Run(func(args mock.Arguments) {
		arg, ok := args.Get(0).(*call)
		require.True(t, ok)
		*arg = *rootCall
	}).Return(nil)

	claim = &Claim{}
	err = claim.setClaimCalldata(client, bridgeAddr, txHash)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// Case 4: No matching call
	rootCall = &call{
		To:    common.HexToAddress("0x01"),
		Err:   nil,
		Calls: []call{},
	}
	client.On("Call", mock.Anything, debugTraceTxEndpoint, txHash, mock.Anything).Run(func(args mock.Arguments) {
		arg, ok := args.Get(0).(*call)
		require.True(t, ok)
		*arg = *rootCall
	}).Return(nil)

	claim = &Claim{}
	err = claim.setClaimCalldata(client, bridgeAddr, txHash)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func strPtr(s string) *string {
	return &s
}
