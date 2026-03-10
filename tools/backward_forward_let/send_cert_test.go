package backward_forward_let

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

// --- stubs ---

type stubAgglayerSender struct {
	hash common.Hash
	err  error
}

func (s *stubAgglayerSender) SendCertificate(_ context.Context, _ *agglayertypes.Certificate) (common.Hash, error) {
	return s.hash, s.err
}

type stubCertStorager struct {
	err     error
	prevErr error
	prev    *aggsendertypes.CertificateHeader
	// last call captured for assertions
	saved *aggsendertypes.Certificate
}

func (s *stubCertStorager) SaveLastSentCertificate(_ context.Context, cert aggsendertypes.Certificate) error {
	s.saved = &cert
	return s.err
}

func (s *stubCertStorager) GetCertificateHeaderByHeight(_ uint64) (*aggsendertypes.CertificateHeader, error) {
	return s.prev, s.prevErr
}

// --- helpers ---

// minimalCertJSON returns the minimal valid JSON for a PP certificate.
func minimalCertJSON(height uint64) string {
	return `{"network_id":1,"height":` + uint64str(height) + `,"prev_local_exit_root":"0x0000000000000000000000000000000000000000000000000000000000000000","new_local_exit_root":"0x0101010101010101010101010101010101010101010101010101010101010101","bridge_exits":[],"imported_bridge_exits":[],"aggchain_data":{"aggchain_data_signature":{"signature":"abcdef"}}}`
}

// uint64str converts a uint64 to its decimal string representation without importing strconv at the top.
func uint64str(n uint64) string {
	if n == 0 {
		return "0"
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return string(digits[i:])
}

// newCLIContext builds a *cli.Context with the given string flags.
func newCLIContext(flags map[string]string) *cli.Context {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	for name := range flags {
		fs.String(name, "", "")
	}
	for name, val := range flags {
		_ = fs.Set(name, val)
	}
	return cli.NewContext(cli.NewApp(), fs, nil)
}

// --- Tests for aggsenderCertTypeFromAggchainData ---

func TestAggsenderCertTypeFromAggchainData_Signature(t *testing.T) {
	t.Parallel()

	got := aggsenderCertTypeFromAggchainData(&agglayertypes.AggchainDataSignature{})
	require.Equal(t, aggsendertypes.CertificateTypePP, got)
}

func TestAggsenderCertTypeFromAggchainData_Multisig(t *testing.T) {
	t.Parallel()

	got := aggsenderCertTypeFromAggchainData(&agglayertypes.AggchainDataMultisig{})
	require.Equal(t, aggsendertypes.CertificateTypePP, got)
}

func TestAggsenderCertTypeFromAggchainData_Proof(t *testing.T) {
	t.Parallel()

	got := aggsenderCertTypeFromAggchainData(&agglayertypes.AggchainDataProof{})
	require.Equal(t, aggsendertypes.CertificateTypeFEP, got)
}

func TestAggsenderCertTypeFromAggchainData_MultisigWithProof(t *testing.T) {
	t.Parallel()

	got := aggsenderCertTypeFromAggchainData(&agglayertypes.AggchainDataMultisigWithProof{})
	require.Equal(t, aggsendertypes.CertificateTypeFEP, got)
}

func TestAggsenderCertTypeFromAggchainData_Nil(t *testing.T) {
	t.Parallel()

	// nil falls to the default case → PP
	got := aggsenderCertTypeFromAggchainData(nil)
	require.Equal(t, aggsendertypes.CertificateTypePP, got)
}

// --- Tests for sendCertificate ---

func TestSendCertificate_HappyPath(t *testing.T) {
	t.Parallel()

	expectedHash := common.HexToHash("0xdeadbeef")
	sender := &stubAgglayerSender{hash: expectedHash}
	storage := &stubCertStorager{}

	certJSON := minimalCertJSON(7)
	var cert agglayertypes.Certificate
	require.NoError(t, cert.UnmarshalJSON([]byte(certJSON)))

	err := sendCertificate(context.Background(), cert, certJSON, sender, storage)
	require.NoError(t, err)

	// DB record should be saved.
	require.NotNil(t, storage.saved)
	require.NotNil(t, storage.saved.Header)
	require.Equal(t, uint64(7), storage.saved.Header.Height)
	require.Equal(t, expectedHash, storage.saved.Header.CertificateID)
	require.Equal(t, agglayertypes.Pending, storage.saved.Header.Status)
	require.Equal(t, aggsendertypes.CertificateSourceLocal, storage.saved.Header.CertSource)
	require.Equal(t, aggsendertypes.CertificateTypePP, storage.saved.Header.CertType)
	require.NotNil(t, storage.saved.SignedCertificate)
	require.Equal(t, certJSON, *storage.saved.SignedCertificate)
}

func TestSendCertificate_AgglayerError(t *testing.T) {
	t.Parallel()

	agglayerErr := errors.New("agglayer unavailable")
	sender := &stubAgglayerSender{err: agglayerErr}
	storage := &stubCertStorager{}

	certJSON := minimalCertJSON(1)
	var cert agglayertypes.Certificate
	require.NoError(t, cert.UnmarshalJSON([]byte(certJSON)))

	err := sendCertificate(context.Background(), cert, certJSON, sender, storage)
	require.Error(t, err)
	require.ErrorIs(t, err, agglayerErr)
	// DB must not be written if agglayer fails.
	require.Nil(t, storage.saved)
}

func TestSendCertificate_DBError(t *testing.T) {
	t.Parallel()

	dbErr := errors.New("disk full")
	sender := &stubAgglayerSender{hash: common.HexToHash("0x1234")}
	storage := &stubCertStorager{err: dbErr}

	certJSON := minimalCertJSON(3)
	var cert agglayertypes.Certificate
	require.NoError(t, cert.UnmarshalJSON([]byte(certJSON)))

	err := sendCertificate(context.Background(), cert, certJSON, sender, storage)
	require.Error(t, err)
	require.ErrorIs(t, err, dbErr)
}

func TestSendCertificate_FEPCertType(t *testing.T) {
	t.Parallel()

	// Use a cert with AggchainDataProof → CertificateTypeFEP.
	certJSON := `{"network_id":1,"height":2,"prev_local_exit_root":"0x0000000000000000000000000000000000000000000000000000000000000000","new_local_exit_root":"0x0101010101010101010101010101010101010101010101010101010101010101","bridge_exits":[],"imported_bridge_exits":[],"aggchain_data":{"aggchain_data_proof":{"proof":"","aggchain_params":"0x0000000000000000000000000000000000000000000000000000000000000000","context":null,"version":"","vkey":"","signature":""}}}`
	var cert agglayertypes.Certificate
	require.NoError(t, cert.UnmarshalJSON([]byte(certJSON)))

	sender := &stubAgglayerSender{hash: common.HexToHash("0xaaaa")}
	storage := &stubCertStorager{}

	err := sendCertificate(context.Background(), cert, certJSON, sender, storage)
	require.NoError(t, err)
	require.NotNil(t, storage.saved)
	require.Equal(t, aggsendertypes.CertificateTypeFEP, storage.saved.Header.CertType)
}

// --- Tests for readCertJSON ---

func TestReadCertJSON_FromString(t *testing.T) {
	t.Parallel()

	const input = `{"network_id":1}`
	ctx := newCLIContext(map[string]string{"cert-json": input})

	got, err := readCertJSON(ctx)
	require.NoError(t, err)
	require.Equal(t, input, got)
}

func TestReadCertJSON_FromFile(t *testing.T) {
	t.Parallel()

	const content = `{"network_id":2}`
	tmpFile := filepath.Join(t.TempDir(), "cert.json")
	require.NoError(t, os.WriteFile(tmpFile, []byte(content), 0o600))

	ctx := newCLIContext(map[string]string{"cert-file": tmpFile})

	got, err := readCertJSON(ctx)
	require.NoError(t, err)
	require.Equal(t, content, got)
}

func TestReadCertJSON_NeitherProvided(t *testing.T) {
	t.Parallel()

	ctx := newCLIContext(map[string]string{})

	_, err := readCertJSON(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--cert-json or --cert-file")
}

func TestReadCertJSON_FileNotFound(t *testing.T) {
	t.Parallel()

	ctx := newCLIContext(map[string]string{"cert-file": "/nonexistent/path/cert.json"})

	_, err := readCertJSON(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "read cert file")
}

func TestReadCertJSON_CertJSONTakesPrecedence(t *testing.T) {
	t.Parallel()

	// When both flags are set, --cert-json wins (it's checked first).
	const certJSONVal = `{"network_id":99}`
	ctx := newCLIContext(map[string]string{
		"cert-json": certJSONVal,
		"cert-file": "/some/file",
	})

	got, err := readCertJSON(ctx)
	require.NoError(t, err)
	require.Equal(t, certJSONVal, got)
}

// --- Tests for openAggsenderStorage ---

func TestOpenAggsenderStorage_EmptyPath(t *testing.T) {
	t.Parallel()

	_, err := openAggsenderStorage(nil, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "--db-path is required")
}

func TestOpenAggsenderStorage_ValidPath(t *testing.T) {
	t.Parallel()

	dbPath := filepath.Join(t.TempDir(), "aggsender.sqlite")
	logger := log.GetDefaultLogger()
	storage, err := openAggsenderStorage(logger, dbPath)
	require.NoError(t, err)
	require.NotNil(t, storage)
}

// --- Tests for RunSendCert error paths ---

// newSendCertCLIContext builds a *cli.Context suitable for invoking RunSendCert,
// with the send-cert subcommand's flags registered.
func newSendCertCLIContext(flags map[string]string) *cli.Context {
	fs := flag.NewFlagSet("send-cert", flag.ContinueOnError)
	// Register all flags that RunSendCert / its callees might access.
	fs.String("cert-json", "", "")
	fs.String("cert-file", "", "")
	fs.String("db-path", "", "")
	for name, val := range flags {
		_ = fs.Set(name, val)
	}
	// Parent app context (needed for c.StringSlice("cfg")).
	parentFS := flag.NewFlagSet("app", flag.ContinueOnError)
	app := cli.NewApp()
	parentCtx := cli.NewContext(app, parentFS, nil)
	return cli.NewContext(app, fs, parentCtx)
}

// TestRunSendCert_LoadConfigError verifies that a bad config file path is reported.
func TestRunSendCert_LoadConfigError(t *testing.T) {
	t.Parallel()

	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg"},
	}
	app.Commands = []*cli.Command{
		{
			Name:   "send-cert",
			Action: RunSendCert,
			Flags: []cli.Flag{
				&cli.StringFlag{Name: "cert-json"},
				&cli.StringFlag{Name: "cert-file"},
				&cli.StringFlag{Name: "db-path"},
			},
		},
	}

	err := app.Run([]string{"app", "--cfg=/nonexistent/path.toml", "send-cert",
		"--cert-json={}", "--db-path=/tmp/x.sqlite"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "read config")
}

// TestRunSendCert_NoCertProvided verifies the error when no cert flags are set.
// LoadConfig succeeds with default values; readCertJSON fails.
func TestRunSendCert_NoCertProvided(t *testing.T) {
	t.Parallel()

	ctx := newSendCertCLIContext(map[string]string{})
	err := RunSendCert(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "--cert-json or --cert-file")
}

// TestRunSendCert_InvalidCertJSON verifies the JSON parse error path.
// LoadConfig succeeds with defaults; cert-json is provided but invalid.
func TestRunSendCert_InvalidCertJSON(t *testing.T) {
	t.Parallel()

	ctx := newSendCertCLIContext(map[string]string{
		"cert-json": "not-valid-json",
		"db-path":   "/tmp/test.sqlite",
	})
	err := RunSendCert(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse certificate JSON")
}
