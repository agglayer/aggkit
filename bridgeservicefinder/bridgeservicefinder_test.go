package bridgeservicefinder

import (
	"context"
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/agglayer/aggkit/bridgeservicefinder/mocks"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TestNew_ErrNilRollupManagerQuerier verifies New refuses to construct a finder when neither a
// RollupManager nor an EthClient (to build the default one) is supplied.
func TestNew_ErrNilRollupManagerQuerier(t *testing.T) {
	_, err := New(Config{}, Options{})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNilRollupManagerQuerier)
}

// TestNew_AcceptsInjectedRollupManagerWithoutEthClient verifies a fully injected RollupManager
// (with a LogFilterer also supplied so no listener defaulting is needed) allows New to succeed and
// Start to run without any EthClient. With RollupCount=0 the listener only watches the rollup
// manager address (to discover future rollups); the test's ctx is cancelled before the first poll
// tick, so the LogFilterer is never touched.
func TestNew_AcceptsInjectedRollupManagerWithoutEthClient(t *testing.T) {
	rm := mocks.NewRollupManagerQuerier(t)
	reader := mocks.NewRollupContractReader(t)
	lf := mocks.NewLogFilterer(t)
	hc := mocks.NewHealthChecker(t)

	rm.EXPECT().RollupCount(mock.Anything).Return(uint32(0), nil)

	f, err := New(Config{}, Options{
		RollupManager: rm,
		ReaderFactory: func(common.Address, aggkittypes.BaseEthereumClienter) (RollupContractReader, error) {
			return reader, nil
		},
		LogFilterer:   lf,
		HealthChecker: hc,
		Logger:        testLogger(),
	})
	require.NoError(t, err)
	require.NotNil(t, f)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, f.Start(ctx))
}

// TestStart_RequireAllHealthyOnStart_MockedBranching exercises Start's RequireAllHealthyOnStart
// branching purely against mocked RollupManagerQuerier/HealthChecker dependencies, with zero
// rollups enumerated (RollupCount=0) plus a config-only network, so no contracts are needed.
func TestStart_RequireAllHealthyOnStart_MockedBranching(t *testing.T) {
	t.Run("unhealthy config-sourced network with RequireAllHealthyOnStart=false: Start succeeds", func(t *testing.T) {
		rm := mocks.NewRollupManagerQuerier(t)
		hc := mocks.NewHealthChecker(t)

		rm.EXPECT().RollupCount(mock.Anything).Return(uint32(0), nil)
		hc.EXPECT().IsHealthy(mock.Anything, "https://dead.example.com").Return(false)

		f, err := New(Config{
			BridgeURLs:               map[uint32]string{1: "https://dead.example.com"},
			RequireAllHealthyOnStart: false,
		}, Options{
			RollupManager: rm,
			HealthChecker: hc,
			LogFilterer:   mocks.NewLogFilterer(t),
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		require.NoError(t, f.Start(ctx))

		got, err := f.GetURL(1)
		require.NoError(t, err)
		require.Equal(t, "https://dead.example.com", got.BridgeURL)
	})

	t.Run("unhealthy config-sourced network with RequireAllHealthyOnStart=true: Start fails", func(t *testing.T) {
		rm := mocks.NewRollupManagerQuerier(t)
		hc := mocks.NewHealthChecker(t)

		rm.EXPECT().RollupCount(mock.Anything).Return(uint32(0), nil)
		hc.EXPECT().IsHealthy(mock.Anything, "https://dead.example.com").Return(false)

		f, err := New(Config{
			BridgeURLs:               map[uint32]string{1: "https://dead.example.com"},
			RequireAllHealthyOnStart: true,
		}, Options{
			RollupManager: rm,
			HealthChecker: hc,
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		err = f.Start(context.Background())
		require.Error(t, err)
		require.ErrorIs(t, err, ErrServicesUnhealthyOnStart)
	})

	t.Run("all healthy with RequireAllHealthyOnStart=true: Start succeeds", func(t *testing.T) {
		rm := mocks.NewRollupManagerQuerier(t)
		hc := mocks.NewHealthChecker(t)

		rm.EXPECT().RollupCount(mock.Anything).Return(uint32(0), nil)
		hc.EXPECT().IsHealthy(mock.Anything, "https://alive.example.com").Return(true)

		f, err := New(Config{
			BridgeURLs:               map[uint32]string{1: "https://alive.example.com"},
			RequireAllHealthyOnStart: true,
		}, Options{
			RollupManager: rm,
			HealthChecker: hc,
			Logger:        testLogger(),
		})
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		t.Cleanup(cancel)
		require.NoError(t, f.Start(ctx))
	})
}

// TestBuildInitialCache_RollupCountErrorAbortsStart verifies a hard error from RollupCount aborts
// Start entirely (returned, not merely logged/skipped).
func TestBuildInitialCache_RollupCountErrorAbortsStart(t *testing.T) {
	rm := mocks.NewRollupManagerQuerier(t)
	wantErr := context.DeadlineExceeded

	rm.EXPECT().RollupCount(mock.Anything).Return(uint32(0), wantErr)

	f, err := New(Config{}, Options{
		RollupManager: rm,
		HealthChecker: mocks.NewHealthChecker(t),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	err = f.Start(context.Background())
	require.Error(t, err)
}

// TestBuildInitialCache_RollupDataErrorAbortsStart verifies a genuine RPC/transport error from
// RollupIDToRollupData is surfaced (aborting Start) rather than silently skipped: enumeration stops
// at the failing network and the wrapped error is returned. This is the "fail loudly" behaviour that
// distinguishes a broken L1 endpoint from a network that legitimately exposes no bridge service.
func TestBuildInitialCache_RollupDataErrorAbortsStart(t *testing.T) {
	rm := mocks.NewRollupManagerQuerier(t)

	rm.EXPECT().RollupCount(mock.Anything).Return(uint32(2), nil)
	rm.EXPECT().RollupIDToRollupData(mock.Anything, uint32(1)).
		Return(agglayermanager.AgglayerManagerRollupDataReturn{}, context.DeadlineExceeded)

	f, err := New(Config{}, Options{
		RollupManager: rm,
		HealthChecker: mocks.NewHealthChecker(t),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	err = f.Start(context.Background())
	require.Error(t, err)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	// Nothing was cached and the second network was never enumerated (Start aborted at network 1).
	_, err = f.GetURL(1)
	require.ErrorIs(t, err, ErrURLNotFound)
	_, err = f.GetURL(2)
	require.ErrorIs(t, err, ErrURLNotFound)
}

// TestBuildInitialCache_NoSourceSkipsNetworkButContinues verifies the benign counterpart: a network
// that legitimately exposes no bridge service URL source (ErrNoSourceAvailable) is skipped without an
// entry, while enumeration continues and later networks are still resolved. This is the graceful
// degradation that must remain, in contrast to the hard-error abort above.
func TestBuildInitialCache_NoSourceSkipsNetworkButContinues(t *testing.T) {
	rm := mocks.NewRollupManagerQuerier(t)
	hc := mocks.NewHealthChecker(t)

	noSourceAddr := common.HexToAddress("0xabc00000000000000000000000000000000001")
	resolvedAddr := common.HexToAddress("0xabc00000000000000000000000000000000002")

	rm.EXPECT().RollupCount(mock.Anything).Return(uint32(2), nil)
	rm.EXPECT().RollupIDToRollupData(mock.Anything, uint32(1)).
		Return(agglayermanager.AgglayerManagerRollupDataReturn{RollupContract: noSourceAddr}, nil)
	rm.EXPECT().RollupIDToRollupData(mock.Anything, uint32(2)).
		Return(agglayermanager.AgglayerManagerRollupDataReturn{RollupContract: resolvedAddr}, nil)

	noSourceReader := mocks.NewRollupContractReader(t)
	noSourceReader.EXPECT().AggchainMetadata(mock.Anything, MetadataBridgeServiceURLKey).
		Return("", ErrSourceNotAvailable)
	noSourceReader.EXPECT().TrustedSequencerURL(mock.Anything).Return("", ErrSourceNotAvailable)

	resolvedReader := mocks.NewRollupContractReader(t)
	resolvedReader.EXPECT().TrustedSequencerURL(mock.Anything).Return("", ErrSourceNotAvailable)
	resolvedReader.EXPECT().AggchainMetadata(mock.Anything, MetadataBridgeServiceURLKey).
		Return("https://metadata.example.com:5577", nil)

	hc.EXPECT().IsHealthy(mock.Anything, "https://metadata.example.com:5577").Return(true)

	f, err := New(Config{}, Options{
		RollupManager: rm,
		ReaderFactory: func(addr common.Address, _ aggkittypes.BaseEthereumClienter) (RollupContractReader, error) {
			if addr == noSourceAddr {
				return noSourceReader, nil
			}

			return resolvedReader, nil
		},
		HealthChecker: hc,
		LogFilterer:   mocks.NewLogFilterer(t),
		Logger:        testLogger(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	require.NoError(t, f.Start(ctx))

	_, err = f.GetURL(1)
	require.ErrorIs(t, err, ErrURLNotFound)

	got, err := f.GetURL(2)
	require.NoError(t, err)
	require.Equal(t, "https://metadata.example.com:5577", got.BridgeURL)
	require.Empty(t, got.JSONRPCURL, "no sequencer url available, so no json-rpc endpoint")
}
