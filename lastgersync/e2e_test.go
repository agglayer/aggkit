package lastgersync_test

import (
	"context"
	"fmt"
	"log"
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

const (
	retryAfterErrorPeriod      = 30 * time.Millisecond
	maxRetryAttemptsAfterError = 10
	waitForNewBlocksPeriod     = 30 * time.Millisecond
	syncBlockChunkSize         = 10
	testIterations             = 10
	syncDelay                  = 150 * time.Millisecond
)

func TestLastGERSyncE2E(t *testing.T) {
	ctx := context.Background()
	setup := helpers.NewE2EEnvWithEVML2(t, helpers.DefaultEnvironmentConfig())
	dbPathSyncer := path.Join(t.TempDir(), "lastGERSyncTestE2E.sqlite")

	syncer, err := lastgersync.New(
		ctx,
		dbPathSyncer,
		setup.L2Environment.ReorgDetector,
		setup.L2Environment.SimBackend.Client(),
		setup.L2Environment.GERAddr,
		syncBlockChunkSize,
		setup.InfoTreeSync,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		etherman.LatestBlock,
		waitForNewBlocksPeriod,
		syncBlockChunkSize,
		true,
	)
	require.NoError(t, err)

	go func() {
		if err := syncer.Start(ctx); err != nil {
			log.Fatalf("lastGERSync failed: %s", err)
		}
	}()

	for i := 0; i < testIterations; i++ {
		updateGlobalExitRoot(t, setup, i)
		time.Sleep(syncDelay)
		testGERSyncer(t, ctx, setup, syncer, i)
	}
}

func updateGlobalExitRoot(t *testing.T, setup *helpers.AggoracleWithEVMChain, i int) {
	t.Helper()

	_, err := setup.L1Environment.GERContract.UpdateExitRoot(setup.L1Environment.Auth, common.HexToHash(strconv.Itoa(i)))
	require.NoError(t, err)
	setup.L1Environment.SimBackend.Commit()
}

func testGERSyncer(t *testing.T, ctx context.Context, setup *helpers.AggoracleWithEVMChain, syncer *lastgersync.LastGERSync, i int) {
	t.Helper()

	expectedGER, err := setup.L1Environment.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)

	isInjected, err := setup.AggoracleSender.IsGERInjected(expectedGER)
	require.NoError(t, err)
	require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Bytes2Hex(expectedGER[:])))

	lb, err := setup.L2Environment.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)
	helpers.RequireProcessorUpdated(t, syncer, lb)

	e, err := syncer.GetFirstGERAfterL1InfoTreeIndex(ctx, uint32(i))
	require.NoError(t, err, fmt.Sprintf("iteration: %d", i))
	require.Equal(t, common.Hash(expectedGER), e.GlobalExitRoot, fmt.Sprintf("iteration: %d", i))
}
