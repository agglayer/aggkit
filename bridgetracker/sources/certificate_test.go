package sources

import (
	"context"
	"errors"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var errFakeCertificateHeaderClient = errors.New("fake certificate header client error")

// fakeCertificateHeaderClient is a fixed CertificateHeaderClient for tests
type fakeCertificateHeaderClient struct {
	header *agglayertypes.CertificateHeader
	err    error
}

func (f *fakeCertificateHeaderClient) GetCertificateHeader(
	_ context.Context, _ common.Hash,
) (*agglayertypes.CertificateHeader, error) {
	return f.header, f.err
}

func TestCertificateSourceCertificateForNotImplemented(t *testing.T) {
	source := NewCertificateSource(&fakeCertificateHeaderClient{})

	cert, err := source.CertificateFor(t.Context(), l2ToL1Bridge())
	require.Nil(t, cert)
	require.ErrorIs(t, err, ErrCertificateResolutionNotImplemented)
}

func TestCertificateSourceCertificateHeaderFor(t *testing.T) {
	certID := common.HexToHash("0x0f")
	settlementTx := common.HexToHash("0x10")
	fake := &fakeCertificateHeaderClient{header: &agglayertypes.CertificateHeader{
		CertificateID:    certID,
		Status:           agglayertypes.Settled,
		SettlementTxHash: &settlementTx,
	}}
	source := NewCertificateSource(fake)

	cert, err := source.certificateHeaderFor(t.Context(), certID)
	require.NoError(t, err)
	require.Equal(t, certID, cert.CertificateID)
	require.Equal(t, agglayertypes.Settled, cert.Status)
	require.Equal(t, &settlementTx, cert.SettlementTxHash)
	require.Empty(t, cert.Error)
}

func TestCertificateSourceCertificateHeaderForTransientError(t *testing.T) {
	fake := &fakeCertificateHeaderClient{err: errFakeCertificateHeaderClient}
	source := NewCertificateSource(fake)

	_, err := source.certificateHeaderFor(t.Context(), common.HexToHash("0x0f"))
	require.ErrorIs(t, err, errFakeCertificateHeaderClient)
}
