package exit_certificate

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// staticLERReader returns a lerReaderFn that always yields the given root, recording the
// blockTag it was called with.
func staticLERReader(root common.Hash, gotBlockTag *string) lerReaderFn {
	return func(_ context.Context, _ string, _ common.Address, blockTag string) (common.Hash, error) {
		if gotBlockTag != nil {
			*gotBlockTag = blockTag
		}
		return root, nil
	}
}

// TestRunStepLERPreflightMatch covers the happy path: the L2 bridge LER at the target block
// equals the agglayer settled LER, so the pipeline may proceed.
func TestRunStepLERPreflightMatch(t *testing.T) {
	t.Parallel()
	settledLER := common.HexToHash("0xabc")
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
		SettledLER: &settledLER,
	}, nil)

	var blockTag string
	err := runStepLERPreflight(context.Background(), &Config{L2NetworkID: 1}, client,
		staticLERReader(settledLER, &blockTag), 42)
	require.NoError(t, err)
	require.Equal(t, "0x2a", blockTag) // the LER must be read at the resolved target block
}

// TestRunStepLERPreflightUnsettledExits covers the AET-11 failure path: a pre-halt L2→L1 bridge
// exit advanced the L2 LER past the agglayer's settled state — the preflight aborts with an
// actionable error before any expensive work runs.
func TestRunStepLERPreflightUnsettledExits(t *testing.T) {
	t.Parallel()
	settledLER := common.HexToHash("0xabc")
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
		SettledLER: &settledLER,
	}, nil)

	err := runStepLERPreflight(context.Background(), &Config{L2NetworkID: 1}, client,
		staticLERReader(common.HexToHash("0xdead"), nil), 42)
	require.ErrorContains(t, err, "target block 42 has unsettled L2 bridge exits")
	require.ErrorContains(t, err, "wait until the agglayer settles them")
}

// TestRunStepLERPreflightPendingCertificate covers the shared pending-certificate guard: an open
// certificate blocks the preflight the same way it blocks Step H.
func TestRunStepLERPreflightPendingCertificate(t *testing.T) {
	t.Parallel()
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetNetworkInfo(mock.Anything, uint32(7)).Return(agglayertypes.NetworkInfo{
		LatestPendingStatus: ptrStatus(agglayertypes.Pending),
		LatestPendingHeight: ptrUint64(3),
	}, nil)

	err := runStepLERPreflight(context.Background(), &Config{L2NetworkID: 7}, client,
		staticLERReader(common.Hash{}, nil), 42)
	require.ErrorContains(t, err, "network 7 has a pending certificate")
}

// TestRunStepLERPreflightErrors covers the error propagation paths: the agglayer query and the
// L2 LER read.
func TestRunStepLERPreflightErrors(t *testing.T) {
	t.Parallel()

	t.Run("network info error", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, mock.Anything).
			Return(agglayertypes.NetworkInfo{}, errors.New("boom"))

		err := runStepLERPreflight(context.Background(), &Config{L2NetworkID: 1}, client,
			staticLERReader(common.Hash{}, nil), 42)
		require.ErrorContains(t, err, "get network info")
	})

	t.Run("LER read error", func(t *testing.T) {
		t.Parallel()
		settledLER := common.HexToHash("0xabc")
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
			SettledLER: &settledLER,
		}, nil)

		readErr := func(context.Context, string, common.Address, string) (common.Hash, error) {
			return common.Hash{}, errors.New("rpc down")
		}
		err := runStepLERPreflight(context.Background(), &Config{L2NetworkID: 1}, client, readErr, 42)
		require.ErrorContains(t, err, "read L2 bridge local exit root at target block 42")
	})
}

// TestRunStepLERPreflightRequiresAgglayerGRPC covers the wrapper's config guard: without the
// agglayer gRPC URL the preflight fails immediately (the same requirement Step H enforces later).
func TestRunStepLERPreflightRequiresAgglayerGRPC(t *testing.T) {
	t.Parallel()

	err := RunStepLERPreflight(context.Background(), &Config{}, 42)
	require.ErrorContains(t, err, "agglayerClient.grpc.url is required")

	cfg := &Config{Options: Options{AgglayerClient: agglayer.ClientConfig{
		GRPC: &aggkitgrpc.ClientConfig{},
	}}}
	err = RunStepLERPreflight(context.Background(), cfg, 42)
	require.ErrorContains(t, err, "agglayerClient.grpc.url is required")
}
