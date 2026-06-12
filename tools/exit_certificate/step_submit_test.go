package exit_certificate

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// submitConfig wires the L1 RPC URL used to capture the latest L1 block before submission.
func submitConfig(l1URL string) *Config {
	return &Config{L1RPCURL: l1URL, L2NetworkID: 1}
}

func TestRunStepSubmitSuccess(t *testing.T) {
	t.Parallel()
	certHash := common.HexToHash("0xc0ffee")

	// L1 stub: eth_blockNumber → 0x1a4 (420), the block captured before submission.
	l1 := newRPCStub(t, func(method string, _ []any) (json.RawMessage, *jsonRPCError) {
		require.Equal(t, rpcMethodEthBlockNumber, method)
		return quoted("0x1a4"), nil
	})

	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, uint32(1)).Return(nil, nil)
	client.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(certHash, nil)

	res, err := runStepSubmit(context.Background(), submitConfig(l1.URL), client, &agglayertypes.Certificate{})
	require.NoError(t, err)
	require.Equal(t, certHash, res.CertificateHash)
	require.Equal(t, uint64(420), res.L1LatestBlockBeforeSubmittingCertificate)
}

func TestRunStepSubmitClosedPendingProceeds(t *testing.T) {
	t.Parallel()
	// A closed (Settled) latest certificate does not block a new submission.
	l1 := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return quoted("0x1"), nil
	})
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, uint32(1)).Return(
		&agglayertypes.CertificateHeader{Status: agglayertypes.Settled, CertificateID: common.HexToHash("0xaa")}, nil)
	client.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.HexToHash("0xbb"), nil)

	res, err := runStepSubmit(context.Background(), submitConfig(l1.URL), client, &agglayertypes.Certificate{})
	require.NoError(t, err)
	require.Equal(t, common.HexToHash("0xbb"), res.CertificateHash)
}

func TestRunStepSubmitRequiresL1RPC(t *testing.T) {
	t.Parallel()
	// Pending check passes (no pending cert) but l1RpcUrl is unset → the L1-capture guard fires.
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, uint32(1)).Return(nil, nil)

	_, err := runStepSubmit(context.Background(), submitConfig(""), client, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "l1RpcUrl is required for step submit")
}

func TestRunStepSubmitL1CaptureError(t *testing.T) {
	t.Parallel()
	// resolveLatestBlock fails (RPC error) → the capture-latest-block error is returned.
	l1 := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return nil, revertErr()
	})
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, uint32(1)).Return(nil, nil)

	_, err := runStepSubmit(context.Background(), submitConfig(l1.URL), client, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "capture latest L1 block before submission")
}

func TestRunStepSubmitPendingCertificateRejected(t *testing.T) {
	t.Parallel()
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, uint32(1)).Return(
		&agglayertypes.CertificateHeader{
			Status: agglayertypes.Pending, CertificateID: common.HexToHash("0xaa"), Height: 9,
		}, nil)

	_, err := runStepSubmit(context.Background(), submitConfig("http://l1"), client, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "already has a pending certificate")
	require.ErrorContains(t, err, "height: 9")
}

func TestRunStepSubmitPendingCheckError(t *testing.T) {
	t.Parallel()
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, mock.Anything).Return(nil, errors.New("boom"))

	_, err := runStepSubmit(context.Background(), submitConfig("http://l1"), client, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "check pending certificate")
}

func TestRunStepSubmitSendError(t *testing.T) {
	t.Parallel()
	l1 := newRPCStub(t, func(string, []any) (json.RawMessage, *jsonRPCError) {
		return quoted("0x1"), nil
	})
	client := mocks.NewAgglayerClientMock(t)
	client.EXPECT().GetLatestPendingCertificateHeader(mock.Anything, uint32(1)).Return(nil, nil)
	client.EXPECT().SendCertificate(mock.Anything, mock.Anything).Return(common.Hash{}, errors.New("rejected"))

	_, err := runStepSubmit(context.Background(), submitConfig(l1.URL), client, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "send certificate to agglayer")
}
