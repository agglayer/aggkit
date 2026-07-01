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
// Start to run without any EthClient. With RollupCount=0 there are no watched addresses, so the
// listener goroutine returns immediately without ever touching the LogFilterer.
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
			URLs:                     map[uint32]string{1: "https://dead.example.com"},
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
		require.Equal(t, "https://dead.example.com", got)
	})

	t.Run("unhealthy config-sourced network with RequireAllHealthyOnStart=true: Start fails", func(t *testing.T) {
		rm := mocks.NewRollupManagerQuerier(t)
		hc := mocks.NewHealthChecker(t)

		rm.EXPECT().RollupCount(mock.Anything).Return(uint32(0), nil)
		hc.EXPECT().IsHealthy(mock.Anything, "https://dead.example.com").Return(false)

		f, err := New(Config{
			URLs:                     map[uint32]string{1: "https://dead.example.com"},
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
			URLs:                     map[uint32]string{1: "https://alive.example.com"},
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

// TestBuildInitialCache_RollupDataErrorSkipsNetworkButContinues verifies a per-network hard error
// from RollupIDToRollupData is logged and skipped, without aborting enumeration of the remaining
// networks.
func TestBuildInitialCache_RollupDataErrorSkipsNetworkButContinues(t *testing.T) {
	rm := mocks.NewRollupManagerQuerier(t)
	hc := mocks.NewHealthChecker(t)

	rm.EXPECT().RollupCount(mock.Anything).Return(uint32(2), nil)
	rm.EXPECT().RollupIDToRollupData(mock.Anything, uint32(1)).
		Return(agglayermanager.AgglayerManagerRollupDataReturn{}, context.DeadlineExceeded)
	rm.EXPECT().RollupIDToRollupData(mock.Anything, uint32(2)).
		Return(agglayermanager.AgglayerManagerRollupDataReturn{
			RollupContract: common.HexToAddress("0xabc0000000000000000000000000000000000a"),
		}, nil)

	reader := mocks.NewRollupContractReader(t)
	reader.EXPECT().AggchainMetadata(mock.Anything, MetadataBridgeServiceURLKey).
		Return("https://metadata.example.com:5577", nil)

	hc.EXPECT().IsHealthy(mock.Anything, "https://metadata.example.com:5577").Return(true)

	f, err := New(Config{}, Options{
		RollupManager: rm,
		ReaderFactory: func(common.Address, aggkittypes.BaseEthereumClienter) (RollupContractReader, error) {
			return reader, nil
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
	require.Equal(t, "https://metadata.example.com:5577", got)
}
