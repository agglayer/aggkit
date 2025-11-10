package multidownloader

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	"github.com/agglayer/aggkit/reorgdetector"
	aggkitsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/stretchr/testify/require"
)

const runL1InfoTree = true
const l1InfoTreeUseMultidownloader = false

func TestEVMMultidownloader(t *testing.T) {
	t.Skip("code to test/debug not real unittest")
	cfgLog := log.Config{
		Environment: "development",
		Level:       "info",
		Outputs:     []string{"stderr"},
	}
	log.Init(cfgLog)
	l1url := os.Getenv("L1URL")
	ethClient, err := ethclient.Dial(l1url)
	if err != nil {
		log.Fatalf("failed to create client for L1 using URL: %s. Err:%v", l1url, err)
	}
	block, err := ethClient.BlockByNumber(t.Context(), nil) // Test connection
	require.NoError(t, err)
	log.Infof("Connected to Ethereum. Current block: %d", block.Number().Uint64())

	logger := log.WithFields("test", "test")

	db, err := storage.NewMultidownloaderStorage(logger, storage.MultidownloaderStorageConfig{
		DBPath: "/tmp/mdr_test.sqlite",
	})
	require.NoError(t, err)
	cfg := Config{
		BlockChunkSize:                  5000,
		MaxParallelBlockHeaderRetrieval: 50,
		BlockFinality:                   aggkittypes.FinalizedBlock,
	}
	mdr, err := NewEVMMultidownloader(logger, cfg, "l1", ethClient, db, nil)
	require.NoError(t, err)
	require.NotNil(t, mdr)
	mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID: "test_syncer",
		ContractsAddr: []common.Address{
			common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78"), // GERManager
			common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764"), // RollupManager
		},
		FromBlock: 5157574,
		ToBlock:   aggkittypes.LatestBlock,
	})

	ctx := context.TODO()
	var l1infotree *l1infotreesync.L1InfoTreeSync
	if runL1InfoTree == true {
		var multidownloader aggkittypes.MultiDownloader
		var dbPath string
		if l1InfoTreeUseMultidownloader {
			multidownloader = mdr
			dbPath = "/tmp/l1infotree_md.sqlite"
		} else {
			multidownloader = aggkitsync.NewAdaptEthClient(ethClient)
			dbPath = "/tmp/l1infotree_eth.sqlite"
		}
		reorgDetector, err := reorgdetector.New(ethClient, reorgdetector.Config{
			DBPath:              "/tmp/l1_reorgdetector.sqlite",
			CheckReorgsInterval: types.NewDuration(time.Second * 10),
			FinalizedBlock:      aggkittypes.FinalizedBlock,
		}, reorgdetector.L1)
		require.NoError(t, err)

		l1infotree, err = l1infotreesync.New(
			ctx,
			l1infotreesync.Config{
				DBPath:             dbPath,
				InitialBlock:       5157574,
				GlobalExitRootAddr: common.HexToAddress("0x2968d6d736178f8fe7393cc33c87f29d9c287e78"),
				RollupManagerAddr:  common.HexToAddress("0xe2ef6215adc132df6913c8dd16487abf118d1764"),
				SyncBlockChunkSize: 6500,
				WaitForNewBlocksPeriod: types.Duration{
					Duration: 5 * time.Second,
				},
				BlockFinality: aggkittypes.FinalizedBlock,
			},
			multidownloader,
			reorgDetector,
			l1infotreesync.FlagStopOnFinalizedBlockReached,
		)
		require.NoError(t, err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := aggkitcommon.TimeTracker{}
		timer.Start()
		err = mdr.Start(ctx)
		timer.Stop()
		log.Infof("Multidownloader sync finished in %s. err: %w", timer.String(), err)
		require.NoError(t, err)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		timer := aggkitcommon.TimeTracker{}
		timer.Start()
		if l1infotree != nil {
			l1infotree.Start(t.Context())
		}
		timer.Stop()
		log.Infof("L1InfoTree sync finished in %s", timer.String())
	}()
	wg.Wait()
}

func TestEVMMultidownloaderExtractSuggestedBlockRangeFromErrorMsg(t *testing.T) {
	br := extractSuggestedBlockRangeFromErrorMsg("Query returned more than 20000 results. Try with this block range [0x852c16, 0x853273].")
	require.NotNil(t, br)
	require.Equal(t, uint64(8727574), br.FromBlock)
	require.Equal(t, uint64(8729203), br.ToBlock)
}
