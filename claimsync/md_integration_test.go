package claimsync

import (
	"context"
	"errors"
	"path"
	"sync"
	"testing"
	"time"

	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	configtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/reorgdetector"
	aggsync "github.com/agglayer/aggkit/sync"
	aggkittypes "github.com/agglayer/aggkit/types"
	mocksethclient "github.com/agglayer/aggkit/types/mocks"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// fakeMultidownloader is a minimal aggkittypes.MultiDownloaderLegacy used to assert the syncer
// registration performed by NewStandaloneClaimSyncWithDownloader without spinning up a real
// multidownloader. RegisterSyncer records the configs it receives; every other method is a stub.
type fakeMultidownloader struct {
	mu          sync.Mutex
	registered  []aggkittypes.SyncerConfig
	registerErr error
	started     bool
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

func (f *fakeMultidownloader) Start(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = true
	return nil
}

func (f *fakeMultidownloader) registeredConfigs() []aggkittypes.SyncerConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]aggkittypes.SyncerConfig, len(f.registered))
	copy(out, f.registered)
	return out
}

func (f *fakeMultidownloader) isStarted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

// mdStandaloneConfig builds a minimal standalone config that selects the multidownloader.
func mdStandaloneConfig(t *testing.T, bridge common.Address, downloader configtypes.Downloader) ConfigStandalone {
	t.Helper()
	return ConfigStandalone{
		ConfigEmbedded: ConfigEmbedded{
			DBQueryTimeout: configtypes.NewDuration(30 * time.Second),
			BridgeAddr:     bridge,
		},
		DBPath:                             path.Join(t.TempDir(), "claimsync_md.sqlite"),
		BlockFinality:                      aggkittypes.FinalizedBlock,
		InitialBlockNum:                    1000,
		SyncBlockChunkSize:                 100,
		RetryAfterErrorPeriod:              configtypes.NewDuration(time.Second),
		MaxRetryAttemptsAfterError:         -1,
		WaitForNewBlocksPeriod:             configtypes.NewDuration(time.Second),
		RequireStorageContentCompatibility: false,
		AutoStart:                          configtypes.FalseMode,
		Downloader:                         downloader,
	}
}

// expectSovereignBridgeDeployment makes the bridgeManager() probe succeed, so
// resolveBridgeDeployment classifies the contract as a sovereign bridge.
func expectSovereignBridgeDeployment(t *testing.T, ethClient *mocksethclient.EthClienter, bridge common.Address) {
	t.Helper()
	ethClient.EXPECT().CallContract(mock.Anything,
		ethereum.CallMsg{To: &bridge, Data: common.Hex2Bytes("14cc01a0")}, mock.Anything).
		Return(common.LeftPadBytes(bridge.Bytes(), common.HashLength), nil).Once()
}

// expectMDReorgDetector wires the reorg detector calls performed while building the standalone
// ClaimSync: the finalized block type and the subscription done by the EVM driver.
func expectMDReorgDetector(t *testing.T) *aggsync.ReorgDetectorMock {
	t.Helper()
	rd := aggsync.NewReorgDetectorMock(t)
	rd.EXPECT().GetFinalizedBlockType().Return(aggkittypes.FinalizedBlock)
	rd.EXPECT().Subscribe(claimsynctypes.L1ClaimSyncer.String()).
		Return(&reorgdetector.Subscription{
			ReorgedBlock:   make(chan uint64),
			ReorgProcessed: make(chan bool),
		}, nil)
	return rd
}

// TestMDConstructor_RegistersSyncer verifies that NewStandaloneClaimSyncWithDownloader registers the
// L1 claim syncer in the provided multidownloader synchronously (before the backend is started) with
// the expected contract address, historical frontier, target finality and no transaction requirement.
func TestMDConstructor_RegistersSyncer(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)

	ethClient := mocksethclient.NewEthClienter(t)
	expectSovereignBridgeDeployment(t, ethClient, bridge)
	rd := expectMDReorgDetector(t)

	backend := &fakeMultidownloader{}

	cs, err := NewStandaloneClaimSyncWithDownloader(
		ctx, cfg, rd, ethClient, claimsynctypes.L1ClaimSyncer, 0, backend)
	require.NoError(t, err)
	require.NotNil(t, cs)

	registered := backend.registeredConfigs()
	require.Len(t, registered, 1)
	got := registered[0]
	require.Equal(t, claimsynctypes.L1ClaimSyncer.String(), got.SyncerID)
	require.Equal(t, []common.Address{bridge}, got.ContractAddresses)
	require.Equal(t, cfg.InitialBlockNum, got.FromBlock, "historical FromBlock must not be repurposed")
	require.Equal(t, cfg.BlockFinality, got.ToBlock)
	require.False(t, got.TransactionRequirements.Enabled,
		"claims do not need transaction data, so the requirement must stay disabled")

	// Registration must happen during construction, before the backend is started.
	require.False(t, backend.isStarted(), "backend must not be started by the constructor")

	// Building the appender/registrar must not issue any raw RPC (e.g. debug_traceTransaction).
	ethClient.AssertNotCalled(t, "Call", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestMDConstructor_RegisterSyncerError verifies that a RegisterSyncer failure aborts construction
// before the bridge deployment is probed (so nil rd/ethClient are never dereferenced).
func TestMDConstructor_RegisterSyncerError(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)

	registerErr := errors.New("register failed")
	backend := &fakeMultidownloader{registerErr: registerErr}

	cs, err := NewStandaloneClaimSyncWithDownloader(
		ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0, backend)
	require.Nil(t, cs)
	require.ErrorIs(t, err, registerErr)
	require.ErrorContains(t, err, "failed to register")
	require.False(t, backend.isStarted())
}

// TestMultidownloaderConstructor_Validation covers the constructor guards that run before any
// dependency is touched, so nil rd/ethClient can be passed safely.
func TestMultidownloaderConstructor_Validation(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")

	t.Run("rejects legacy selector", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, configtypes.LegacyDownloader)
		cs, err := NewStandaloneClaimSyncWithDownloader(
			ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0, nil)
		require.Nil(t, cs)
		require.ErrorContains(t, err, "requires Downloader=multidownloader")
	})

	t.Run("rejects empty selector", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, "")
		cs, err := NewStandaloneClaimSyncWithDownloader(
			ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0, nil)
		require.Nil(t, cs)
		require.ErrorContains(t, err, "requires Downloader=multidownloader")
	})

	t.Run("rejects nil backend", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)
		cs, err := NewStandaloneClaimSyncWithDownloader(
			ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0, nil)
		require.Nil(t, cs)
		require.ErrorIs(t, err, ErrMultidownloaderDepsMissing)
		require.ErrorContains(t, err, "backend is nil")
	})

	t.Run("rejects L2 syncer before touching deps", func(t *testing.T) {
		cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)
		// Non-nil backend so the L2 check (not the nil-backend guard) is exercised.
		cs, err := NewStandaloneClaimSyncWithDownloader(
			ctx, cfg, nil, nil, claimsynctypes.L2ClaimSyncer, 0, &fakeMultidownloader{})
		require.Nil(t, cs)
		require.ErrorContains(t, err, "only supported for the L1 claim syncer")
	})
}

// TestLegacyConstructors_RejectMultidownloader verifies that the legacy constructors refuse a
// multidownloader config instead of silently falling back to the legacy downloader.
func TestLegacyConstructors_RejectMultidownloader(t *testing.T) {
	ctx := t.Context()
	bridge := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	cfg := mdStandaloneConfig(t, bridge, configtypes.Multidownloader)

	t.Run("NewStandaloneClaimSync", func(t *testing.T) {
		cs, err := NewStandaloneClaimSync(ctx, cfg, nil, nil, claimsynctypes.L1ClaimSyncer, 0)
		require.Nil(t, cs)
		require.ErrorIs(t, err, ErrMultidownloaderDepsMissing)
		require.ErrorContains(t, err, "NewStandaloneClaimSyncWithDownloader")
	})

	t.Run("NewClaimSync", func(t *testing.T) {
		cs, err := NewClaimSync(ctx, cfg, nil, nil, 0, claimsynctypes.L1ClaimSyncer, nil)
		require.Nil(t, cs)
		require.ErrorIs(t, err, ErrMultidownloaderDepsMissing)
		require.ErrorContains(t, err, "NewStandaloneClaimSyncWithDownloader")
	})
}
