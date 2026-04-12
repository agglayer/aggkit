package query

import (
	"errors"
	"testing"

	agglayermocks "github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/mocks"
	claimsynctypesmocks "github.com/agglayer/aggkit/claimsync/types/mocks"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
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

func TestSetClaimSyncerNextRequiredBlock_Success(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetLastSettledCertificateToBlock(ctx, certHeader).Return(uint64(42), nil)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(42)).Return(nil)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.NoError(t, err)
}

func TestSetClaimSyncerNextRequiredBlock_GetLatestSettledCertHeaderError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, _, agglayerClient := newTestSetter(t)

	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).
		Return(nil, errors.New("agglayer unavailable"))

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "agglayer unavailable")
}

func TestSetClaimSyncerNextRequiredBlock_GetLastSettledCertToBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetLastSettledCertificateToBlock(ctx, certHeader).
		Return(uint64(0), errors.New("db error"))

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "db error")
}

func TestSetClaimSyncerNextRequiredBlock_SetNextRequiredBlockError(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	setter, certQuerier, agglayerClient := newTestSetter(t)

	certHeader := &agglayertypes.CertificateHeader{}
	agglayerClient.EXPECT().GetLatestSettledCertificateHeader(ctx, uint32(1)).Return(certHeader, nil)
	certQuerier.EXPECT().GetLastSettledCertificateToBlock(ctx, certHeader).Return(uint64(10), nil)

	claimSyncer := claimsynctypesmocks.NewClaimSyncer(t)
	claimSyncer.EXPECT().SetNextRequiredBlock(ctx, uint64(10)).Return(errors.New("syncer error"))

	err := setter.SetClaimSyncerNextRequiredBlock(ctx, claimSyncer, noRetryHandler())
	require.ErrorIs(t, err, aggkitcommon.ErrExecutionFails)
	require.ErrorContains(t, err, "syncer error")
}
