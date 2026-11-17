package bridgetracker

import (
	"testing"

	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestConfig_Validate_FinalizedBlock(t *testing.T) {
	latestMinus3 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: -3}
	finalizedPlus100 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Finalized, Offset: 100}

	tests := []struct {
		name          string
		mutate        func(*Config)
		expectedError string
	}{
		{name: "defaults (empty) are valid", mutate: func(*Config) {}},
		{
			name: "FinalizedBlock, SafeBlock and LatestBlock/-N are valid",
			mutate: func(c *Config) {
				c.L1FinalizedBlock = aggkittypes.FinalizedBlock
				c.L2FinalizedBlock = aggkittypes.SafeBlock
				c.FinalizedBlockByNetwork = map[uint32]aggkittypes.BlockNumberFinality{3: latestMinus3}
			},
		},
		{
			name:          "L1: PendingBlock",
			mutate:        func(c *Config) { c.L1FinalizedBlock = aggkittypes.PendingBlock },
			expectedError: "[Tracker].L1FinalizedBlock",
		},
		{
			name:          "L2: a fixed block number",
			mutate:        func(c *Config) { c.L2FinalizedBlock = *aggkittypes.NewBlockNumber(10) },
			expectedError: "[Tracker].L2FinalizedBlock",
		},
		{
			name:          "L2: offset beyond the allowed one",
			mutate:        func(c *Config) { c.L2FinalizedBlock = finalizedPlus100 },
			expectedError: "[Tracker].L2FinalizedBlock",
		},
		{
			name: "by network: PendingBlock",
			mutate: func(c *Config) {
				c.FinalizedBlockByNetwork = map[uint32]aggkittypes.BlockNumberFinality{7: aggkittypes.PendingBlock}
			},
			expectedError: "[Tracker.FinalizedBlockByNetwork].7",
		},
		{
			name: "by network: empty block",
			mutate: func(c *Config) {
				c.FinalizedBlockByNetwork = map[uint32]aggkittypes.BlockNumberFinality{7: {}}
			},
			expectedError: "[Tracker.FinalizedBlockByNetwork].7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{L1GlobalExitRootAddress: common.HexToAddress("0x1f7ad7caA53e35b4f0D138dC5CBF91aC108a2674")}
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.expectedError == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.expectedError)
		})
	}
}

func TestConfig_FinalizedBlockFor(t *testing.T) {
	latestMinus3 := aggkittypes.BlockNumberFinality{Block: aggkittypes.Latest, Offset: -3}

	t.Run("unset: the default for every network", func(t *testing.T) {
		cfg := Config{}
		require.Equal(t, DefaultFinalizedBlock, cfg.FinalizedBlockFor(0))
		require.Equal(t, DefaultFinalizedBlock, cfg.FinalizedBlockFor(1))
		require.Equal(t, aggkittypes.FinalizedBlock, DefaultFinalizedBlock)
	})

	t.Run("L1 setting for network 0, L2 setting for the rest, overrides first", func(t *testing.T) {
		cfg := Config{
			L1FinalizedBlock: aggkittypes.SafeBlock,
			L2FinalizedBlock: latestMinus3,
			FinalizedBlockByNetwork: map[uint32]aggkittypes.BlockNumberFinality{
				5: aggkittypes.FinalizedBlock,
				0: aggkittypes.LatestBlock, // an override can also pin network 0
			},
		}
		require.Equal(t, aggkittypes.LatestBlock, cfg.FinalizedBlockFor(0))
		require.Equal(t, latestMinus3, cfg.FinalizedBlockFor(1))
		require.Equal(t, aggkittypes.FinalizedBlock, cfg.FinalizedBlockFor(5))

		cfg.FinalizedBlockByNetwork = nil
		require.Equal(t, aggkittypes.SafeBlock, cfg.FinalizedBlockFor(0))
	})

	t.Run("an empty override falls back to the L1/L2 setting", func(t *testing.T) {
		cfg := Config{
			L2FinalizedBlock:        aggkittypes.SafeBlock,
			FinalizedBlockByNetwork: map[uint32]aggkittypes.BlockNumberFinality{2: {}},
		}
		require.Equal(t, aggkittypes.SafeBlock, cfg.FinalizedBlockFor(2))
	})
}

func TestConfig_FinalizedBlockWarnings(t *testing.T) {
	t.Run("nothing to warn about for the recommended value (set or empty)", func(t *testing.T) {
		require.Empty(t, (&Config{}).FinalizedBlockWarnings())
		cfg := Config{
			L1FinalizedBlock:        aggkittypes.FinalizedBlock,
			L2FinalizedBlock:        aggkittypes.FinalizedBlock,
			FinalizedBlockByNetwork: map[uint32]aggkittypes.BlockNumberFinality{2: aggkittypes.FinalizedBlock},
		}
		require.Empty(t, cfg.FinalizedBlockWarnings())
	})

	t.Run("one warning per setting that is not the finalized tag, in a stable order", func(t *testing.T) {
		cfg := Config{
			L1FinalizedBlock: aggkittypes.SafeBlock,
			L2FinalizedBlock: aggkittypes.FinalizedBlock,
			FinalizedBlockByNetwork: map[uint32]aggkittypes.BlockNumberFinality{
				9: aggkittypes.LatestBlock,
				3: {Block: aggkittypes.Latest, Offset: -5},
				4: aggkittypes.FinalizedBlock,
				// a finalized tag with an offset is not the plain tag either
				6: {Block: aggkittypes.Finalized, Offset: -1},
			},
		}
		warnings := cfg.FinalizedBlockWarnings()
		require.Len(t, warnings, 4)
		require.Contains(t, warnings[0], `[Tracker].L1FinalizedBlock = "SafeBlock"`)
		require.Contains(t, warnings[1], `[Tracker.FinalizedBlockByNetwork].3 = "LatestBlock/-5"`)
		require.Contains(t, warnings[2], `[Tracker.FinalizedBlockByNetwork].6 = "FinalizedBlock/-1"`)
		require.Contains(t, warnings[3], `[Tracker.FinalizedBlockByNetwork].9 = "LatestBlock"`)
		for _, warning := range warnings {
			require.Contains(t, warning, "not recommended")
		}
	})
}
