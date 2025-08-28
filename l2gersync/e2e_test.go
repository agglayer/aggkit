package l2gersync_test

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"testing"
	"time"

	"github.com/agglayer/aggkit/l2gersync"
	"github.com/agglayer/aggkit/test/helpers"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

const (
	retryAfterErrorPeriod      = 30 * time.Millisecond
	maxRetryAttemptsAfterError = 10
	waitForNewBlocksPeriod     = 30 * time.Millisecond
	syncBlockChunkSize         = 10
	testIterations             = 3
	syncDelay                  = 1 * time.Second
)

func TestL2GERSyncE2E(t *testing.T) {
	t.Parallel()
	ctx, _ := context.WithTimeout(context.Background(), 30*time.Minute)

	l1Setup, l2Setup := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))

	dbPathSyncer := path.Join(t.TempDir(), "l2GERSyncTestE2E.sqlite")

	syncer, err := l2gersync.New(
		ctx,
		dbPathSyncer,
		l2Setup.ReorgDetector,
		l2Setup.SimBackend.Client(),
		l2Setup.GERAddr,
		l1Setup.InfoTreeSync,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		aggkittypes.LatestBlock,
		waitForNewBlocksPeriod,
		syncBlockChunkSize,
		true,
	)
	require.NoError(t, err)

	go syncer.Start(ctx)

	for i := range testIterations {
		updateL1GlobalExitRoot(t, l1Setup, i)
		time.Sleep(15 * syncDelay)
		testGERSyncer(t, ctx, l1Setup, l2Setup, syncer, i)
	}
}

func TestL2GERSync_GERRemoval(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	l1Environment, l2Environment := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.SovereignChainL2GERContract))

	dbPathSyncer := path.Join(t.TempDir(), "l2GERSyncTestE2E.sqlite")

	syncer, err := l2gersync.New(
		ctx,
		dbPathSyncer,
		l2Environment.ReorgDetector,
		l2Environment.SimBackend.Client(),
		l2Environment.GERAddr,
		l1Environment.InfoTreeSync,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		aggkittypes.LatestBlock,
		waitForNewBlocksPeriod,
		syncBlockChunkSize,
		true,
	)
	require.NoError(t, err)

	go syncer.Start(ctx)

	updatedGERs := make([]common.Hash, 0, testIterations)
	for i := range testIterations {
		ger := updateL1GlobalExitRoot(t, l1Environment, i)
		updatedGERs = append(updatedGERs, ger)
		time.Sleep(syncDelay)
		testGERSyncer(t, ctx, l1Environment, l2Environment, syncer, i)
	}

	removeGERsUntilIdx := testIterations / 2
	gersToRemove := make([][common.HashLength]byte, 0, removeGERsUntilIdx)
	for _, ger := range updatedGERs[:removeGERsUntilIdx] {
		gersToRemove = append(gersToRemove, ger)
	}

	_, err = l2Environment.GERManagerSovereignSC.RemoveGlobalExitRoots(
		l2Environment.Auth, gersToRemove)
	require.NoError(t, err)
	l2Environment.SimBackend.Commit()

	// wait for the GER removal events to be processed
	lb, err := l2Environment.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)
	helpers.RequireProcessorUpdated(t, syncer, lb, l2Environment.SimBackend.Client())

	for _, removedGER := range gersToRemove {
		isInjected, err := l2Environment.AggoracleSender.IsGERInjected(removedGER)
		require.NoError(t, err)
		require.False(t, isInjected)
	}

	for _, updatedGER := range updatedGERs[removeGERsUntilIdx:] {
		isInjected, err := l2Environment.AggoracleSender.IsGERInjected(updatedGER)
		require.NoError(t, err)
		require.True(t, isInjected)
	}
}

func TestL2GERSync_IndexLegacyGERManagerSC(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l1Setup, l2Setup := helpers.NewSimulatedEVMEnvironment(t, helpers.DefaultEnvironmentConfig(helpers.LegacyL2GERContract))

	dbPathSyncer := path.Join(t.TempDir(), "l2GERSyncTestE2E.sqlite")

	l2GERSyncer, err := l2gersync.New(
		ctx,
		dbPathSyncer,
		l2Setup.ReorgDetector,
		l2Setup.SimBackend.Client(),
		l2Setup.GERAddr,
		l1Setup.InfoTreeSync,
		retryAfterErrorPeriod,
		maxRetryAttemptsAfterError,
		aggkittypes.LatestBlock,
		waitForNewBlocksPeriod,
		syncBlockChunkSize,
		true,
	)
	require.NoError(t, err)

	go l2GERSyncer.Start(ctx)

	startBlockNumber, err := l2Setup.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)
	for i := range testIterations {
		updateL1GlobalExitRoot(t, l1Setup, i)
		updateL2GlobalExitRoot(t, l2Setup, i)
		// wait for the GER to be indexed
		time.Sleep(syncDelay)
	}

	l1Setup.SimBackend.Commit()
	l2Setup.SimBackend.Commit()
	time.Sleep(1 * time.Second)

	endBlockNumber, err := l2Setup.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)

	injectedGERs, err := l2GERSyncer.GetInjectedGERsForRange(ctx, startBlockNumber, endBlockNumber)
	require.NoError(t, err)
	require.Len(t, injectedGERs, testIterations)

	mer, err := l2Setup.GERManagerLegacySC.LastMainnetExitRoot(nil)
	require.NoError(t, err)
	for i := range testIterations {
		expectedGER := crypto.Keccak256Hash(mer[:], common.HexToHash(fmt.Sprintf("%x", i)).Bytes())
		ger, ok := injectedGERs[expectedGER]
		require.True(t, ok, fmt.Sprintf("GER for iteration %d not found", i))
		require.Equal(t, expectedGER, ger.GlobalExitRoot, fmt.Sprintf("GER mismatch for iteration %d", i))
	}
}

func updateL1GlobalExitRoot(t *testing.T, l1 *helpers.L1Environment, i int) common.Hash {
	t.Helper()

	rollupExitRoot := common.HexToHash(strconv.Itoa(i))
	_, err := l1.GERContract.UpdateExitRoot(l1.Auth, rollupExitRoot)
	require.NoError(t, err)
	l1.SimBackend.Commit()

	mainnetExitRoot, err := l1.GERContract.LastMainnetExitRoot(nil)
	require.NoError(t, err)

	return crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
}

func updateL2GlobalExitRoot(t *testing.T, l2 *helpers.L2Environment, i int) common.Hash {
	t.Helper()

	rollupExitRoot := common.HexToHash(strconv.Itoa(i))
	_, err := l2.GERManagerLegacySC.UpdateExitRoot(l2.Auth, rollupExitRoot)
	require.NoError(t, err)
	l2.SimBackend.Commit()

	mainnetExitRoot, err := l2.GERManagerLegacySC.LastMainnetExitRoot(nil)
	require.NoError(t, err)

	return crypto.Keccak256Hash(mainnetExitRoot[:], rollupExitRoot[:])
}

func testGERSyncer(t *testing.T, ctx context.Context,
	l1Setup *helpers.L1Environment, l2Setup *helpers.L2Environment,
	syncer *l2gersync.L2GERSync, i int) {
	t.Helper()
	time.Sleep(2 * time.Second)

	expectedGER, err := l1Setup.GERContract.GetLastGlobalExitRoot(&bind.CallOpts{Pending: false})
	require.NoError(t, err)

	isInjected, err := l2Setup.AggoracleSender.IsGERInjected(expectedGER)
	require.NoError(t, err)
	require.True(t, isInjected, fmt.Sprintf("iteration %d, GER: %s", i, common.Hash(expectedGER)))

	lb, err := l2Setup.SimBackend.Client().BlockNumber(ctx)
	require.NoError(t, err)
	helpers.RequireProcessorUpdated(t, syncer, lb, l2Setup.SimBackend.Client())

	e, err := syncer.GetFirstGERAfterL1InfoTreeIndex(ctx, uint32(i))
	require.NoError(t, err, fmt.Sprintf("iteration: %d", i))
	require.Equal(t, common.Hash(expectedGER), e.GlobalExitRoot, fmt.Sprintf("iteration: %d", i))
}
