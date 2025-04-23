package bridgesync

import (
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/etrog/polygonzkevmbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/fep/etrog/polygonzkevmbridgev2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/bridgel2sovereignchain"
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

			appenderMap, err := buildAppender(ethClient, bridgeAddr, false)
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
