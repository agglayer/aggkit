package exit_certificate

import (
	"context"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func ptrStatus(s agglayertypes.CertificateStatus) *agglayertypes.CertificateStatus { return &s }
func ptrUint64(v uint64) *uint64                                                   { return &v }

// TestRunStepHPendingCertificateRejected covers the guard that refuses to proceed when the agglayer
// still has a non-settled (open) certificate for the network.
func TestRunStepHPendingCertificateRejected(t *testing.T) {
	t.Parallel()

	t.Run("open certificate with known height", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(7)).Return(agglayertypes.NetworkInfo{
			LatestPendingStatus: ptrStatus(agglayertypes.Pending),
			LatestPendingHeight: ptrUint64(3),
		}, nil)

		_, err := runStepH(context.Background(), &Config{L2NetworkID: 7}, client, nil)
		require.ErrorContains(t, err, "network 7 has a pending certificate")
		require.ErrorContains(t, err, "status Pending")
		require.ErrorContains(t, err, "height 3")
	})

	t.Run("open certificate with unknown height", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		// Candidate is also an open status; with a nil height the message reports "unknown".
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(7)).Return(agglayertypes.NetworkInfo{
			LatestPendingStatus: ptrStatus(agglayertypes.Candidate),
		}, nil)

		_, err := runStepH(context.Background(), &Config{L2NetworkID: 7}, client, nil)
		require.ErrorContains(t, err, "height unknown")
	})
}

// TestRunStepHSettled covers the happy paths once no open certificate blocks the step: the settled
// LER and next height are derived from the network info.
func TestRunStepHSettled(t *testing.T) {
	t.Parallel()
	settledLER := common.HexToHash("0xabc")

	t.Run("settled certificate present", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
			SettledLER:    &settledLER,
			SettledHeight: ptrUint64(4),
		}, nil)

		res, err := runStepH(context.Background(), &Config{L2NetworkID: 1}, client, nil)
		require.NoError(t, err)
		require.Equal(t, settledLER, res.PreviousLocalExitRoot)
		require.Equal(t, uint64(5), res.Height) // settled height + 1
	})

	t.Run("no settled certificate yet → zero prev LER", func(t *testing.T) {
		t.Parallel()
		// A settled InError status is closed (not open), so the guard passes; no SettledLER → zero.
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
			LatestPendingStatus: ptrStatus(agglayertypes.Settled),
		}, nil)

		res, err := runStepH(context.Background(), &Config{L2NetworkID: 1}, client, nil)
		require.NoError(t, err)
		require.Equal(t, common.Hash{}, res.PreviousLocalExitRoot)
		require.Equal(t, uint64(0), res.Height)
	})
}

// TestRunStepHLERMismatch covers the cross-check against Step G's InitialLocalExitRoot.
func TestRunStepHLERMismatch(t *testing.T) {
	t.Parallel()
	settledLER := common.HexToHash("0xabc")

	t.Run("mismatch is an error", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
			SettledLER: &settledLER,
		}, nil)

		gResult := &StepGResult{InitialLocalExitRoot: common.HexToHash("0xdead")}
		_, err := runStepH(context.Background(), &Config{L2NetworkID: 1}, client, gResult)
		require.ErrorContains(t, err, "LocalExitRoot mismatch")
	})

	t.Run("match succeeds", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, uint32(1)).Return(agglayertypes.NetworkInfo{
			SettledLER: &settledLER,
		}, nil)

		gResult := &StepGResult{InitialLocalExitRoot: settledLER}
		res, err := runStepH(context.Background(), &Config{L2NetworkID: 1}, client, gResult)
		require.NoError(t, err)
		require.Equal(t, settledLER, res.PreviousLocalExitRoot)
	})
}

func TestRunStepHGetNetworkInfoError(t *testing.T) {
	t.Parallel()
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetNetworkInfo(mock.Anything, mock.Anything).Return(agglayertypes.NetworkInfo{}, errors.New("boom"))

	_, err := runStepH(context.Background(), &Config{L2NetworkID: 1}, client, nil)
	require.ErrorContains(t, err, "get network info")
}
