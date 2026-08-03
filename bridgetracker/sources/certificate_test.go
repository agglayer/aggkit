package sources

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgeservicefinder"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var errFakeCertificateHeaderClient = errors.New("fake certificate header client error")

var testLogger = log.WithFields("module", "certificate_test")

// fakeCertificateHeaderClient is a fixed CertificateHeaderClient for tests
type fakeCertificateHeaderClient struct {
	header *agglayertypes.CertificateHeader
	err    error

	settled    *agglayertypes.CertificateHeader
	settledErr error
	pending    *agglayertypes.CertificateHeader
	pendingErr error
}

func (f *fakeCertificateHeaderClient) GetCertificateHeader(
	_ context.Context, _ common.Hash,
) (*agglayertypes.CertificateHeader, error) {
	return f.header, f.err
}

func (f *fakeCertificateHeaderClient) GetLatestSettledCertificateHeader(
	_ context.Context, _ uint32,
) (*agglayertypes.CertificateHeader, error) {
	return f.settled, f.settledErr
}

func (f *fakeCertificateHeaderClient) GetLatestPendingCertificateHeader(
	_ context.Context, _ uint32,
) (*agglayertypes.CertificateHeader, error) {
	return f.pending, f.pendingErr
}

// fakeRootIndexes serves /bridge/v1/root-by-ler for a fixed set of known roots, keyed by hex
// LER; an unknown root answers 404 ("not synced yet"), like the real bridge service does
type fakeRootIndexes map[string]uint32

// start serves fakeRootIndexes on network l2ToL1Bridge().NetworkID (5), the only network
// certificateIDFor ever asks for a root index (bridge's own origin network)
func (f fakeRootIndexes) start(t *testing.T) NetworkURLResolver {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/bridge/v1/root-by-ler", func(w http.ResponseWriter, r *http.Request) {
		index, ok := f[r.URL.Query().Get("ler")]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"not found (not synced yet)"}`)
			return
		}
		fmt.Fprintf(w, `{"index":%d,"block_num":0,"block_position":0}`, index)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return staticURLs{l2ToL1Bridge().NetworkID: bridgeservicefinder.NetworkURLs{BridgeURL: server.URL}}
}

func TestCertificateSourceCertificateHeaderFor(t *testing.T) {
	certID := common.HexToHash("0x0f")
	settlementTx := common.HexToHash("0x10")
	fake := &fakeCertificateHeaderClient{header: &agglayertypes.CertificateHeader{
		CertificateID:    certID,
		Status:           agglayertypes.Settled,
		SettlementTxHash: &settlementTx,
	}}
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	cert, err := source.certificateHeaderFor(t.Context(), certID)
	require.NoError(t, err)
	require.Equal(t, certID, cert.CertificateID)
	require.Equal(t, agglayertypes.Settled, cert.Status)
	require.Equal(t, &settlementTx, cert.SettlementTxHash)
	require.Empty(t, cert.Error)
}

func TestCertificateSourceCertificateHeaderForTransientError(t *testing.T) {
	fake := &fakeCertificateHeaderClient{err: errFakeCertificateHeaderClient}
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	_, err := source.certificateHeaderFor(t.Context(), common.HexToHash("0x0f"))
	require.ErrorIs(t, err, errFakeCertificateHeaderClient)
}

func TestCertificateIDForSettledCovers(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	ler := common.HexToHash("0xaaaa")
	certID := common.HexToHash("0xbbbb")

	fake := &fakeCertificateHeaderClient{
		settled: &agglayertypes.CertificateHeader{CertificateID: certID, NewLocalExitRoot: ler},
	}
	source := NewCertificateSource(fake, fakeRootIndexes{ler.Hex(): 7}.start(t), testLogger)

	got, err := source.certificateIDFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Equal(t, &certID, got)
}

func TestCertificateIDForSettledNotCoveredButPendingSurfaced(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	settledLER := common.HexToHash("0xaaaa")
	pendingLER := common.HexToHash("0xcccc")
	pendingCertID := common.HexToHash("0xdddd")

	fake := &fakeCertificateHeaderClient{
		settled: &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0xbbbb"), NewLocalExitRoot: settledLER},
		pending: &agglayertypes.CertificateHeader{CertificateID: pendingCertID, NewLocalExitRoot: pendingLER},
	}
	// pending's root deliberately does not cover bridge (index 3 < DepositCount 7) either: it
	// must still be surfaced (see certificateIDFor's doc) since it is not settled/terminal
	source := NewCertificateSource(fake, fakeRootIndexes{settledLER.Hex(): 5, pendingLER.Hex(): 3}.start(t), testLogger)

	got, err := source.certificateIDFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Equal(t, &pendingCertID, got)
}

func TestCertificateIDForSettledNotCoveredNoPending(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	settledLER := common.HexToHash("0xaaaa")

	fake := &fakeCertificateHeaderClient{
		settled: &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0xbbbb"), NewLocalExitRoot: settledLER},
	}
	// A settled-but-non-covering certificate must never be returned: CertificatePendingResolver
	// treats Settled as "done", so this would make the tracker think the step completed early
	source := NewCertificateSource(fake, fakeRootIndexes{settledLER.Hex(): 5}.start(t), testLogger)

	got, err := source.certificateIDFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestCertificateIDForNoSettledNoPending(t *testing.T) {
	bridge := l2ToL1Bridge()
	fake := &fakeCertificateHeaderClient{}
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	got, err := source.certificateIDFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Nil(t, got)
}

func TestCertificateIDForNoSettledPendingSurfaced(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	pendingLER := common.HexToHash("0xcccc")
	pendingCertID := common.HexToHash("0xdddd")

	fake := &fakeCertificateHeaderClient{
		pending: &agglayertypes.CertificateHeader{CertificateID: pendingCertID, NewLocalExitRoot: pendingLER},
	}
	source := NewCertificateSource(fake, fakeRootIndexes{pendingLER.Hex(): 7}.start(t), testLogger)

	got, err := source.certificateIDFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Equal(t, &pendingCertID, got)
}

func TestCertificateIDForSettledErrorPropagates(t *testing.T) {
	bridge := l2ToL1Bridge()
	fake := &fakeCertificateHeaderClient{settledErr: errFakeCertificateHeaderClient}
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	_, err := source.certificateIDFor(t.Context(), bridge)
	require.ErrorIs(t, err, errFakeCertificateHeaderClient)
}

func TestCertificateIDForPendingErrorPropagates(t *testing.T) {
	bridge := l2ToL1Bridge()
	fake := &fakeCertificateHeaderClient{pendingErr: errFakeCertificateHeaderClient}
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	_, err := source.certificateIDFor(t.Context(), bridge)
	require.ErrorIs(t, err, errFakeCertificateHeaderClient)
}

func TestCertificateIDForRootNotSyncedYetIsTransient(t *testing.T) {
	bridge := l2ToL1Bridge()
	settledLER := common.HexToHash("0xaaaa")

	fake := &fakeCertificateHeaderClient{
		settled: &agglayertypes.CertificateHeader{CertificateID: common.HexToHash("0xbbbb"), NewLocalExitRoot: settledLER},
	}
	// the bridge service on bridge.NetworkID has not synced settledLER yet: retried by the
	// engine, not treated as "not covered"
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	_, err := source.certificateIDFor(t.Context(), bridge)
	require.Error(t, err)
}

func TestCertificateForNotCovered(t *testing.T) {
	bridge := l2ToL1Bridge()
	fake := &fakeCertificateHeaderClient{}
	source := NewCertificateSource(fake, fakeRootIndexes{}.start(t), testLogger)

	cert, err := source.CertificateFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Nil(t, cert)
}

func TestCertificateForCovered(t *testing.T) {
	bridge := l2ToL1Bridge() // DepositCount: 7
	ler := common.HexToHash("0xaaaa")
	certID := common.HexToHash("0xbbbb")
	settlementTx := common.HexToHash("0x10")

	fake := &fakeCertificateHeaderClient{
		settled: &agglayertypes.CertificateHeader{CertificateID: certID, NewLocalExitRoot: ler},
		header: &agglayertypes.CertificateHeader{
			CertificateID:    certID,
			Status:           agglayertypes.Settled,
			SettlementTxHash: &settlementTx,
		},
	}
	source := NewCertificateSource(fake, fakeRootIndexes{ler.Hex(): 7}.start(t), testLogger)

	cert, err := source.CertificateFor(t.Context(), bridge)
	require.NoError(t, err)
	require.Equal(t, certID, cert.CertificateID)
	require.Equal(t, agglayertypes.Settled, cert.Status)
	require.Equal(t, &settlementTx, cert.SettlementTxHash)
}
