package multidownloader

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/multidownloader/storage"
	"github.com/agglayer/aggkit/test/contracts/logemitter"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum"
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

func TestE2E(t *testing.T) {
	// Simulated L1
	testData := buildL1Simulated(t)

	logger := log.WithFields("module", "mdr_e2e")
	cfg := NewConfigDefault("e2e_test", t.TempDir())
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
	cfg.WaitPeriodToCheckCatchUp = configtypes.Duration{Duration: 1 * time.Millisecond}
	cfg.PeriodToCheckReorgs = configtypes.Duration{Duration: 1 * time.Millisecond}
	require.NoError(t, err)

	mdr, err := NewEVMMultidownloader(
		logger,
		cfg,
		"mdr_e2e",
		testData.ethClient,
		nil, // rpcClient
		store,
		nil,
		nil,
	)
	require.NoError(t, err)
	require.NotNil(t, mdr)
	// Generate some logs
	_, err = testData.LogEmitterContract.EmitPing(testData.auth, big.NewInt(123), "hello world")
	require.NoError(t, err)
	testData.SimulatedL1.Commit()

	err = mdr.RegisterSyncer(aggkittypes.SyncerConfig{
		SyncerID: "log_emitter_e2e_test",
		ContractAddresses: []common.Address{
			testData.LogEmitterAddr,
		},
		FromBlock: 0,
		ToBlock:   aggkittypes.LatestBlock,
	})
	require.NoError(t, err)
	ctx := t.Context()
	err = mdr.Initialize(ctx)
	require.NoError(t, err)

	go func() {
		err := mdr.Start(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			require.NoError(t, err)
		}
	}()
	latestBlock, err := mdr.BlockNumber(ctx, aggkittypes.LatestBlock)
	require.NoError(t, err)
	logs, err := mdr.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{testData.LogEmitterAddr},
		FromBlock: big.NewInt(0),
		ToBlock:   big.NewInt(int64(latestBlock)),
	})
	require.NoError(t, err)
	emitterLogs := processEvents(t, testData.LogEmitterContract, logs)
	require.Equal(t, 2, len(logs))
	require.Equal(t, testData.LogEmitterAddr, logs[0].Address)
	require.Equal(t, logEmitterEvent{
		From:    testData.auth.From,
		Id:      big.NewInt(123),
		Message: "hello world",
	}, emitterLogs[1])
	timeStart := time.Now()
	testData.SimulatedL1.Commit() // Block 3
	_, err = testData.LogEmitterContract.EmitPing(testData.auth, big.NewInt(123), "block 4")
	require.NoError(t, err)
	testData.SimulatedL1.Commit() // Block 4
	_, err = mdr.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{testData.LogEmitterAddr},
		FromBlock: big.NewInt(0),
		ToBlock:   big.NewInt(int64(latestBlock + 2)),
	})
	require.NoError(t, err)
	require.Equal(t, 3, len(logs))
	elapsed := time.Since(timeStart)
	logger.Infof("E2E test completed in %s", elapsed.String())
	showChainStatus(t, ctx, logger, testData.SimulatedL1)
	blk4, err := mdr.HeaderByNumber(ctx, aggkittypes.NewBlockNumber(4))
	require.NoError(t, err)

	// Forking at block 3 -> so block 4 will be reorged
	forkAt(t, ctx, logger, testData.SimulatedL1, 3)

	// Now se have to create a longer chain to force reorg
	testData.SimulatedL1.Commit() // reorg chain: Block 4
	testData.SimulatedL1.Commit() // reorg chain: Block 5
	showChainStatus(t, ctx, logger, testData.SimulatedL1)
	_, err = mdr.FilterLogs(ctx, ethereum.FilterQuery{
		Addresses: []common.Address{testData.LogEmitterAddr},
		FromBlock: big.NewInt(0),
		ToBlock:   big.NewInt(int64(5)),
	})
	require.NoError(t, err)
	blkReorged4, err := mdr.HeaderByNumber(ctx, aggkittypes.NewBlockNumber(4))
	require.NoError(t, err)
	logger.Infof("Block 4 hash after reorg: %s", blkReorged4.Hash.Hex())
	require.NotEqual(t, blk4.Hash, blkReorged4.Hash, "block 4 hash should be different after reorg")
	time.Sleep(1 * time.Second)
	err = mdr.Stop(ctx)
	require.NoError(t, err)
	isValid, reorgChainID, err := mdr.CheckValidBlock(ctx, blk4.Number, blk4.Hash)
	require.NoError(t, err)
	require.False(t, isValid, "block 4 should not be valid after reorg")
	require.Equal(t, uint64(1), reorgChainID, "reorgChainID should be 1")
}

func forkAt(t *testing.T, ctx context.Context, logger *log.Logger, sim *simulated.Backend, blockNumber uint64) {
	t.Helper()
	blk, err := sim.Client().HeaderByNumber(ctx, big.NewInt(int64(blockNumber)))
	require.NoError(t, err)
	require.NoError(t, err)
	logger.Infof("Forking L1 at block %d (%s)... This will generate new block for reorg >%d", blockNumber, blk.Hash().Hex(), blockNumber)

	err = sim.Fork(blk.Hash())
	require.NoError(t, err)
}

func showChainStatus(t *testing.T, ctx context.Context, logger *log.Logger, sim *simulated.Backend) {
	t.Helper()
	latestBlock, err := sim.Client().BlockNumber(ctx)

	require.NoError(t, err)
	logger.Infof("Current chain latest block: %d", latestBlock)
	for i := uint64(0); i <= latestBlock; i++ {
		blk, err := sim.Client().HeaderByNumber(ctx, big.NewInt(int64(i)))
		require.NoError(t, err)
		logger.Infof(" Block %d: %s", i, blk.Hash().Hex())
	}
}

type logEmitterEvent struct {
	From    common.Address
	Id      *big.Int
	Message string
}

func processEvents(t *testing.T, contract *logemitter.Logemitter, logs []types.Log) []logEmitterEvent {
	t.Helper()
	result := make([]logEmitterEvent, 0)
	for _, lg := range logs {
		if lg.Topics[0] == pingSignature {
			event, err := contract.ParsePing(lg)
			require.NoError(t, err)
			log.Infof("Processed Ping event: From=%s, Id=%s, Message=%s",
				event.From, event.Id, event.Message)
			result = append(result, logEmitterEvent{
				From:    event.From,
				Id:      event.Id,
				Message: event.Message,
			})
		} else {
			t.Fatalf("Unknown event signature: %s", lg.Topics[0].Hex())
		}
	}
	return result
}
