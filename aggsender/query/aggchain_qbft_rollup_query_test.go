package query

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/agglayer/aggkit/aggsender/mocks"
	"github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	typesmocks "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAggchainQbftRollupQuerier(t *testing.T) {
	t.Parallel()
	logger := log.WithFields("test", "aggchainQbftRollupQuerier")
	addr := common.HexToAddress("0x1")

	t.Run("zero address", func(t *testing.T) {
		t.Parallel()
		_, err := NewQbftQuerier(logger, aggkitcommon.ZeroAddress, nil)
		require.ErrorContains(t, err, "AggchainQBFT contract address is zero")
	})

	t.Run("error on constructor reading lastProvenBlockNumber from L1", func(t *testing.T) {
		t.Parallel()
		mockL1Client := typesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("some error")).Once()

		_, err := NewQbftQuerier(logger, addr, mockL1Client)
		require.ErrorContains(t, err, "error AggchainQBFT.LastProvenBlockNumber")
	})

	t.Run("reads lastProvenBlockNumber through the generated binding", func(t *testing.T) {
		t.Parallel()
		mockL1Client := typesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
			Return(common.LeftPadBytes(big.NewInt(1234).Bytes(), 32), nil).Once()

		querier, err := NewQbftQuerier(logger, addr, mockL1Client)
		require.NoError(t, err)
		require.Equal(t, uint64(1234), querier.StartL2Block())
		require.True(t, querier.IsFEP())
	})

	t.Run("caller returns error", func(t *testing.T) {
		t.Parallel()
		mockCaller := mocks.NewQBFTContractQuerier(t)
		mockCaller.EXPECT().LastProvenBlockNumber((*bind.CallOpts)(nil)).Return(0, errors.New("mock error")).Once()

		_, err := newQbftQuerier(logger, addr, mockCaller)
		require.ErrorContains(t, err, "mock error")
	})

	t.Run("start block and last settled block", func(t *testing.T) {
		t.Parallel()
		mockCaller := mocks.NewQBFTContractQuerier(t)
		mockCaller.EXPECT().LastProvenBlockNumber((*bind.CallOpts)(nil)).Return(uint64(1000), nil).Once()
		querier, err := newQbftQuerier(logger, addr, mockCaller)
		require.NoError(t, err)
		require.Equal(t, uint64(1000), querier.StartL2Block())
		require.True(t, querier.IsFEP())

		mockCaller.EXPECT().LastProvenBlockNumber((*bind.CallOpts)(nil)).Return(uint64(1500), nil).Once()
		lastSettled, err := querier.GetLastSettledL2Block()
		require.NoError(t, err)
		require.Equal(t, uint64(1500), lastSettled)
		// the start block is fixed at construction
		require.Equal(t, uint64(1000), querier.StartL2Block())

		mockCaller.EXPECT().LastProvenBlockNumber((*bind.CallOpts)(nil)).Return(uint64(0), errors.New("boom")).Once()
		_, err = querier.GetLastSettledL2Block()
		require.ErrorContains(t, err, "error getting lastProvenBlockNumber from AggchainQBFT")
	})
}

func TestNewRollupQuerier(t *testing.T) {
	t.Parallel()
	logger := log.WithFields("test", "NewRollupQuerier")
	addr := common.HexToAddress("0x1")

	t.Run("PP mode does not ask for the aggchain type", func(t *testing.T) {
		t.Parallel()
		committee := mocks.NewMultisigQuerier(t)
		q, err := NewRollupQuerier(logger, committee, types.PessimisticProofMode, addr, nil)
		require.NoError(t, err)
		require.False(t, q.IsFEP())
	})

	t.Run("error getting aggchain type", func(t *testing.T) {
		t.Parallel()
		committee := mocks.NewMultisigQuerier(t)
		committee.EXPECT().AggchainType().Return("", errors.New("rpc down")).Once()
		_, err := NewRollupQuerier(logger, committee, types.AggchainProofMode, addr, nil)
		require.ErrorContains(t, err, "error getting aggchain type: rpc down")
	})

	t.Run("QBFT type selects the QBFT querier", func(t *testing.T) {
		t.Parallel()
		committee := mocks.NewMultisigQuerier(t)
		committee.EXPECT().AggchainType().Return(types.AggchainTypeQBFT, nil).Once()
		mockL1Client := typesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
			Return(common.LeftPadBytes([]byte{7}, 32), nil).Once()

		q, err := NewRollupQuerier(logger, committee, types.AggchainProofMode, addr, mockL1Client)
		require.NoError(t, err)
		require.IsType(t, &aggchainQbftRollupQuerier{}, q)
		require.Equal(t, uint64(7), q.StartL2Block())
	})

	t.Run("FEP type selects the FEP querier", func(t *testing.T) {
		t.Parallel()
		committee := mocks.NewMultisigQuerier(t)
		committee.EXPECT().AggchainType().Return(types.AggchainTypeFEP, nil).Once()
		mockL1Client := typesmocks.NewBaseEthereumClienter(t)
		mockL1Client.EXPECT().CallContract(mock.Anything, mock.Anything, mock.Anything).
			Return(common.LeftPadBytes([]byte{9}, 32), nil).Once()

		q, err := NewRollupQuerier(logger, committee, types.AggchainProofMode, addr, mockL1Client)
		require.NoError(t, err)
		require.IsType(t, &aggchainFEPRollupQuerier{}, q)
		require.Equal(t, uint64(9), q.StartL2Block())
	})
}

type fakeBridgeRoot struct {
	root [32]byte
	err  error
	opts *bind.CallOpts
}

func (f *fakeBridgeRoot) GetRoot(opts *bind.CallOpts) ([32]byte, error) {
	f.opts = opts
	return f.root, f.err
}

type fakeAggchainBase struct {
	aggchainType [2]byte
	err          error
	selector     [4]byte
}

func (f *fakeAggchainBase) GetAggchainTypeFromSelector(_ *bind.CallOpts, selector [4]byte) ([2]byte, error) {
	f.selector = selector
	return f.aggchainType, f.err
}

func TestQbftChainQuery(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	blockHash := common.HexToHash("0xabcd")

	t.Run("GetBlockHash returns the hash given by the node", func(t *testing.T) {
		t.Parallel()
		l2 := typesmocks.NewBaseEthereumClienter(t)
		l2.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(10)).
			Return(&aggkittypes.BlockHeader{Number: 10, Hash: blockHash}, nil).Once()
		sut := &qbftChainQuery{l2Client: l2}

		got, err := sut.GetBlockHash(ctx, 10)
		require.NoError(t, err)
		require.Equal(t, blockHash, got)

		l2.EXPECT().CustomHeaderByNumber(mock.Anything, aggkittypes.NewBlockNumber(11)).
			Return(nil, errors.New("not found")).Once()
		_, err = sut.GetBlockHash(ctx, 11)
		require.ErrorContains(t, err, "error getting L2 block 11: not found")
	})

	t.Run("GetBridgeRootAtBlock queries getRoot at that block", func(t *testing.T) {
		t.Parallel()
		bridge := &fakeBridgeRoot{root: blockHash}
		sut := &qbftChainQuery{bridgeL2: bridge}

		got, err := sut.GetBridgeRootAtBlock(ctx, 25)
		require.NoError(t, err)
		require.Equal(t, blockHash, got)
		require.Equal(t, uint64(25), bridge.opts.BlockNumber.Uint64())

		bridge.err = errors.New("reverted")
		_, err = sut.GetBridgeRootAtBlock(ctx, 25)
		require.ErrorContains(t, err, "error getting L2 bridge root at block 25")
	})

	t.Run("GetAggchainTypeFromSelector", func(t *testing.T) {
		t.Parallel()
		base := &fakeAggchainBase{aggchainType: aggchainQBFT}
		sut := &qbftChainQuery{aggchainBase: base}

		got, err := sut.GetAggchainTypeFromSelector([4]byte{0, 1, 0, 2})
		require.NoError(t, err)
		require.Equal(t, types.AggchainTypeQBFT, got)
		require.Equal(t, [4]byte{0, 1, 0, 2}, base.selector)

		base.aggchainType = [2]byte{0xFF, 0xFF}
		_, err = sut.GetAggchainTypeFromSelector([4]byte{})
		require.ErrorContains(t, err, "unsupported aggchain type")

		base.err = errors.New("rpc")
		_, err = sut.GetAggchainTypeFromSelector([4]byte{})
		require.ErrorContains(t, err, "error getting aggchain type from selector")
	})

	t.Run("constructor", func(t *testing.T) {
		t.Parallel()
		l1 := typesmocks.NewBaseEthereumClienter(t)
		l2 := typesmocks.NewBaseEthereumClienter(t)
		q, err := NewQBFTChainQuery(l1, l2, common.HexToAddress("0x1"), common.HexToAddress("0x2"))
		require.NoError(t, err)
		require.NotNil(t, q)
	})
}
