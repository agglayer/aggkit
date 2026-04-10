package query

import (
	"errors"
	"math/big"
	"testing"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	claimsynctypesmocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/stretchr/testify/require"
)

func newTestSetter(t *testing.T) (
	*SetInitialBlockToClaimSyncer,
	*mocks.CertificateQuerier,
	*agglayermocks.AgglayerClientMock,
) {
	t.Helper()
	certQuerier := mocks.NewCertificateQuerier(t)
	agglayerClient := agglayermocks.NewAgglayerClientMock(t)
	logger := log.WithFields("module", "test")
	setter := NewSetInitialBlockToClaimSyncer(certQuerier, agglayerClient, uint32(1), logger)
	return setter, certQuerier, agglayerClient
}

// noRetryHandler executes exactly once with no sleep.
func noRetryHandler() *aggkitcommon.RetryHandlerDelays {
	return aggkitcommon.NewRetryHandler(nil, 0)
}

func TestSetClaimSyncerNextRequiredBlock_NilClaimSyncer(t *testing.T) {
	t.Parallel()
	setter, _, _ := newTestSetter(t)

	err := setter.SetClaimSyncerNextRequiredBlock(t.Context(), nil, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_AlreadyHasProcessedBlocks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, _ := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(100), true, nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_GetLastProcessedBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, _ := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, errors.New("storage error"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorContains(t, err, "storage error")
}

func TestSetClaimSyncerNextRequiredBlock_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, certHeader).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlock: 42, LastImportedBridgeExitBlock: 42})
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(42)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_GetLatestSettledCertHeaderError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, agglayerClient := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).
		Return(nil, errors.New("agglayer unavailable"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "agglayer unavailable")
}

func TestSetClaimSyncerNextRequiredBlock_GetLastSettledCertToBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, certHeader).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlockErr: errors.New("db error")})

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "db error")
}

func TestSetClaimSyncerNextRequiredBlock_SetNextRequiredBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, certHeader).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlock: 10, LastImportedBridgeExitBlock: 10})
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(10)).Return(errors.New("syncer error"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "syncer error")
}

func TestSetClaimSyncerNextRequiredBlock_NilCertFromAgglayer(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(nil, nil)
	// nil cert means no settled certificate yet; GetBlockNumbersFromCertHeader is still called
	// with nil and returns only the FEP start block (bridge/import queries are skipped).
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, (*agglayertypes.CertificateHeader)(nil)).
		Return(aggsendertypes.SettledBlocks{LastBridgeExitBlock: 10, LastImportedBridgeExitBlock: 10})
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(10)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_RPCFallback_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	globalIndex := big.NewInt(7)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	claimSyncer.EXPECT().GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(80), true, nil)
	// earliest = min(100, 80) = 80
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(80)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_RPCFallback_NotFound(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	globalIndex := big.NewInt(7)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	claimSyncer.EXPECT().GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "no claim found for global index")
}

func TestSetClaimSyncerNextRequiredBlock_RPCFallback_Error(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	globalIndex := big.NewInt(7)
	settledIBE := &agglayertypes.SettledImportedBridgeExit{GlobalIndex: globalIndex}
	certHeader := &agglayertypes.CertificateHeader{}
	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().GetLastProcessedBlock(ctx).Return(uint64(0), false, nil)
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetBlockNumbersFromCertHeader(ctx, certHeader).Return(aggsendertypes.SettledBlocks{
		LastBridgeExitBlock:            100,
		LastImportedBridgeExitBlockErr: errors.New("claim not in db"),
		SettledImportedBridgeExit:      settledIBE,
	})
	claimSyncer.EXPECT().GetLatestBlockNumByGlobalIndexFromRPC(ctx, globalIndex, (*aggkittypes.BlockNumberFinality)(nil)).
		Return(uint64(0), false, errors.New("rpc error"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "rpc error")
}
