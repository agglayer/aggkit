package l1infotreesync

import (
	"context"
	"errors"
	"math/big"
	"path"
	"testing"

	"github.com/agglayer/aggkit/sync"
	aggkittypesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGetRollupExitTreeMerkleProof(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetRollupExitTreeMerkleProof(context.Background(), 0, common.Hash{})
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLatestInfoUntilBlock(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLatestL1InfoLeafUntilBlock(context.Background(), 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetInfoByIndex(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetInfoByIndex(context.Background(), 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestL1InfoTreeSync_GetLatestL1InfoGER(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLatestL1InfoGER(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetL1InfoTreeRootByIndex(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetL1InfoTreeRootByIndex(context.Background(), 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLastRollupExitRoot(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLastRollupExitRoot(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLastL1InfoTreeRoot(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLastL1InfoTreeRoot(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLastProcessedBlock(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLastProcessedBlock(context.Background())
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLocalExitRoot(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLocalExitRoot(context.Background(), 0, common.Hash{})
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLastVerifiedBatches(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLastVerifiedBatches(0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetFirstVerifiedBatches(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetFirstVerifiedBatches(0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetFirstVerifiedBatchesAfterBlock(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetFirstVerifiedBatchesAfterBlock(0, 0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetFirstL1InfoWithRollupExitRoot(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetFirstL1InfoWithRollupExitRoot(common.Hash{})
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetLastInfo(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetLastInfo()
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetFirstInfo(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetFirstInfo()
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetFirstInfoAfterBlock(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetFirstInfoAfterBlock(0)
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetL1InfoTreeMerkleProofFromIndexToRoot(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	_, err := s.GetL1InfoTreeMerkleProofFromIndexToRoot(context.Background(), 0, common.Hash{})
	require.Error(t, err)
	require.True(t, errors.Is(err, sync.ErrInconsistentState))
}

func TestGetRPCServices(t *testing.T) {
	s := L1InfoTreeSync{
		processor: &processor{
			halted: true,
		},
	}
	services := s.GetRPCServices()
	require.Equal(t, 1, len(services))
}

func TestIsUpToDate(t *testing.T) {
	t.Parallel()

	t.Run("processor halted", func(t *testing.T) {
		t.Parallel()

		s := L1InfoTreeSync{
			processor: &processor{
				halted: true,
			},
		}

		mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
		ctx := context.Background()
		result, err := s.IsUpToDate(ctx, mockL1Client)

		require.Error(t, err)
		require.True(t, errors.Is(err, sync.ErrInconsistentState))
		require.False(t, result)
	})

	t.Run("GetLastProcessedBlock fails", func(t *testing.T) {
		t.Parallel()

		path := path.Join(t.TempDir(), "l1infotreesyncProcessor.db")
		processor, err := newProcessor(path)
		require.NoError(t, err)
		s := L1InfoTreeSync{
			processor: processor,
		}
		processor.db.Close()

		mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
		ctx := context.Background()
		result, err := s.IsUpToDate(ctx, mockL1Client)

		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get last processed block")
		require.False(t, result)
	})

	t.Run("BlockByNumber fails", func(t *testing.T) {
		t.Parallel()

		path := path.Join(t.TempDir(), "l1infotreesyncProcessor.db")
		processor, err := newProcessor(path)
		require.NoError(t, err)
		defer processor.db.Close()
		s := L1InfoTreeSync{
			processor: processor,
		}

		mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(nil, errors.New("RPC error"))

		ctx := context.Background()
		result, err := s.IsUpToDate(ctx, mockL1Client)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to get the latest finalized L1 block")
		require.False(t, result)
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		path := path.Join(t.TempDir(), "l1infotreesyncProcessor.db")
		processor, err := newProcessor(path)
		require.NoError(t, err)
		defer processor.db.Close()
		s := L1InfoTreeSync{
			processor: processor,
		}

		block := ethtypes.NewBlock(&ethtypes.Header{Number: big.NewInt(0)}, nil, nil, nil)
		mockL1Client := aggkittypesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().HeaderByNumber(mock.Anything, mock.Anything).Return(block.Header(), nil)

		ctx := context.Background()
		result, err := s.IsUpToDate(ctx, mockL1Client)
		require.NoError(t, err)
		require.True(t, result)
	})
}
