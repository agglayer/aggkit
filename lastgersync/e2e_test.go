package lastgersync_test

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/lastgersync"
	"github.com/agglayer/aggkit/test/helpers"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestE2E(t *testing.T) {
	ctx := context.Background()
	setup := helpers.NewE2EEnvWithEVML2(t)
	dbPathSyncer := path.Join(t.TempDir(), "lastGERSyncTestE2E.sqlite")
	const (
		retryAfterErrorPeriod      = time.Millisecond * 30
		maxRetryAttemptsAfterError = 10
		waitForNewBlocksPeriod     = time.Millisecond * 30
		syncBlockChunkSize         = 10
	)
	syncer, err := lastgersync.New(
		ctx,
		dbPathSyncer,
		setup.L2Environment.ReorgDetector,
		setup.L2Environment.SimBackend.Client(),
		setup.L2Environment.GERAddr,
		setup.InfoTreeSync,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		etherman.LatestBlock,
		waitForNewBlocksPeriod,
		syncBlockChunkSize,
	)
	require.NoError(t, err)
	go syncer.Start(ctx)

	for i := 0; i < 10; i++ {
		// Update GER on L1
		_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, common.HexToHash(strconv.Itoa(i)))
		require.NoError(t, err)
		setup.L1Environment.SimBackend.Commit()
		time.Sleep(time.Millisecond * 150)
		expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
		require.NoError(t, err)
		isInjected, err := setup.AggoracleSender.IsGERInjected(expectedGER)
		require.NoError(t, err)
		require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))

		// Wait for syncer to catch up
		lb, err := setup.L2Environment.SimBackend.Client().BlockNumber(ctx)
		require.NoError(t, err)
		helpers.RequireProcessorUpdated(t, syncer, lb)

		e, err := syncer.GetFirstGERAfterL1InfoTreeIndex(ctx, uint32(i))
		require.NoError(t, err, fmt.Sprint("iteration: ", i))
		require.Equal(t, common.Hash(expectedGER), e.GlobalExitRoot, fmt.Sprint("iteration: ", i))
	}
}
