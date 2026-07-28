package claimer

import (
	"context"
	"testing"

	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestResolveRollupManagerMissingL1RPC(t *testing.T) {
	t.Parallel()

	_, err := resolveRollupManager(context.Background(), &exitcertificate.Config{})
	require.ErrorContains(t, err, "l1RpcUrl is not set")
}

func TestResolveRollupManagerMissingSovereignRollup(t *testing.T) {
	t.Parallel()

	_, err := resolveRollupManager(context.Background(), &exitcertificate.Config{
		L1RPCURL: "http://localhost:8545",
	})
	require.ErrorContains(t, err, "sovereignRollupAddr is not set")
}

func TestDeriveFromExitCertificateRequiresRollupManager(t *testing.T) {
	t.Parallel()

	// Without L1 RPC the on-chain RollupManager resolution fails before a config is produced.
	_, err := DeriveFromExitCertificate(context.Background(), &exitcertificate.Config{
		SovereignRollupAddr: common.HexToAddress("0x1234"),
	})
	require.ErrorContains(t, err, "l1RpcUrl is not set")
}
