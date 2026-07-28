package claimer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/l1infotreesync"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const sampleWaitResult = `{
  "certificateHash": "0x1234000000000000000000000000000000000000000000000000000000000000",
  "finalStatus": "Settled",
  "updateL1InfoTree": {
    "mainnetExitRoot": "0x1111000000000000000000000000000000000000000000000000000000000000",
    "rollupExitRoot": "0x2222000000000000000000000000000000000000000000000000000000000000",
    "txHash": "0x3333000000000000000000000000000000000000000000000000000000000000"
  }
}`

func TestLoadStepWaitResult(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "step-wait-result.json")
	require.NoError(t, os.WriteFile(path, []byte(sampleWaitResult), 0o600))

	result, err := LoadStepWaitResult(path)
	require.NoError(t, err)
	require.Equal(t,
		common.HexToHash("0x1234000000000000000000000000000000000000000000000000000000000000"),
		result.CertificateHash)
	require.NotNil(t, result.UpdateL1InfoTree)
	require.Equal(t,
		common.HexToHash("0x1111000000000000000000000000000000000000000000000000000000000000"),
		result.UpdateL1InfoTree.MainnetExitRoot)
	require.Equal(t,
		common.HexToHash("0x2222000000000000000000000000000000000000000000000000000000000000"),
		result.UpdateL1InfoTree.RollupExitRoot)
}

func TestLoadStepWaitResultErrors(t *testing.T) {
	t.Parallel()

	_, err := LoadStepWaitResult(filepath.Join(t.TempDir(), "missing.json"))
	require.ErrorContains(t, err, "reading wait result")

	badPath := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(badPath, []byte(`{not json`), 0o600))
	_, err = LoadStepWaitResult(badPath)
	require.ErrorContains(t, err, "parsing wait result")
}

func TestSettlementGER(t *testing.T) {
	t.Parallel()

	mainnet := common.HexToHash("0x1111")
	rollup := common.HexToHash("0x2222")
	result := &exitcertificate.StepWaitResult{
		UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{
			MainnetExitRoot: mainnet,
			RollupExitRoot:  rollup,
		},
	}

	ger, err := SettlementGER(result)
	require.NoError(t, err)
	require.Equal(t, l1infotreesync.CalculateGER(mainnet, rollup), ger)
}

func TestSettlementGERMissingUpdate(t *testing.T) {
	t.Parallel()

	_, err := SettlementGER(&exitcertificate.StepWaitResult{})
	require.ErrorContains(t, err, "no updateL1InfoTree event")
}
