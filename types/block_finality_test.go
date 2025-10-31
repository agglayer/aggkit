package types

import (
	"bytes"
	"context"
	"fmt"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

type configTest struct {
	BlockFinality BlockNumberFinality `mapstructure:"BlockFinality"`
}

func TestBlockNumberFinalityReadFromConfigFile(t *testing.T) {
	cfg, err := readConfigFile[configTest](t, "BlockFinality = \"SafeBlock\"")
	require.NoError(t, err)
	require.Equal(t, Safe, cfg.BlockFinality.Block)
	_, err = readConfigFile[configTest](t, "BlockFinality = \"badname\"")
	require.Error(t, err)
	_, err = readConfigFile[configTest](t, "BlockFinality = \"\"")
	require.Error(t, err)
}

func TestBlockNumberFinalityWithOffset(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedResult string
		expectedErr    error
	}{
		{
			name:           "valid finalized block",
			input:          FinalizedBlockName,
			expectedResult: FinalizedBlockName,
		},
		{
			name:           "valid finalized block",
			input:          FinalizedBlockName + "/+5",
			expectedResult: FinalizedBlockName + "/5",
		},
		{
			name:           "valid finalized block",
			input:          FinalizedBlockName + "/-5",
			expectedResult: FinalizedBlockName + "/-5",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var b BlockNumberFinality
			err := b.UnmarshalText([]byte(testCase.input))

			if testCase.expectedErr == nil {
				require.Equal(t, testCase.expectedResult, b.String())
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), testCase.expectedErr.Error())
			}
		})
	}
}

func TestBlockNumberFinality_LessFinalThan(t *testing.T) {
	tests := []struct {
		name           string
		firstFinality  BlockNumberFinality
		secondFinality BlockNumberFinality
		isLessFinal    bool
	}{
		{
			name:           "empty finality less final than pending block type",
			firstFinality:  BlockNumberFinality{}, // IsEmpty()
			secondFinality: PendingBlock,
			isLessFinal:    true,
		},
		{
			name:           "pending block type less final than latest block type",
			firstFinality:  PendingBlock,
			secondFinality: LatestBlock,
			isLessFinal:    true,
		},
		{
			name:           "pending block type less final than safe block type",
			firstFinality:  PendingBlock,
			secondFinality: SafeBlock,
			isLessFinal:    true,
		},
		{
			name:           "pending block type less final than fianlized block type",
			firstFinality:  PendingBlock,
			secondFinality: FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name:           "latest block type less final than pending block type",
			firstFinality:  LatestBlock,
			secondFinality: SafeBlock,
			isLessFinal:    true,
		},
		{
			name:           "latest block type less final than finalzed block type",
			firstFinality:  LatestBlock,
			secondFinality: FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name:           "safe block type less final than finalized block type",
			firstFinality:  SafeBlock,
			secondFinality: FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name:           "safe block type less final than finalized block type",
			firstFinality:  SafeBlock,
			secondFinality: FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name: "finalized block type less strict due to offset",
			firstFinality: BlockNumberFinality{
				Block:  Safe,
				Offset: 1,
			},
			secondFinality: BlockNumberFinality{
				Block:  Safe,
				Offset: 5,
			},
			isLessFinal: false,
		},
		{
			name: "finalized block type stricter due to offset",
			firstFinality: BlockNumberFinality{
				Block:  Safe,
				Offset: 1,
			},
			secondFinality: BlockNumberFinality{
				Block:  Safe,
				Offset: -5,
			},
			isLessFinal: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.firstFinality.LessFinalThan(tt.secondFinality)
			require.Equal(t, tt.isLessFinal, result)
		})
	}
}

func TestBlockNumber_ApplyOffset(t *testing.T) {
	tests := []struct {
		name           string
		blockType      BlockNumber
		blockNumber    uint64
		offset         int64
		expectedResult uint64
	}{
		{
			name:           "positive offset",
			blockType:      0,
			blockNumber:    100,
			offset:         5,
			expectedResult: 105,
		},
		{
			name:           "negative offset within range",
			blockType:      0,
			blockNumber:    100,
			offset:         -10,
			expectedResult: 90,
		},
		{
			name:           "negative offset, capped to zero",
			blockType:      0,
			blockNumber:    5,
			offset:         -10,
			expectedResult: 0,
		},
		{
			name:           "negative offset below zero",
			blockType:      0,
			blockNumber:    50,
			offset:         -100,
			expectedResult: 0,
		},
		{
			name:           "zero offset",
			blockType:      0,
			blockNumber:    123,
			offset:         0,
			expectedResult: 123,
		},
		{
			name:           "latest block ignores positive offset",
			blockType:      Latest,
			blockNumber:    500,
			offset:         10,
			expectedResult: 500,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.blockType.ApplyOffset(tt.blockNumber, tt.offset)
			require.Equal(t, tt.expectedResult, result)
		})
	}
}

func readConfigFile[T any](t *testing.T, configData string) (T, error) {
	t.Helper()
	viper.SetConfigType("toml")
	err := viper.ReadConfig(bytes.NewBuffer([]byte(configData)))
	require.NoError(t, err)
	decodeHooks := []viper.DecoderConfigOption{
		// this allows arrays to be decoded from env var separated by ",", example: MY_VAR="value1,value2,value3"
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(),
			mapstructure.StringToSliceHookFunc(","),
		)),
	}
	var cfg T
	err = viper.Unmarshal(&cfg, decodeHooks...)
	return cfg, err
}

func TestBlockNumberFinality(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedResult BlockNumberFinality
		expectedErr    error
	}{
		{
			name:           "valid finalized block",
			input:          "FinalizedBlock",
			expectedResult: FinalizedBlock,
		},
		{
			name:           "valid safe block",
			input:          "SafeBlock",
			expectedResult: SafeBlock,
		},
		{
			name:           "valid pending block",
			input:          "PendingBlock",
			expectedResult: PendingBlock,
		},
		{
			name:           "valid latest block",
			input:          "LatestBlock",
			expectedResult: LatestBlock,
		},
		{
			name:        "invalid block",
			input:       "InvalidBlock",
			expectedErr: fmt.Errorf("invalid finality keyword: InvalidBlock"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var b BlockNumberFinality
			err := b.UnmarshalText([]byte(testCase.input))

			if testCase.expectedErr == nil {
				require.Equal(t, testCase.expectedResult, b)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), testCase.expectedErr.Error())
			}
		})
	}
}

func TestBlockNumberFinalityJSONSchema(t *testing.T) {
	schema := BlockNumberFinality{}.JSONSchema()
	require.Equal(t, "string", schema.Type)
	require.Equal(t, "BlockNumberFinality", schema.Title)
}

func TestBlockNumberFinality_BlockHeader(t *testing.T) {
	ctx := context.Background()

	t.Run("Success with offset", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		blockFinality := BlockNumberFinality{Block: Finalized, Offset: -5}

		finalizedHeader := &types.Header{Number: big.NewInt(100)}
		offsetHeader := &types.Header{Number: big.NewInt(95)}

		mockClient.EXPECT().HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber))).Return(finalizedHeader, nil).Once()
		mockClient.EXPECT().HeaderByNumber(ctx, big.NewInt(95)).Return(offsetHeader, nil).Once()

		result, err := blockFinality.BlockHeader(ctx, mockClient)
		require.NoError(t, err)
		require.Equal(t, offsetHeader, result)
	})

	t.Run("Error on first call", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		blockFinality := BlockNumberFinality{Block: Latest, Offset: 0}

		testErr := fmt.Errorf("first call error")
		mockClient.EXPECT().HeaderByNumber(ctx, (*big.Int)(nil)).Return(nil, testErr).Once()

		result, err := blockFinality.BlockHeader(ctx, mockClient)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), testErr.Error())
	})

	t.Run("Error on second call", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		// Safe with positive offset so the resolved block differs from the base and triggers a second fetch
		blockFinality := BlockNumberFinality{Block: Safe, Offset: 10}

		safeHeader := &types.Header{Number: big.NewInt(100)}
		testErr := fmt.Errorf("second call error")

		// First call resolves base Safe header (100)
		mockClient.EXPECT().HeaderByNumber(ctx, big.NewInt(int64(rpc.SafeBlockNumber))).Return(safeHeader, nil).Once()
		// Second call attempts to fetch 110 and fails
		mockClient.EXPECT().HeaderByNumber(ctx, big.NewInt(110)).Return(nil, testErr).Once()

		result, err := blockFinality.BlockHeader(ctx, mockClient)
		require.Error(t, err)
		require.Nil(t, result)
		require.Contains(t, err.Error(), testErr.Error())
	})
}
