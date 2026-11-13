package main

import (
	"context"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/claimsync"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/reorgdetector"
	aggsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestStartL1ConsumersIfNeeded_ClaimSyncAutoStartResolvedNil is a regression test for the startup
// logging in startL1ConsumersIfNeeded dereferencing cfg.ClaimL1Sync.AutoStart.Resolved. The config
// passed to startup is the ORIGINAL one, whose AutoStart stays in "auto" mode with a nil Resolved
// pointer (the resolution done by runClaimSyncL1IfNeeded happens on a by-value copy and only the
// internal ClaimSync config holds the resolved pointer). The startup logging must therefore use the
// mode string and never dereference Resolved, while the real ClaimSync still starts fine from its
// separately resolved internal config.
func TestStartL1ConsumersIfNeeded_ClaimSyncAutoStartResolvedNil(t *testing.T) {
	ctx := context.Background()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	// Original config exactly as produced by the defaults: AutoStart=auto, Resolved=nil.
	originalCfg := config.Config{}
	originalCfg.ClaimL1Sync = claimsync.ConfigStandalone{
		ConfigEmbedded: claimsync.ConfigEmbedded{
			DBQueryTimeout: configtypes.NewDuration(30 * time.Second),
			BridgeAddr:     bridge,
		},
		DBPath:                             path.Join(t.TempDir(), "claimsync_l1.sqlite"),
		BlockFinality:                      aggkittypes.FinalizedBlock,
		InitialBlockNum:                    1000,
		SyncBlockChunkSize:                 100,
		RetryAfterErrorPeriod:              configtypes.NewDuration(time.Second),
		MaxRetryAttemptsAfterError:         -1,
		WaitForNewBlocksPeriod:             configtypes.NewDuration(time.Second),
		RequireStorageContentCompatibility: false,
		AutoStart:                          configtypes.AutoMode,
		Downloader:                         configtypes.LegacyDownloader,
	}
	require.Nil(t, originalCfg.ClaimL1Sync.AutoStart.Resolved, "precondition: original config is unresolved")

	// The real ClaimSync is built from a separately resolved copy, mimicking
	// runClaimSyncL1IfNeeded: Resolve mutates only the local copy's AutoStart.Resolved.
	claimCfg := originalCfg.ClaimL1Sync
	require.True(t, claimCfg.AutoStart.Resolve(true))
	require.Nil(t, originalCfg.ClaimL1Sync.AutoStart.Resolved,
		"resolving the copy must not touch the original config")

	ethClient := mocksethclient.NewEthClienter(t)
	ethClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{To: &bridge, Data: common.Hex2Bytes("14cc01a0")}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).Once()

	rd := aggsync.NewReorgDetectorMock(t)
	rd.EXPECT().GetFinalizedBlockType().Return(aggkittypes.FinalizedBlock)
	rd.EXPECT().Subscribe(claimsynctypes.L1ClaimSyncer.String()).
		Return(&reorgdetector.Subscription{
			ReorgedBlock:   make(chan uint64),
			ReorgProcessed: make(chan bool),
		}, nil)

	l1ClaimSync, err := claimsync.NewStandaloneClaimSync(
		ctx, claimCfg, rd, ethClient, claimsynctypes.L1ClaimSyncer, 0)
	require.NoError(t, err)
	require.NotNil(t, l1ClaimSync)

	// A canceled startup context makes the driver exit immediately (no RPC, no hung goroutine).
	startCtx, cancel := context.WithCancel(context.Background())
	cancel()

	require.NotPanics(t, func() {
		startL1ConsumersIfNeeded(
			startCtx,
			[]string{aggkitcommon.BRIDGE},
			originalCfg,
			ethClient,
			nil, // l1MultiDownloader
			nil, // l1InfoTreeSync
			l1ClaimSync,
			nil, // l1BridgeSync
			nil, // wg (only the claim branch runs)
		)
	})

	require.Nil(t, originalCfg.ClaimL1Sync.AutoStart.Resolved,
		"startup must not resolve the original config")
	require.Equal(t, "auto", originalCfg.ClaimL1Sync.AutoStart.String())
}
