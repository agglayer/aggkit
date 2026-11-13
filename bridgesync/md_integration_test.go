package bridgesync

import (
	"context"
	"math"
	"math/big"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	bridgesyncmocks "github.com/agglayer/aggkit/bridgesync/mocks"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	mdrtypes "github.com/agglayer/aggkit/multidownloader/types"
	aggsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeMultidownloader is a minimal aggkittypes.MultiDownloaderLegacy used to assert the registration
// performed by the constructors without spinning up a real multidownloader.
type fakeMultidownloader struct {
	mu          sync.Mutex
	registered  []aggkittypes.SyncerConfig
	registerErr error
}

var _ aggkittypes.MultiDownloaderLegacy = (*fakeMultidownloader)(nil)

func (f *fakeMultidownloader) ChainID(context.Context) (uint64, error) { return 0, nil }

func (f *fakeMultidownloader) BlockNumber(context.Context, aggkittypes.BlockNumberFinality) (uint64, error) {
	return 0, nil
}

func (f *fakeMultidownloader) FilterLogs(context.Context, ethereum.FilterQuery) ([]types.Log, error) {
	return nil, nil
}

func (f *fakeMultidownloader) HeaderByNumber(
	context.Context, *aggkittypes.BlockNumberFinality,
) (*aggkittypes.BlockHeader, error) {
	return &aggkittypes.BlockHeader{}, nil
}

func (f *fakeMultidownloader) EthClient() aggkittypes.BaseEthereumClienter { return nil }

func (f *fakeMultidownloader) RegisterSyncer(data aggkittypes.SyncerConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registered = append(f.registered, data)
	return f.registerErr
}

func (f *fakeMultidownloader) Start(context.Context) error { return nil }

func (f *fakeMultidownloader) registeredConfigs() []aggkittypes.SyncerConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]aggkittypes.SyncerConfig, len(f.registered))
	copy(out, f.registered)
	return out
}

// fakeBackendMD embeds fakeMultidownloader and overrides FilterLogs so the provider backfill path can
// be exercised with configured logs. It records every query it receives and implements the optional
// IsAvailable pre-check capability, reporting the range as covered only when available is true.
type fakeBackendMD struct {
	*fakeMultidownloader
	queryMu   sync.Mutex
	logs      []types.Log
	err       error
	available bool
	queries   []ethereum.FilterQuery
}

var _ aggkittypes.MultiDownloaderLegacy = (*fakeBackendMD)(nil)

func (f *fakeBackendMD) IsAvailable(mdrtypes.LogQuery) bool { return f.available }

func (f *fakeBackendMD) FilterLogs(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
	f.queryMu.Lock()
	defer f.queryMu.Unlock()
	f.queries = append(f.queries, q)
	return f.logs, f.err
}

func (f *fakeBackendMD) recordedQueries() []ethereum.FilterQuery {
	f.queryMu.Lock()
	defer f.queryMu.Unlock()
	out := make([]ethereum.FilterQuery, len(f.queries))
	copy(out, f.queries)
	return out
}

// fakeTransactionProvider records the logs it is asked to resolve.
type fakeTransactionProvider struct {
	mu    sync.Mutex
	calls []types.Log
	tx    *aggkittypes.RPCTransaction
	err   error
}

var _ aggkittypes.TransactionProvider = (*fakeTransactionProvider)(nil)

func (f *fakeTransactionProvider) TransactionByLog(
	_ context.Context, l types.Log,
) (*aggkittypes.RPCTransaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, l)
	return f.tx, f.err
}

func (f *fakeTransactionProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeEnricherSetter mimics the optional SetLogsEnricher hook of the sync downloader.
type fakeEnricherSetter struct {
	installed aggsync.LogsEnricherFunc
}

func (f *fakeEnricherSetter) SetLogsEnricher(enricher aggsync.LogsEnricherFunc) aggsync.LogsEnricherFunc {
	prev := f.installed
	f.installed = enricher
	return prev
}

func mdTestConfig(t *testing.T, bridge common.Address, downloader cfgtypes.Downloader) Config {
	t.Helper()
	resolved := testSyncFromInBridges
	cfg := Config{
		DBPath:                             path.Join(t.TempDir(), "bridge_md.sqlite"),
		BridgeAddr:                         bridge,
		BlockFinality:                      aggkittypes.SafeBlock,
		SyncBlockChunkSize:                 100,
		InitialBlockNum:                    1000,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Second),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(time.Second),
		MaxRetryAttemptsAfterError:         3,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(30 * time.Second),
		Downloader:                         downloader,
	}
	cfg.SyncFromInBridges.Resolved = &resolved
	return cfg
}

func expectBridgeDeploymentAndSanity(t *testing.T, ethClient *mocksethclient.EthClienter, bridge common.Address) {
	t.Helper()
	// BridgeManager succeeds => sovereign deployment.
	ethClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{To: &bridge, Data: common.Hex2Bytes("14cc01a0")}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).Once()
	// sanity check: lastUpdatedDepositCount.
	ethClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{To: &bridge, Data: common.Hex2Bytes("be5831c7")}, mock.Anything).
		Return(common.LeftPadBytes(common.Hex2Bytes("2a"), common.HashLength), nil).Once()
}

func expectReorgDetector(t *testing.T) *bridgesyncmocks.ReorgDetector {
	t.Helper()
	rd := bridgesyncmocks.NewReorgDetector(t)
	rd.EXPECT().Subscribe(mock.Anything).Return(nil, nil)
	rd.EXPECT().GetFinalizedBlockType().Return(aggkittypes.SafeBlock)
	rd.EXPECT().String().Return("mockReorgDetector")
	return rd
}

func TestNewL1WithDownloader_RegistersSyncer(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)

	ethClient := mocksethclient.NewEthClienter(t)
	expectBridgeDeploymentAndSanity(t, ethClient, bridge)
	rd := expectReorgDetector(t)

	backend := &fakeMultidownloader{}
	provider := &fakeTransactionProvider{}

	bs, err := NewL1WithDownloader(ctx, cfg, rd, ethClient, 1, backend, provider)
	require.NoError(t, err)
	require.NotNil(t, bs)

	registered := backend.registeredConfigs()
	require.Len(t, registered, 1)
	got := registered[0]
	require.Equal(t, L1BridgeSyncer.String(), got.SyncerID)
	require.Equal(t, []common.Address{bridge}, got.ContractAddresses)
	require.Equal(t, cfg.InitialBlockNum, got.FromBlock, "historical FromBlock must not be repurposed")
	require.Equal(t, cfg.BlockFinality, got.ToBlock)
	require.True(t, got.TransactionRequirements.Enabled)
	require.Equal(t, cfg.InitialBlockNum, got.TransactionRequirements.FromBlock,
		"fresh DB: tx frontier starts at InitialBlockNum")
	require.Equal(t, []common.Hash{bridgeEventSignature}, got.TransactionRequirements.Topic0)

	// The optional tx warm-up hook must be installed on the downloader.
	require.NotNil(t, bs.downloader.SetLogsEnricher(nil), "enricher should have been installed")
}

func TestNewL1WithDownloader_RegistersFutureTxFrontier(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)

	// Seed the syncer storage with an already processed block so the tx requirement is future-only.
	database, err := newSqliteDB(cfg.DBPath)
	require.NoError(t, err)
	_, err = database.Exec("INSERT INTO block (num) VALUES (?)", uint64(1500))
	require.NoError(t, err)
	require.NoError(t, database.Close())

	ethClient := mocksethclient.NewEthClienter(t)
	expectBridgeDeploymentAndSanity(t, ethClient, bridge)
	rd := expectReorgDetector(t)

	backend := &fakeMultidownloader{}
	bs, err := NewL1WithDownloader(ctx, cfg, rd, ethClient, 1, backend, &fakeTransactionProvider{})
	require.NoError(t, err)
	require.NotNil(t, bs)

	registered := backend.registeredConfigs()
	require.Len(t, registered, 1)
	require.Equal(t, cfg.InitialBlockNum, registered[0].FromBlock)
	require.Equal(t, uint64(1501), registered[0].TransactionRequirements.FromBlock)
}

// fakeLastProcessedBlockReader stubs the processor checkpoint so the transaction frontier logic can be
// exercised with checkpoint values a real storage driver would reject (for example MaxUint64, which
// SQLite refuses because the high bit is set) or with read failures.
type fakeLastProcessedBlockReader struct {
	lastProcessed uint64
	found         bool
	err           error
}

func (f fakeLastProcessedBlockReader) GetLastProcessedBlock(context.Context) (uint64, bool, error) {
	return f.lastProcessed, f.found, f.err
}

func TestComputeTransactionFromBlock(t *testing.T) {
	ctx := t.Context()
	logger := log.WithFields("module", "test")
	dbPath := path.Join(t.TempDir(), "tx_frontier.sqlite")
	database, err := newSqliteDB(dbPath)
	require.NoError(t, err)
	defer database.Close()
	proc, err := newProcessor(database, "tx_frontier", logger, 30*time.Second)
	require.NoError(t, err)

	t.Run("no checkpoint uses initial block", func(t *testing.T) {
		from, err := computeTransactionFromBlock(ctx, proc, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(100), from)
	})

	t.Run("checkpoint L yields L+1", func(t *testing.T) {
		_, err := database.Exec("INSERT INTO block (num) VALUES (?)", uint64(50))
		require.NoError(t, err)
		from, err := computeTransactionFromBlock(ctx, proc, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(51), from)
	})

	t.Run("overflow is reported", func(t *testing.T) {
		_, err := computeTransactionFromBlock(ctx,
			fakeLastProcessedBlockReader{lastProcessed: math.MaxUint64, found: true}, 100)
		require.ErrorContains(t, err, "overflows")
	})

	t.Run("read error is wrapped", func(t *testing.T) {
		_, err := computeTransactionFromBlock(ctx,
			fakeLastProcessedBlockReader{err: context.Canceled}, 100)
		require.ErrorIs(t, err, context.Canceled)
		require.ErrorContains(t, err, "failed to read last processed block")
	})
}

func TestConstructors_RejectMultidownloaderWithoutDeps(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234")

	t.Run("NewL1 with multidownloader config fails", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)
		_, err := NewL1(ctx, cfg, nil, nil, 1)
		require.ErrorIs(t, err, ErrMultidownloaderDepsMissing)
		require.ErrorContains(t, err, "NewL1WithDownloader")
	})

	t.Run("NewL2 with multidownloader config fails", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)
		_, err := NewL2(ctx, cfg, nil, nil, 1, false, bridgesynctypes.EmptyLER)
		require.ErrorContains(t, err, "not supported for the L2")
	})

	t.Run("NewL1WithDownloader requires multidownloader config", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, cfgtypes.LegacyDownloader)
		_, err := NewL1WithDownloader(ctx, cfg, nil, nil, 1, &fakeMultidownloader{}, &fakeTransactionProvider{})
		require.ErrorContains(t, err, "requires Downloader=multidownloader")
	})

	t.Run("NewL1WithDownloader requires backend", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)
		_, err := NewL1WithDownloader(ctx, cfg, nil, nil, 1, nil, &fakeTransactionProvider{})
		require.ErrorIs(t, err, ErrMultidownloaderDepsMissing)
		require.ErrorContains(t, err, "backend is nil")
	})

	t.Run("NewL1WithDownloader requires provider", func(t *testing.T) {
		cfg := mdTestConfig(t, bridge, cfgtypes.Multidownloader)
		_, err := NewL1WithDownloader(ctx, cfg, nil, nil, 1, &fakeMultidownloader{}, nil)
		require.ErrorIs(t, err, ErrMultidownloaderDepsMissing)
		require.ErrorContains(t, err, "transaction provider is nil")
	})
}

// mustBridgeEventLog builds a real, parseable BridgeEvent log.
func mustBridgeEventLog(
	t *testing.T, leafType uint8, originAddr, destAddr common.Address, txHash common.Hash,
) types.Log {
	t.Helper()
	abi, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)
	event, err := abi.EventByID(bridgeEventSignature)
	require.NoError(t, err)
	data, err := event.Inputs.Pack(
		leafType, uint32(1), originAddr, uint32(2), destAddr, big.NewInt(100), []byte{}, uint32(1))
	require.NoError(t, err)
	return types.Log{
		Address:     common.HexToAddress("0x10"),
		Topics:      []common.Hash{bridgeEventSignature},
		Data:        data,
		TxHash:      txHash,
		Index:       1,
		BlockNumber: 1,
	}
}

func newBridgeAppenderForProvider(
	t *testing.T, ethClient *mocksethclient.EthClienter, bridge common.Address,
	provider *fakeTransactionProvider, syncFromInBridges bool,
) aggsync.LogAppenderMap {
	t.Helper()
	agglayerBridge, err := agglayerbridge.NewAgglayerbridge(bridge, ethClient)
	require.NoError(t, err)
	agglayerBridgeL2, err := agglayerbridgel2.NewAgglayerbridgel2(bridge, ethClient)
	require.NoError(t, err)
	deployment := &bridgeDeployment{
		kind:             NonSovereignChain,
		agglayerBridge:   agglayerBridge,
		agglayerBridgeL2: agglayerBridgeL2,
	}
	appender, err := buildAppender(t.Context(), ethClient, bridge, syncFromInBridges, deployment,
		log.WithFields("module", "test"), &providerTxnResolver{provider: provider})
	require.NoError(t, err)
	return appender
}

// TestBridgeAppender_ProviderPath_MultipleLogsSameTx verifies that the multidownloader appender path
// resolves every bridge log through the provider (it never calls eth_getTransactionByHash), including
// several logs that share the same transaction.
func TestBridgeAppender_ProviderPath_MultipleLogsSameTx(t *testing.T) {
	bridge := common.HexToAddress("0x10")
	// No expectations on the eth client: any RPC call would fail the test.
	ethClient := mocksethclient.NewEthClienter(t)

	tx := &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
	}
	to := "0x2222222222222222222222222222222222222222"
	tx.To = &to
	provider := &fakeTransactionProvider{tx: tx}

	appender := newBridgeAppenderForProvider(t, ethClient, bridge, provider, false)

	txHash := common.HexToHash("0xabc")
	block := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: 1}}
	for i := 0; i < 2; i++ {
		l := mustBridgeEventLog(t, bridgeLeafTypeMessage, common.HexToAddress("0x40"),
			common.HexToAddress("0x30"), txHash)
		require.NoError(t, appender[bridgeEventSignature](block, l))
	}
	require.Len(t, block.Events, 2)
	require.Equal(t, 2, provider.callCount())
}

// TestBridgeAppender_ProviderPath_DirectToBridge_NoTrace verifies that an asset event whose
// transaction is sent directly to the bridge keeps the legacy "no debug trace" policy even on the
// provider path.
func TestBridgeAppender_ProviderPath_DirectToBridge_NoTrace(t *testing.T) {
	bridge := common.HexToAddress("0x10")
	ethClient := mocksethclient.NewEthClienter(t)

	to := bridge.Hex()
	tx := &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
		To:      &to,
	}
	provider := &fakeTransactionProvider{tx: tx}
	appender := newBridgeAppenderForProvider(t, ethClient, bridge, provider, true)

	block := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: 1}}
	l := mustBridgeEventLog(t, bridgeLeafTypeAsset, common.HexToAddress("0x50"),
		common.HexToAddress("0x30"), common.HexToHash("0xdef"))
	require.NoError(t, appender[bridgeEventSignature](block, l))
	require.Len(t, block.Events, 1)
	require.Equal(t, 1, provider.callCount())
}

// TestBridgeAppender_ProviderPath_IndirectSkipFrom verifies the syncFromInBridges=false policy on the
// provider path: an asset event routed through a contract is stored without a FromAddress and without
// a debug trace.
func TestBridgeAppender_ProviderPath_IndirectSkipFrom(t *testing.T) {
	bridge := common.HexToAddress("0x10")
	ethClient := mocksethclient.NewEthClienter(t)

	to := "0x9999999999999999999999999999999999999999"
	tx := &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
		To:      &to,
	}
	provider := &fakeTransactionProvider{tx: tx}
	appender := newBridgeAppenderForProvider(t, ethClient, bridge, provider, false)

	block := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: 1}}
	l := mustBridgeEventLog(t, bridgeLeafTypeAsset, common.HexToAddress("0x50"),
		common.HexToAddress("0x30"), common.HexToHash("0xdef"))
	require.NoError(t, appender[bridgeEventSignature](block, l))
	require.Len(t, block.Events, 1)
	require.Equal(t, 1, provider.callCount())
}

// TestBridgeAppender_ProviderPath_IndirectTrace verifies the traces client is unchanged: an indirect
// asset event with syncFromInBridges=true still resolves the FromAddress through
// debug_traceTransaction, while the transaction itself comes from the provider.
func TestBridgeAppender_ProviderPath_IndirectTrace(t *testing.T) {
	bridge := common.HexToAddress("0x10")
	ethClient := mocksethclient.NewEthClienter(t)
	ethClient.EXPECT().Call(mock.Anything, DebugTraceTxEndpoint, mock.Anything, mock.Anything).
		Run(func(result any, _ string, _ ...any) {
			arg, ok := result.(*Call)
			require.True(t, ok)
			*arg = Call{
				Type: CallTypeCall,
				From: common.HexToAddress("0x3333333333333333333333333333333333333333"),
				To:   common.HexToAddress("0x4444444444444444444444444444444444444444"),
				Calls: []Call{{
					Type:  CallTypeCall,
					To:    bridge,
					From:  common.HexToAddress("0x50"),
					Input: append(BridgeAssetMethodID, make([]byte, 100)...),
				}},
			}
		}).
		Return(nil).
		Once()

	to := "0x9999999999999999999999999999999999999999"
	provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
		To:      &to,
	}}
	appender := newBridgeAppenderForProvider(t, ethClient, bridge, provider, true)

	block := &aggsync.EVMBlock{EVMBlockHeader: aggsync.EVMBlockHeader{Num: 1}}
	l := mustBridgeEventLog(t, bridgeLeafTypeAsset, common.HexToAddress("0x50"),
		common.HexToAddress("0x30"), common.HexToHash("0xdef"))
	require.NoError(t, appender[bridgeEventSignature](block, l))
	require.Len(t, block.Events, 1)
}

func TestBridgeLogsEnricher(t *testing.T) {
	txHashA := common.HexToHash("0xaaa")
	txHashB := common.HexToHash("0xbbb")
	logs := []types.Log{
		{Topics: []common.Hash{common.HexToHash("0xdead")}},
		{Topics: []common.Hash{bridgeEventSignature}, TxHash: txHashA, BlockNumber: 1},
		{Topics: []common.Hash{bridgeEventSignature}, TxHash: txHashB, BlockNumber: 2},
	}

	t.Run("resolves only bridge event logs", func(t *testing.T) {
		provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{}}
		require.NoError(t, bridgeLogsEnricher(provider)(t.Context(), logs))
		require.Equal(t, 2, provider.callCount())
	})

	t.Run("propagates the inclusion mismatch sentinel", func(t *testing.T) {
		provider := &fakeTransactionProvider{err: aggkittypes.ErrTransactionInclusionMismatch}
		err := bridgeLogsEnricher(provider)(t.Context(), logs)
		require.ErrorIs(t, err, aggkittypes.ErrTransactionInclusionMismatch)
	})
}

func TestRegisterLogsEnricher(t *testing.T) {
	enricher := bridgeLogsEnricher(&fakeTransactionProvider{})

	setter := &fakeEnricherSetter{}
	require.True(t, registerLogsEnricher(setter, enricher))
	require.NotNil(t, setter.installed)

	require.False(t, registerLogsEnricher(struct{}{}, enricher))
}

func TestBackfillTxnSender_WithTransactionProvider(t *testing.T) {
	bridge := common.HexToAddress("0x1234")
	ethClient := mocksethclient.NewEthClienter(t)

	record := RecordToBackfill{
		BlockNum: 7,
		BlockPos: 1,
		TxHash:   common.HexToHash("0xabc"),
		LeafType: bridgeLeafTypeMessage,
	}

	blockHash := common.HexToHash("0xbb")
	to := "0x2222222222222222222222222222222222222222"
	provider := &fakeTransactionProvider{tx: &aggkittypes.RPCTransaction{
		FromRaw: "0x1111111111111111111111111111111111111111",
		To:      &to,
	}}

	foundLog := types.Log{
		Address:     bridge,
		Topics:      []common.Hash{bridgeEventSignature},
		TxHash:      record.TxHash,
		BlockNumber: record.BlockNum,
		BlockHash:   blockHash,
		Index:       uint(record.BlockPos),
	}
	// The exact original log comes from the download backend, never a direct RPC FilterLogs: the eth
	// client has no expectations, so any RPC call would fail the test.
	backend := &fakeBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		available:           true,
		logs:                []types.Log{foundLog},
	}

	backfiller, err := NewBackfillTxnSender(path.Join(t.TempDir(), "backfill.sqlite"), ethClient, bridge, true,
		log.WithFields("module", "test"), WithTransactionProvider(provider), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()
	require.NotNil(t, backfiller.provider)
	require.NotNil(t, backfiller.backend)

	logEvent := &agglayerbridge.AgglayerbridgeBridgeEvent{
		LeafType:      bridgeLeafTypeMessage,
		OriginAddress: common.HexToAddress("0x50"),
	}
	txnSender, fromAddr, toAddr, err := backfiller.extractDataViaProvider(t.Context(), record, logEvent)
	require.NoError(t, err)
	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"), txnSender)
	require.Equal(t, common.HexToAddress("0x2222222222222222222222222222222222222222"), toAddr)
	require.NotNil(t, fromAddr)
	require.Equal(t, 1, provider.callCount(), "provider must be used, not eth_getTransactionByHash")
	require.Len(t, backend.recordedQueries(), 1, "the log must be read from the backend, not the node")
}

func TestBackfillTxnSender_ProviderCoverageMissing(t *testing.T) {
	bridge := common.HexToAddress("0x1234")
	// No expectations on the eth client: the provider path must never fall back to a direct RPC.
	ethClient := mocksethclient.NewEthClienter(t)

	record := RecordToBackfill{BlockNum: 7, TxHash: common.HexToHash("0xabc")}
	// The backend returns the exact original log, but the provider has no transaction for it yet.
	foundLog := types.Log{
		Address:     bridge,
		Topics:      []common.Hash{bridgeEventSignature},
		TxHash:      record.TxHash,
		BlockNumber: record.BlockNum,
		Index:       0,
	}
	backend := &fakeBackendMD{
		fakeMultidownloader: &fakeMultidownloader{},
		available:           true,
		logs:                []types.Log{foundLog},
	}

	backfiller, err := NewBackfillTxnSender(path.Join(t.TempDir(), "backfill.sqlite"), ethClient, bridge, true,
		log.WithFields("module", "test"),
		WithTransactionProvider(&fakeTransactionProvider{}), WithDownloadBackend(backend))
	require.NoError(t, err)
	defer backfiller.Close()

	txnSender, fromAddr, toAddr, err := backfiller.extractDataViaProvider(t.Context(), record,
		&agglayerbridge.AgglayerbridgeBridgeEvent{LeafType: bridgeLeafTypeMessage})
	require.ErrorIs(t, err, aggkittypes.ErrTransactionUnavailable)
	require.ErrorContains(t, err, "not covered")
	require.Equal(t, common.Address{}, txnSender)
	require.Nil(t, fromAddr)
	require.Equal(t, common.Address{}, toAddr)
	require.Len(t, backend.recordedQueries(), 1, "the log must be read from the backend, not the node")
}
