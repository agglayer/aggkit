package types

import (
	"bytes"
	"fmt"
	"testing"

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

func TestBlockNumberFinalityWithOffset(t *testing.T) {

func TestBlockNumberFinalityCmp(t *testing.T) {
	finalized, err := NewBlockNumberFinality(FinalizedBlockName)
	require.NoError(t, err)
	safe, err := NewBlockNumberFinality(SafeBlockName)
	require.NoError(t, err)
	require.True(t, safe.GreaterThan(&finalized))
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
