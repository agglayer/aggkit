package multidownloader

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sync"
	"testing"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdsync "github.com/agglayer/aggkit/multidownloader/sync"
	aggkitsync "github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/test/contracts/logemitter"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/ethereum/go-ethereum/params"
	"github.com/stretchr/testify/require"
)

var (
	pingSignature = crypto.Keccak256Hash([]byte("Ping(address,uint256,string)"))
)

type mdrE2ESimulatedEnv struct {
	SimulatedL1        *simulated.Backend
	LogEmitterAddr     common.Address
	LogEmitterContract *logemitter.Logemitter
	ethClient          *etherman.DefaultEthClient
	auth               *bind.TransactOpts
}

type PingEvent struct {
	BlockPosition uint64
	From          common.Address
	Id            uint64
	Message       string
}

type LogemitterEvent struct {
	PingEvent *PingEvent
}

func logemitterAppender(contract *logemitter.Logemitter) aggkitsync.LogAppenderMap {
	appender := make(aggkitsync.LogAppenderMap)
	appender[pingSignature] = func(b *aggkitsync.EVMBlock, l types.Log) error {
		event, err := contract.ParsePing(l)
		b.Events = append(b.Events, &LogemitterEvent{PingEvent: &PingEvent{
			BlockPosition: uint64(l.Index),
			From:          event.From,
			Id:            event.Id.Uint64(),
			Message:       event.Message,
		}})
		return err
	}
	return appender
}

type logemitterProcessor struct {
	logger    *log.Logger
	mdr       *EVMMultidownloader
	mutex     sync.Mutex
	lastBlock *aggkittypes.BlockHeader
	events    map[uint64]*aggkitsync.Block
}

func (p *logemitterProcessor) GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.lastBlock, nil
}
func (p *logemitterProcessor) ProcessBlock(ctx context.Context, block aggkitsync.Block) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.lastBlock = &aggkittypes.BlockHeader{
		Number: block.Num,
		Hash:   block.Hash,
	}
	p.logger.Infof("Processed block number %d / %s with %d events",
		block.Num, block.Hash.Hex(), len(block.Events))
	if p.events == nil {
		p.events = make(map[uint64]*aggkitsync.Block)
	}
	p.events[block.Num] = &block
	return nil
}
func (p *logemitterProcessor) Reorg(ctx context.Context, firstReorgedBlock uint64) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.logger.Infof("Processing reorg from block number %d", firstReorgedBlock)
	hdr, err := p.mdr.ethClient.CustomHeaderByNumber(ctx, aggkittypes.NewBlockNumber(firstReorgedBlock-1))
	if err != nil {
		return err
	}
	p.logger.Infof("New last block after reorg: %s", hdr.String())
	p.lastBlock = hdr
	// remove reorged events from p.events
	for blkNum := range p.events {
		if blkNum >= firstReorgedBlock {
			delete(p.events, blkNum)
		}
	}
	return nil
}

func (p *logemitterProcessor) lastPingEvent() *PingEvent {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	var lastEvent *PingEvent
	var lastBlockNum uint64
	for blkNum, block := range p.events {
		for _, ev := range block.Events {
			logEv, ok := ev.(*LogemitterEvent)
			if !ok {
				continue
			}
			if logEv.PingEvent != nil {
				if blkNum >= lastBlockNum {
					lastBlockNum = blkNum
					lastEvent = logEv.PingEvent
				}
			}
		}
	}
	return lastEvent
}

func newLogemitterSyncer(t *testing.T, mdr *EVMMultidownloader,
	contract *logemitter.Logemitter,
	syncerConfig aggkittypes.SyncerConfig) (*mdsync.EVMDriver,
	*logemitterProcessor, *mdsync.EVMDownloader) {
	t.Helper()
	logger := log.WithFields("module", "sync_logemitter")
	downloader := mdsync.NewEVMDownloader(
		mdr,
		logger,
		&aggkitsync.RetryHandler{
			MaxRetryAttemptsAfterError: 5,
		},
		logemitterAppender(contract),
		1*time.Minute,
		1*time.Second,
	)

	processor := &logemitterProcessor{
		logger: logger,
		mdr:    mdr,
	}

	driver := mdsync.NewEVMDriver(
		logger,
		processor,
		downloader,
		syncerConfig,
		100,
		&aggkitsync.RetryHandler{
			MaxRetryAttemptsAfterError: 5,
		},
		nil,
	)
	// TODO: Register syncer must be done by driver?
	err := mdr.RegisterSyncer(syncerConfig)
	require.NoError(t, err)
	return driver, processor, downloader
}

func buildL1Simulated(t *testing.T) *mdrE2ESimulatedEnv {
	t.Helper()
	// Generate key + address
	key, err := crypto.GenerateKey()
	require.NoError(t, err)
	from := crypto.PubkeyToAddress(key.PublicKey)
	// Genesis
	alloc := types.GenesisAlloc{
		from: {Balance: big.NewInt(0).Mul(big.NewInt(100), big.NewInt(params.Ether))}, // 100 ETH
	}
	envL1 := simulated.NewBackend(alloc, simulated.WithBlockGasLimit(10000000))
	chainID := big.NewInt(1337)
	auth, err := bind.NewKeyedTransactorWithChainID(key, chainID)
	require.NoError(t, err)
	logEmitterAddr, _, logEmitterContract, err := logemitter.DeployLogemitter(auth, envL1.Client(), "msg")
	require.NoError(t, err)
	require.NotEqual(t, logEmitterAddr, nil)
	require.NotNil(t, logEmitterContract)

	envL1.Commit()
	return &mdrE2ESimulatedEnv{
		SimulatedL1:        envL1,
		LogEmitterAddr:     logEmitterAddr,
		LogEmitterContract: logEmitterContract,
		ethClient:          etherman.NewDefaultEthClient(envL1.Client(), nil, nil),
		auth:               auth,
	}
}

func newMultidownloader(t *testing.T, testData *mdrE2ESimulatedEnv) *EVMMultidownloader {
	t.Helper()
	cfg := NewConfigDefault("e2e_test", t.TempDir())
	// This log logger will only log errors to avoid cluttering the test output
	logger, _, err := log.NewLogger(log.Config{
		Level:       "error",
		Environment: "development",
		Outputs:     []string{"stdout"},
	})
	require.NoError(t, err)
	store, err := storage.NewMultidownloaderStorage(logger,
		storage.MultidownloaderStorageConfig{
			DBPath: cfg.StoragePath,
		})
	require.NoError(t, err)
	simulatedFinalized, err := aggkittypes.NewBlockNumberFinality("LatestBlock/-5")
	require.NoError(t, err)
	_, err = testData.ethClient.CustomHeaderByNumber(t.Context(), simulatedFinalized)
	require.NoError(t, err)

	cfg.BlockFinality = *simulatedFinalized
	cfg.WaitPeriodToCheckCatchUp = configtypes.Duration{Duration: 100 * time.Millisecond}
	cfg.PeriodToCheckReorgs = configtypes.Duration{Duration: 500 * time.Millisecond}

	mdr, err := NewEVMMultidownloader(
		logger,
		cfg,
		"mdr_e2e_custom_syncer",
		testData.ethClient,
		nil, // rpcClient
		store,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, mdr)
	return mdr
}

func TestE2E_CustomSyncer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	var err error
	testData := buildL1Simulated(t)
	mdr := newMultidownloader(t, testData)
	syncerConfig := aggkittypes.SyncerConfig{
		SyncerID: "log_emitter_e2e_test_custom_syncer",
		ContractAddresses: []common.Address{
			testData.LogEmitterAddr,
		},
		FromBlock: 0,
		ToBlock:   aggkittypes.LatestBlock,
	}

	driver, processor, _ := newLogemitterSyncer(t, mdr, testData.LogEmitterContract, syncerConfig)
	ctx := context.TODO()
	err = mdr.Initialize(ctx)
	require.NoError(t, err)

	// It's important, mdr must be started
	go func() {
		err := mdr.Start(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			require.NoError(t, err)
		}
	}()
	go func() {
		driver.Sync(ctx)
	}()

	for numReorgs := 0; numReorgs < 3; numReorgs++ {
		var blocks []*types.Header
		var lastBlock *types.Header
		var logIndex int64
		for i := 0; i < 10; i++ {
			logIndex++
			log.Infof("Emitting ping %d", logIndex)
			_, err = testData.LogEmitterContract.EmitPing(testData.auth,
				big.NewInt(logIndex),
				fmt.Sprintf("iteration %d", logIndex))
			require.NoError(t, err)
			testData.SimulatedL1.Commit() // Block 3
			hdr, err := testData.ethClient.HeaderByNumber(ctx, nil)
			require.NoError(t, err)
			if blocks == nil {
				blocks = make([]*types.Header, 0)
			}
			if lastBlock == nil || (lastBlock.Number.Uint64() != hdr.Number.Uint64()) {
				blocks = append(blocks, hdr)
				lastBlock = hdr
			}
		}
		// Catch up
		for {
			lastPing := processor.lastPingEvent()
			log.Infof("Catching up: last ping id: %+v", lastPing)
			if lastPing != nil && lastPing.Id == uint64(logIndex) {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		lastProcessedBlock, err := processor.GetLastProcessedBlockHeader(ctx)
		require.NoError(t, err)
		// Pick a random  index to fork (minimum 1 block must be refactored)
		chooseBlockIndex := rand.Intn(len(blocks) - 2)
		err = testData.SimulatedL1.Fork(blocks[chooseBlockIndex].Hash())
		require.NoError(t, err)
		testData.SimulatedL1.Commit() // reorg chain: Block 4
		for {
			currentBlock, err := processor.GetLastProcessedBlockHeader(ctx)
			require.NoError(t, err)
			log.Infof("Catching up after reorg: previousLastBlock (%d) !=  currentLastBlock=%d", lastProcessedBlock.Number, currentBlock.Number)
			if currentBlock.Number != lastProcessedBlock.Number {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		log.Infof("Finish reorg %d", numReorgs)
	}
	log.Info("Finish tests")
}
