package backward_forward_let

import (
	"context"
	"encoding/json"
	"flag"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestLoadCertIDsFile(t *testing.T) {
	t.Parallel()

	certID := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	path := filepath.Join(t.TempDir(), "cert-ids.json")
	require.NoError(t, os.WriteFile(path, []byte(`{
	  "network_id": 7,
	  "certificates": {
	    "42": "`+certID.Hex()+`"
	  }
	}`), 0o600))

	got, err := loadCertIDsFile(path, 7)
	require.NoError(t, err)
	require.Equal(t, certID, got[42])
}

func TestLoadCertIDsFileRejectsWrongNetwork(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "cert-ids.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"network_id":8,"certificates":{}}`), 0o600))

	_, err := loadCertIDsFile(path, 7)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match config L2NetworkID")
}

func TestParseCertificateIDRejectsShortHex(t *testing.T) {
	t.Parallel()

	_, err := parseCertificateID("0xdead")
	require.Error(t, err)
	require.Contains(t, err.Error(), "32-byte")
}

func TestValidateAdminCertificate(t *testing.T) {
	t.Parallel()

	cert := exportTestCertificate(7, 42, nil)
	certID := cert.CertificateID()

	require.NoError(t, validateAdminCertificate(cert, 7, 42, certID))
	require.ErrorContains(t, validateAdminCertificate(cert, 8, 42, certID), "network_id")
	require.ErrorContains(t, validateAdminCertificate(cert, 7, 43, certID), "height")
	require.ErrorContains(t, validateAdminCertificate(cert, 7, 42, common.HexToHash("0x01")), "certificate ID")
}

func TestRunExportCertExitsWritesOverrideAndManifest(t *testing.T) {
	cert42 := exportTestCertificate(7, 42, []*agglayertypes.BridgeExit{
		exportTestBridgeExit(1),
	})
	cert43 := exportTestCertificate(7, 43, nil)
	cert42ID := cert42.CertificateID()
	cert43ID := cert43.CertificateID()

	oldFetch := fetchAdminCertificate
	fetchAdminCertificate = func(_ context.Context, adminURL string, certID common.Hash) (*agglayertypes.Certificate, error) {
		require.Equal(t, "http://example.test/admin?debug=true", adminURL)
		switch certID {
		case cert42ID:
			return cert42, nil
		case cert43ID:
			return cert43, nil
		default:
			t.Fatalf("unexpected cert ID: %s", certID.Hex())
			return nil, nil
		}
	}
	t.Cleanup(func() { fetchAdminCertificate = oldFetch })

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[BackwardForwardLET]\nL2NetworkID = 7\n"), 0o600))
	certIDsPath := filepath.Join(tmpDir, "cert-ids.json")
	require.NoError(t, os.WriteFile(certIDsPath, []byte(`{
	  "network_id": 7,
	  "certificates": {
	    "42": "`+cert42ID.Hex()+`",
	    "43": "`+cert43ID.Hex()+`"
	  }
	}`), 0o600))

	outPath := filepath.Join(tmpDir, "override.json")
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	ctx := newExportCertExitsCLIContext(t, cfgPath, map[string]string{
		"agglayer-admin-url": "http://example.test/admin?debug=true",
		"cert-ids-file":      certIDsPath,
		"out":                outPath,
		"manifest-out":       manifestPath,
		"max-certs":          "10",
		"timeout":            "1m",
	})

	require.NoError(t, RunExportCertExits(ctx))

	overrideData, err := os.ReadFile(outPath)
	require.NoError(t, err)
	var override overrideFileJSON
	require.NoError(t, json.Unmarshal(overrideData, &override))
	require.Equal(t, uint32(7), override.NetworkID)
	require.Len(t, override.Heights["42"], 1)
	require.Empty(t, override.Heights["43"])

	manifestData, err := os.ReadFile(manifestPath)
	require.NoError(t, err)
	var manifest exportCertExitsManifest
	require.NoError(t, json.Unmarshal(manifestData, &manifest))
	require.Equal(t, "http://example.test/admin", manifest.AgglayerAdminURL)
	require.Len(t, manifest.Certificates, 2)
	require.Equal(t, uint64(42), manifest.Certificates[0].Height)
	require.Equal(t, 1, manifest.Certificates[0].BridgeExitCount)
	require.Equal(t, uint64(43), manifest.Certificates[1].Height)
	require.Equal(t, 0, manifest.Certificates[1].BridgeExitCount)
}

func TestRunExportCertExitsRejectsOverMaxCerts(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	require.NoError(t, os.WriteFile(cfgPath, []byte("[BackwardForwardLET]\nL2NetworkID = 7\n"), 0o600))
	certID := common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")
	certIDsPath := filepath.Join(tmpDir, "cert-ids.json")
	require.NoError(t, os.WriteFile(certIDsPath, []byte(`{
	  "network_id": 7,
	  "certificates": {
	    "1": "`+certID.Hex()+`",
	    "2": "`+certID.Hex()+`"
	  }
	}`), 0o600))

	ctx := newExportCertExitsCLIContext(t, cfgPath, map[string]string{
		"agglayer-admin-url": "http://example.test/admin",
		"cert-ids-file":      certIDsPath,
		"out":                filepath.Join(tmpDir, "override.json"),
		"max-certs":          "1",
		"timeout":            "1m",
	})

	err := RunExportCertExits(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "refusing to export 2 certificates")
}

func newExportCertExitsCLIContext(t *testing.T, configPath string, flags map[string]string) *cli.Context {
	t.Helper()
	app := cli.NewApp()
	app.Flags = []cli.Flag{
		&cli.StringSliceFlag{Name: "cfg", Aliases: []string{"c"}},
	}
	parentSet := flag.NewFlagSet("app", flag.ContinueOnError)
	for _, f := range app.Flags {
		require.NoError(t, f.Apply(parentSet))
	}
	require.NoError(t, parentSet.Parse([]string{"--cfg", configPath}))
	parentCtx := cli.NewContext(app, parentSet, nil)

	commandSet := flag.NewFlagSet("export-cert-exits", flag.ContinueOnError)
	commandSet.String("agglayer-admin-url", "", "")
	commandSet.String("cert-ids-file", "", "")
	commandSet.String("out", "", "")
	commandSet.String("manifest-out", "", "")
	commandSet.Uint64("max-certs", DefaultExportCertExitsMaxCerts, "")
	commandSet.Duration("timeout", DefaultExportCertExitsTimeout, "")
	for name, value := range flags {
		require.NoError(t, commandSet.Set(name, value))
	}
	return cli.NewContext(app, commandSet, parentCtx)
}

func exportTestCertificate(networkID uint32, height uint64, exits []*agglayertypes.BridgeExit) *agglayertypes.Certificate {
	if exits == nil {
		exits = []*agglayertypes.BridgeExit{}
	}
	return &agglayertypes.Certificate{
		NetworkID:           networkID,
		Height:              height,
		PrevLocalExitRoot:   common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		NewLocalExitRoot:    common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		BridgeExits:         exits,
		ImportedBridgeExits: []*agglayertypes.ImportedBridgeExit{},
	}
}

func exportTestBridgeExit(index byte) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		LeafType: bridgetypes.LeafTypeAsset,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      0,
			OriginTokenAddress: common.Address{},
		},
		DestinationNetwork: 7,
		DestinationAddress: common.BytesToAddress([]byte{index}),
		Amount:             big.NewInt(int64(index)),
		Metadata:           nil,
	}
}
