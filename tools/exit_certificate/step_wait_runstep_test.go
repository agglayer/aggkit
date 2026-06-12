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

func TestWaitUntilFinalSettled(t *testing.T) {
	t.Parallel()
	certHash := common.HexToHash("0xc0ffee")
	settlementTx := common.HexToHash("0x5e771e")

	client := mocks.NewAgglayerClientMock(t)
	// First poll returns a transient error (retried), second returns Settled.
	client.EXPECT().GetCertificateHeader(mock.Anything, certHash).
		Return(nil, errors.New("transient")).Once()
	client.EXPECT().GetCertificateHeader(mock.Anything, certHash).
		Return(&agglayertypes.CertificateHeader{
			Status:           agglayertypes.Settled,
			NewLocalExitRoot: common.HexToHash("0xabc"),
			SettlementTxHash: &settlementTx,
		}, nil)

	header, err := waitUntilFinal(context.Background(), client, certHash)
	require.NoError(t, err)
	require.True(t, header.Status.IsSettled())
	require.Equal(t, &settlementTx, header.SettlementTxHash)
}

func TestWaitUntilFinalContextCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled → the select returns immediately

	client := mocks.NewAgglayerClientMock(t)
	_, err := waitUntilFinal(ctx, client, common.HexToHash("0x1"))
	require.ErrorIs(t, err, context.Canceled)
}

func TestRunStepWaitRequiresGRPCURL(t *testing.T) {
	t.Parallel()
	_, err := RunStepWait(context.Background(), &Config{}, &StepSubmitResult{})
	require.ErrorContains(t, err, "agglayerClient.grpc.url is required")
}

func TestRunStepHRequiresGRPCURL(t *testing.T) {
	t.Parallel()
	_, err := RunStepH(context.Background(), &Config{}, nil)
	require.ErrorContains(t, err, "agglayerClient.grpc.url is required")
}

func TestRunStepSubmitRequiresGRPCURL(t *testing.T) {
	t.Parallel()
	_, err := RunStepSubmit(context.Background(), &Config{}, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "agglayerClient.grpc.url is required")
}
