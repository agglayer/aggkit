package types_test

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

var (
	blockFinalityEmpty      *aggkittypes.BlockNumberFinality
	blockFinalityCreated    = aggkittypes.BlockNumberFinality{}
	BlockFinalitySafeOffset = aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5}
)

type configTest struct {
	BlockFinality aggkittypes.BlockNumberFinality `mapstructure:"BlockFinality"`
}

func TestBlockNumberFinality_String(t *testing.T) {
	require.Equal(t, "nil", blockFinalityEmpty.String())
	latest, err := aggkittypes.NewBlockNumberFinality("latestBlock")
	require.NoError(t, err)
	require.Equal(t, "LatestBlock", latest.String())
	latest.Offset = -5
	require.Equal(t, "LatestBlock/-5", latest.String())
}

func TestBlockNumberFinality_Equal(t *testing.T) {
	bn1 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5}
	bn2 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5}
	bn3 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: 0}
	bn4 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: -5}
	require.False(t, blockFinalityEmpty.Equal(bn1), "bn1 should not be equal to empty finality")
	require.True(t, bn1.Equal(bn2), "bn1 should be equal to bn2")
	require.False(t, bn1.Equal(bn3), "bn1 should not be equal to bn3")
	require.False(t, bn1.Equal(bn4), "bn1 should not be equal to bn4")
}

func TestBlockNumberFinality_IsEmpty(t *testing.T) {
	require.True(t, blockFinalityEmpty.IsEmpty(), "empty finality should be empty")
	bn1 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5}
	require.False(t, bn1.IsEmpty(), "bn1 should not be empty")
	require.True(t, blockFinalityCreated.IsEmpty(), "empty finality should be empty")
}

func TestBlockNumberFinality_IsFinalized(t *testing.T) {
	require.True(t, aggkittypes.FinalizedBlock.IsFinalized(), "FinalizedBlock should be finalized")
	require.False(t, aggkittypes.SafeBlock.IsFinalized(), "SafeBlock should not be finalized")
	require.False(t, blockFinalityEmpty.IsFinalized())
	require.False(t, blockFinalityCreated.IsFinalized())
}
func TestBlockNumberFinality_IsSafe(t *testing.T) {
	require.False(t, aggkittypes.FinalizedBlock.IsSafe(), "FinalizedBlock should not be safe")
	require.True(t, aggkittypes.SafeBlock.IsSafe(), "SafeBlock should be safe")
	require.False(t, blockFinalityEmpty.IsSafe())
	require.False(t, blockFinalityCreated.IsSafe())
	require.True(t, BlockFinalitySafeOffset.IsSafe())
}

func TestBlockNumberFinalityReadFromConfigFile(t *testing.T) {
	cfg, err := readConfigFile[configTest](t, "BlockFinality = \"SafeBlock\"")
	require.NoError(t, err)
	require.Equal(t, aggkittypes.Safe, cfg.BlockFinality.Block)
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
			input:          aggkittypes.FinalizedBlockName,
			expectedResult: aggkittypes.FinalizedBlockName,
		},
		{
			name:           "valid finalized block",
			input:          aggkittypes.FinalizedBlockName + "/+5",
			expectedResult: aggkittypes.FinalizedBlockName + "/5",
		},
		{
			name:           "valid finalized block",
			input:          aggkittypes.FinalizedBlockName + "/-5",
			expectedResult: aggkittypes.FinalizedBlockName + "/-5",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var b aggkittypes.BlockNumberFinality
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
		firstFinality  aggkittypes.BlockNumberFinality
		secondFinality aggkittypes.BlockNumberFinality
		isLessFinal    bool
	}{
		{
			name:           "empty finality less final than pending block type",
			firstFinality:  aggkittypes.BlockNumberFinality{}, // IsEmpty()
			secondFinality: aggkittypes.PendingBlock,
			isLessFinal:    true,
		},
		{
			name:           "pending block type less final than latest block type",
			firstFinality:  aggkittypes.PendingBlock,
			secondFinality: aggkittypes.LatestBlock,
			isLessFinal:    true,
		},
		{
			name:           "pending block type less final than safe block type",
			firstFinality:  aggkittypes.PendingBlock,
			secondFinality: aggkittypes.SafeBlock,
			isLessFinal:    true,
		},
		{
			name:           "pending block type less final than fianlized block type",
			firstFinality:  aggkittypes.PendingBlock,
			secondFinality: aggkittypes.FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name:           "latest block type less final than pending block type",
			firstFinality:  aggkittypes.LatestBlock,
			secondFinality: aggkittypes.SafeBlock,
			isLessFinal:    true,
		},
		{
			name:           "latest block type less final than finalzed block type",
			firstFinality:  aggkittypes.LatestBlock,
			secondFinality: aggkittypes.FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name:           "safe block type less final than finalized block type",
			firstFinality:  aggkittypes.SafeBlock,
			secondFinality: aggkittypes.FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name:           "safe block type less final than finalized block type",
			firstFinality:  aggkittypes.SafeBlock,
			secondFinality: aggkittypes.FinalizedBlock,
			isLessFinal:    true,
		},
		{
			name: "finalized block type less strict due to offset",
			firstFinality: aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Safe,
				Offset: 1,
			},
			secondFinality: aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Safe,
				Offset: 5,
			},
			isLessFinal: false,
		},
		{
			name: "finalized block type stricter due to offset",
			firstFinality: aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Safe,
				Offset: 1,
			},
			secondFinality: aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Safe,
				Offset: -5,
			},
			isLessFinal: true,
		},
		{
			name: "finalized is not LessFinalThan safe",
			firstFinality: aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Finalized,
				Offset: 0,
			},
			secondFinality: aggkittypes.BlockNumberFinality{
				Block:  aggkittypes.Safe,
				Offset: 0,
			},
			isLessFinal: false,
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
		blockType      aggkittypes.BlockNumber
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
			blockType:      aggkittypes.Latest,
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
		expectedResult aggkittypes.BlockNumberFinality
		expectedErr    error
	}{
		{
			name:           "valid finalized block",
			input:          "FinalizedBlock",
			expectedResult: aggkittypes.FinalizedBlock,
		},
		{
			name:           "valid safe block",
			input:          "SafeBlock",
			expectedResult: aggkittypes.SafeBlock,
		},
		{
			name:           "valid pending block",
			input:          "PendingBlock",
			expectedResult: aggkittypes.PendingBlock,
		},
		{
			name:           "valid latest block",
			input:          "LatestBlock",
			expectedResult: aggkittypes.LatestBlock,
		},
		{
			name:        "invalid block",
			input:       "InvalidBlock",
			expectedErr: fmt.Errorf("invalid finality keyword: InvalidBlock"),
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			var b aggkittypes.BlockNumberFinality
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
	schema := aggkittypes.BlockNumberFinality{}.JSONSchema()
	require.Equal(t, "string", schema.Type)
	require.Equal(t, "BlockNumberFinality", schema.Title)
}
func TestBlockNumberFinality_BlockNumber(t *testing.T) {
	ctx := t.Context()
	mockClient := mocks.NewBaseEthereumClienter(t)
	finalizedHeader := &types.Header{Number: big.NewInt(100)}
	mockClient.EXPECT().HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber))).Return(finalizedHeader, nil).Maybe()
	_, err := blockFinalityEmpty.BlockNumber(ctx, mockClient)
	require.Error(t, err)
	_, err = blockFinalityCreated.BlockNumber(ctx, mockClient)
	require.Error(t, err)
	number, err := aggkittypes.FinalizedBlock.BlockNumber(ctx, mockClient)
	require.NoError(t, err)
	require.Equal(t, finalizedHeader.Number.Uint64(), number)
}

func TestBlockNumberFinality_BlockHeader(t *testing.T) {
	ctx := t.Context()

	t.Run("Success with offset", func(t *testing.T) {
		mockClient := mocks.NewBaseEthereumClienter(t)
		blockFinality := aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: -5}

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
		blockFinality := aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: 0}

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
		blockFinality := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: 10}

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

func TestBlockNumberFinality_Validate(t *testing.T) {
	tests := []struct {
		name          string
		finality      aggkittypes.BlockNumberFinality
		expectedError string
	}{
		{
			name:          "LatestBlock with positive offset should fail",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: 1},
			expectedError: fmt.Sprintf("positive offset 1 exceeds maximum allowed %d for LatestBlock", aggkittypes.MaxPositiveOffsetLatest),
		},
		{
			name:          "LatestBlock with zero offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: 0},
			expectedError: "",
		},
		{
			name:          "LatestBlock with negative offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: -5},
			expectedError: "",
		},
		{
			name:          "PendingBlock with positive offset should fail",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Pending, Offset: 1},
			expectedError: fmt.Sprintf("positive offset 1 exceeds maximum allowed %d for PendingBlock", aggkittypes.MaxPositiveOffsetPending),
		},
		{
			name:          "PendingBlock with zero offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Pending, Offset: 0},
			expectedError: "",
		},
		{
			name:          "SafeBlock with offset exceeding limit should fail",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: aggkittypes.MaxPositiveOffsetSafe + 1},
			expectedError: fmt.Sprintf("positive offset %d exceeds maximum allowed %d for SafeBlock", aggkittypes.MaxPositiveOffsetSafe+1, aggkittypes.MaxPositiveOffsetSafe),
		},
		{
			name:          "SafeBlock with offset at limit should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: aggkittypes.MaxPositiveOffsetSafe},
			expectedError: "",
		},
		{
			name:          "SafeBlock with offset below limit should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: aggkittypes.MaxPositiveOffsetSafe - 1},
			expectedError: "",
		},
		{
			name:     "FinalizedBlock with offset exceeding limit should fail",
			finality: aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: aggkittypes.MaxPositiveOffsetFinalized + 1},
			expectedError: fmt.Sprintf("positive offset %d exceeds maximum allowed %d for FinalizedBlock",
				aggkittypes.MaxPositiveOffsetFinalized+1, aggkittypes.MaxPositiveOffsetFinalized),
		},
		{
			name:          "FinalizedBlock with offset at limit should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: aggkittypes.MaxPositiveOffsetFinalized},
			expectedError: "",
		},
		{
			name:          "FinalizedBlock with offset below limit should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: aggkittypes.MaxPositiveOffsetFinalized - 1},
			expectedError: "",
		},
		{
			name:          "SafeBlock with negative offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -10},
			expectedError: "",
		},
		{
			name:          "FinalizedBlock with negative offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: -10},
			expectedError: "",
		},
		{
			name:          "Empty block should fail validation",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Empty, Offset: 0},
			expectedError: "block type must be one of LatestBlock, SafeBlock, FinalizedBlock, or PendingBlock",
		},
		{
			name:          "Empty block with positive offset should fail validation",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Empty, Offset: 100},
			expectedError: "block type must be one of LatestBlock, SafeBlock, FinalizedBlock, or PendingBlock",
		},
		{
			name:          "Unknown block type should fail validation",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.BlockNumber(999), Offset: 0},
			expectedError: "block type must be one of LatestBlock, SafeBlock, FinalizedBlock, or PendingBlock",
		},
		{
			name:          "Valid block with zero offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: 0},
			expectedError: "",
		},
		{
			name:          "Valid block with negative offset should pass",
			finality:      aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5},
			expectedError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.finality.Validate()
			if tt.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedError)
			}
		})
	}
}

func TestBlockNumberFinalityEqual(t *testing.T) {
	bn1 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5}
	bn2 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: -5}
	bn3 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Safe, Offset: 0}
	bn4 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: -5}

	require.False(t, blockFinalityEmpty.Equal(bn1), "bn1 should not be equal to empty finality")
	require.True(t, bn1.Equal(bn2), "bn1 should be equal to bn2")
	require.False(t, bn1.Equal(bn3), "bn1 should not be equal to bn3")
	require.False(t, bn1.Equal(bn4), "bn1 should not be equal to bn4")
}
