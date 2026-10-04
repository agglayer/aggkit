package multidownloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	aggkitcommon "github.com/agglayer/aggkit/common"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	mdsync "github.com/agglayer/aggkit/multidownloader/sync"
	mdrsynctypes "github.com/agglayer/aggkit/multidownloader/sync/types"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggkitsync "github.com/agglayer/aggkit/sync"
	"github.com/agglayer/aggkit/test/contracts/logemitter"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rpc"
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
	events    map[uint64]*aggkitsync.EVMBlock
}

func (p *logemitterProcessor) GetLastProcessedBlockHeader(ctx context.Context) (*aggkittypes.BlockHeader, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.lastBlock, nil
}

func (tp *logemitterProcessor) ProcessBlocks(ctx context.Context, blocks *mdrsynctypes.DownloadResult) error {
	if blocks == nil || len(blocks.Data) == 0 {
		return nil
	}
	for _, block := range blocks.Data {
		if err := tp.ProcessBlock(ctx, block); err != nil {
			return err
		}
	}
	return nil
}

func (p *logemitterProcessor) ProcessBlock(ctx context.Context, block *aggkitsync.EVMBlock) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.lastBlock = &aggkittypes.BlockHeader{
		Number: block.Num,
		Hash:   block.Hash,
	}
	p.logger.Infof("Processed block number %d / %s with %d events",
		block.Num, block.Hash.Hex(), len(block.Events))
	if p.events == nil {
		p.events = make(map[uint64]*aggkitsync.EVMBlock)
	}
	p.events[block.Num] = block
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
	return newMultidownloaderWithFinality(t, testData, "LatestBlock/-5")
}

func newMultidownloaderWithFinality(t *testing.T, testData *mdrE2ESimulatedEnv,
	finality string) *EVMMultidownloader {
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
	simulatedFinalized, err := aggkittypes.NewBlockNumberFinality(finality)
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
		fromBlock := syncerConfig.FromBlock
		driver.Sync(ctx, &fromBlock)
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

// batchLimitedRPCClient is an RPCClienter on top of the simulated backend (which has no batch support)
// that rejects JSON-RPC batches larger than maxBatch like a capped provider does.
type batchLimitedRPCClient struct {
	env      *mdrE2ESimulatedEnv
	maxBatch int
	rejectFn func() error

	mu    sync.Mutex
	calls []batchCall
}

type batchCall struct {
	size     int
	rejected bool
}

func (c *batchLimitedRPCClient) Call(_ any, method string, _ ...any) error {
	return fmt.Errorf("batchLimitedRPCClient: Call %s not supported", method)
}

func (c *batchLimitedRPCClient) CallContext(_ context.Context, _ any, method string, _ ...any) error {
	return fmt.Errorf("batchLimitedRPCClient: CallContext %s not supported", method)
}

func (c *batchLimitedRPCClient) BatchCallContext(ctx context.Context, b []rpc.BatchElem) error {
	rejected := len(b) > c.maxBatch
	c.mu.Lock()
	c.calls = append(c.calls, batchCall{size: len(b), rejected: rejected})
	c.mu.Unlock()
	if rejected {
		return c.rejectFn()
	}
	for i := range b {
		if b[i].Method != "eth_getBlockByNumber" {
			b[i].Error = fmt.Errorf("method %s not supported", b[i].Method)
			continue
		}
		hexNum, ok := b[i].Args[0].(string)
		if !ok {
			b[i].Error = fmt.Errorf("unexpected block argument %v", b[i].Args[0])
			continue
		}
		num, err := strconv.ParseUint(strings.TrimPrefix(hexNum, "0x"), 16, 64)
		if err != nil {
			b[i].Error = err
			continue
		}
		hdr, err := c.env.SimulatedL1.Client().HeaderByNumber(ctx, new(big.Int).SetUint64(num))
		if err != nil {
			b[i].Error = err
			continue
		}
		raw, err := json.Marshal(hdr)
		if err != nil {
			b[i].Error = err
			continue
		}
		b[i].Error = json.Unmarshal(raw, b[i].Result)
	}
	return nil
}

func (c *batchLimitedRPCClient) snapshot() []batchCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]batchCall(nil), c.calls...)
}

func TestE2E_BatchLimitedProvider(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}
	const (
		providerMaxBatch = 40
		numEvents        = 130
		// the LogEmitter constructor emits one Ping too
		totalEvents = numEvents + 1
	)
	tests := []struct {
		name string
		// rejectFn builds the error returned by the provider for an oversized batch
		rejectFn func() error
		// expectedRejectedSizes is the number of distinct rejected sizes expected (exact)
		expectedRejectedSizes int
		// maxSizeAfterFirstRejection is the largest batch the limiter may still send after the first rejection:
		// the provider limit when it is parsed from the message, else half of the rejected size
		maxSizeAfterFirstRejection int
	}{
		{
			name: "limit in the error message",
			rejectFn: func() error {
				return rpc.HTTPError{
					StatusCode: 400,
					Status:     "400 Bad Request",
					Body:       []byte("too many batch requests, max is 40"),
				}
			},
			expectedRejectedSizes:      1,
			maxSizeAfterFirstRejection: providerMaxBatch,
		},
		{
			name: "limit not in the error message",
			rejectFn: func() error {
				return rpc.HTTPError{
					StatusCode: 400,
					Status:     "400 Bad Request",
					Body:       []byte("batch too large"),
				}
			},
			expectedRejectedSizes:      2,
			maxSizeAfterFirstRejection: 65, // the first rejected batch has 129 headers: halved, rounded up
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testData := buildL1Simulated(t)
			limited := &batchLimitedRPCClient{env: testData, maxBatch: providerMaxBatch, rejectFn: tc.rejectFn}
			rpcCfg := ethermanconfig.NewDefaultRPCClientConfig()
			rpcCfg.HashFromJSON = false
			rpcCfg.BatchBlockHeaderRetrieval = true
			testData.ethClient = etherman.NewDefaultEthClient(testData.SimulatedL1.Client(), limited, rpcCfg)

			for i := 1; i <= numEvents; i++ {
				_, err := testData.LogEmitterContract.EmitPing(testData.auth, big.NewInt(int64(i)),
					fmt.Sprintf("iteration %d", i))
				require.NoError(t, err)
				testData.SimulatedL1.Commit()
			}
			tip, err := testData.ethClient.BlockNumber(t.Context())
			require.NoError(t, err)
			require.Greater(t, tip, uint64(2*providerMaxBatch))

			mdr := newMultidownloaderWithFinality(t, testData, "LatestBlock/-3")
			fastBackoff, err := aggkitcommon.NewExponentialBackoff(aggkitcommon.ExponentialBackoffConfig{
				Initial: time.Millisecond, Max: 5 * time.Millisecond, Factor: 2, Jitter: 0,
			})
			require.NoError(t, err)
			mdr.startStepBackoff = fastBackoff
			require.NoError(t, mdr.RegisterSyncer(aggkittypes.SyncerConfig{
				SyncerID:          "batch_limited_e2e",
				ContractAddresses: []common.Address{testData.LogEmitterAddr},
				FromBlock:         0,
				ToBlock:           aggkittypes.LatestBlock,
			}))

			ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- mdr.Start(ctx) }()

			countLogs := func() int {
				logs, err := mdr.storage.GetEthLogs(nil, mdrtypes.NewLogQuery(0, tip,
					[]common.Address{testData.LogEmitterAddr}))
				if err != nil {
					return -1
				}
				return len(logs)
			}
			require.Eventually(t, func() bool { return countLogs() == totalEvents },
				80*time.Second, 20*time.Millisecond, "sync did not converge to the tip")
			highest, err := mdr.storage.GetHighestBlockNumber(nil)
			require.NoError(t, err)
			require.Equal(t, tip, highest)

			// the multidownloader keeps syncing: a new block is picked up after the limiter has shrunk
			_, err = testData.LogEmitterContract.EmitPing(testData.auth, big.NewInt(numEvents+1), "extra")
			require.NoError(t, err)
			testData.SimulatedL1.Commit()
			require.Eventually(t, func() bool {
				logs, err := mdr.storage.GetEthLogs(nil, mdrtypes.NewLogQuery(0, tip+1,
					[]common.Address{testData.LogEmitterAddr}))
				return err == nil && len(logs) == totalEvents+1
			}, 30*time.Second, 20*time.Millisecond, "sync stopped after the limit was discovered")

			cancel()
			select {
			case startErr := <-done:
				require.True(t, startErr == nil || errors.Is(startErr, context.Canceled), "Start: %v", startErr)
			case <-time.After(10 * time.Second):
				require.FailNow(t, "Start did not return after cancel")
			}

			calls := limited.snapshot()
			rejectedSizes := map[int]struct{}{}
			firstRejection := -1
			for i, call := range calls {
				if call.rejected {
					rejectedSizes[call.size] = struct{}{}
					if firstRejection < 0 {
						firstRejection = i
					}
				}
			}
			require.GreaterOrEqual(t, firstRejection, 0, "the provider limit was never hit; calls=%v", calls)
			require.Len(t, rejectedSizes, tc.expectedRejectedSizes, "rejected sizes=%v calls=%v", rejectedSizes, calls)
			require.LessOrEqual(t, len(rejectedSizes), 6, "too many distinct rejected sizes: %v", rejectedSizes)
			for i, call := range calls[firstRejection+1:] {
				require.LessOrEqual(t, call.size, tc.maxSizeAfterFirstRejection,
					"batch of %d sent after the first rejection (call %d); calls=%v", call.size, i, calls)
				if !call.rejected {
					require.LessOrEqual(t, call.size, providerMaxBatch)
				}
			}
		})
	}
}
