package bridgeservicefinder

import (
	"errors"
	"testing"

	"github.com/agglayer/aggkit/bridgeservicefinder/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newTestFinderForBridgeAddress builds a *finder (not the Finder interface) with rm as its
// RollupManagerQuerier, so tests can call BridgeAddress directly without going through Start
// (BridgeAddress is resolved lazily, on first use, independently of the cache built by Start).
func newTestFinderForBridgeAddress(t *testing.T, cfg Config, rm RollupManagerQuerier) *finder {
	t.Helper()

	f, err := New(cfg, Options{RollupManager: rm, LogFilterer: mocks.NewLogFilterer(t), Logger: testLogger()})
	require.NoError(t, err)
	concrete, ok := f.(*finder)
	require.True(t, ok)
	return concrete
}

// TestBridgeAddress_DefaultsToRollupManagerBridgeAddress verifies a network absent from
// Config.BridgeAddress resolves to the rollup manager's own on-chain BridgeAddress().
func TestBridgeAddress_DefaultsToRollupManagerBridgeAddress(t *testing.T) {
	wantAddr := common.HexToAddress("0xb1123e")
	rm := mocks.NewRollupManagerQuerier(t)
	rm.EXPECT().BridgeAddress(mock.Anything).Return(wantAddr, nil).Once()

	f := newTestFinderForBridgeAddress(t, Config{}, rm)

	got, err := f.BridgeAddress(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, wantAddr, got)
}

// TestBridgeAddress_CachesTheOnChainDefaultAcrossNetworksAndCalls verifies the on-chain
// BridgeAddress() call happens at most once, regardless of how many networks or calls ask for
// the default — it is an immutable constructor parameter, safe to cache forever.
func TestBridgeAddress_CachesTheOnChainDefaultAcrossNetworksAndCalls(t *testing.T) {
	wantAddr := common.HexToAddress("0xb1123e")
	rm := mocks.NewRollupManagerQuerier(t)
	rm.EXPECT().BridgeAddress(mock.Anything).Return(wantAddr, nil).Once() // .Once(): a second call fails the test

	f := newTestFinderForBridgeAddress(t, Config{}, rm)

	for _, networkID := range []uint32{1, 2, 1} {
		got, err := f.BridgeAddress(t.Context(), networkID)
		require.NoError(t, err)
		require.Equal(t, wantAddr, got)
	}
}

// TestBridgeAddress_OverridePrecedesTheOnChainDefault verifies a networkID present in
// Config.BridgeAddress is served verbatim, without ever consulting the rollup manager.
func TestBridgeAddress_OverridePrecedesTheOnChainDefault(t *testing.T) {
	overrideAddr := common.HexToAddress("0xdeaf")
	rm := mocks.NewRollupManagerQuerier(t) // no BridgeAddress expectation: must never be called

	f := newTestFinderForBridgeAddress(t, Config{BridgeAddress: map[uint32]common.Address{63: overrideAddr}}, rm)

	got, err := f.BridgeAddress(t.Context(), 63)
	require.NoError(t, err)
	require.Equal(t, overrideAddr, got)
}

// TestBridgeAddress_Network0OverrideIsDefaultForOtherNetworks verifies a Config.BridgeAddress[0]
// override doubles as the default for a network with no override of its own, taking precedence
// over the on-chain rollup manager BridgeAddress() (which must never be consulted in this case).
func TestBridgeAddress_Network0OverrideIsDefaultForOtherNetworks(t *testing.T) {
	network0Addr := common.HexToAddress("0xcafe")
	rm := mocks.NewRollupManagerQuerier(t) // no BridgeAddress expectation: must never be called

	f := newTestFinderForBridgeAddress(t, Config{BridgeAddress: map[uint32]common.Address{0: network0Addr}}, rm)

	for _, networkID := range []uint32{0, 5, 82} {
		got, err := f.BridgeAddress(t.Context(), networkID)
		require.NoError(t, err)
		require.Equal(t, network0Addr, got)
	}
}

// TestBridgeAddress_PerNetworkOverridePrecedesNetwork0Default verifies a network's own override
// wins over Config.BridgeAddress[0], even when both are configured.
func TestBridgeAddress_PerNetworkOverridePrecedesNetwork0Default(t *testing.T) {
	network0Addr := common.HexToAddress("0xcafe")
	network63Addr := common.HexToAddress("0xdeaf")
	rm := mocks.NewRollupManagerQuerier(t) // no BridgeAddress expectation: must never be called

	f := newTestFinderForBridgeAddress(t, Config{
		BridgeAddress: map[uint32]common.Address{0: network0Addr, 63: network63Addr},
	}, rm)

	got, err := f.BridgeAddress(t.Context(), 63)
	require.NoError(t, err)
	require.Equal(t, network63Addr, got)

	got, err = f.BridgeAddress(t.Context(), 84)
	require.NoError(t, err)
	require.Equal(t, network0Addr, got)
}

// TestBridgeAddress_OnChainFailureIsNotCached verifies a failed on-chain read is not cached: the
// next call retries instead of repeating the same error forever.
func TestBridgeAddress_OnChainFailureIsNotCached(t *testing.T) {
	wantAddr := common.HexToAddress("0xb1123e")
	wantErr := errors.New("rpc unavailable")
	rm := mocks.NewRollupManagerQuerier(t)
	rm.EXPECT().BridgeAddress(mock.Anything).Return(common.Address{}, wantErr).Once()
	rm.EXPECT().BridgeAddress(mock.Anything).Return(wantAddr, nil).Once()

	f := newTestFinderForBridgeAddress(t, Config{}, rm)

	_, err := f.BridgeAddress(t.Context(), 1)
	require.ErrorIs(t, err, wantErr)

	got, err := f.BridgeAddress(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, wantAddr, got)
}
