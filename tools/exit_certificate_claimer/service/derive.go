package claimer

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

// derivedBlockFinality is the L1 finality the derived L1 Info Tree sync runs at. It is fixed (not
// taken from the exit_certificate config, whose targetBlock is an L2 reference) because the L1 Info
// Tree must only follow finalized L1 state.
const derivedBlockFinality = "FinalizedBlock"

// loadOrDeriveConfig selects the config source from the CLI flags: --exit-certificate-config
// derives the claimer config from an exit_certificate config file, otherwise --config loads a native
// claimer config. The two are mutually exclusive.
func loadOrDeriveConfig(ctx context.Context, c *cli.Context, logger *log.Logger) (*Config, error) {
	cfg, err := selectConfig(ctx, c, logger)
	if err != nil {
		return nil, err
	}
	// CLI overrides apply to both the native and derived config.
	if c.IsSet("address") {
		cfg.Address = c.String("address")
	}
	if c.IsSet("port") {
		cfg.Port = c.Int("port")
	}
	return cfg, nil
}

// selectConfig loads the native claimer config, or derives one from an exit_certificate config when
// --exit-certificate-config is given. The two config sources are mutually exclusive.
func selectConfig(ctx context.Context, c *cli.Context, logger *log.Logger) (*Config, error) {
	ecConfigPath := c.String("exit-certificate-config")
	if ecConfigPath == "" {
		return LoadConfig(c.String("config"))
	}
	if c.IsSet("config") {
		return nil, fmt.Errorf("--config and --exit-certificate-config are mutually exclusive")
	}

	logger.Infof("deriving claimer config from exit_certificate config %q", ecConfigPath)
	ecCfg, err := exitcertificate.LoadConfig(ecConfigPath)
	if err != nil {
		return nil, fmt.Errorf("loading exit_certificate config %q: %w", ecConfigPath, err)
	}
	return DeriveFromExitCertificate(ctx, ecCfg)
}

// DeriveFromExitCertificate builds a claimer Config from the exit_certificate tool's Config so both
// tools can share a single source of truth. File paths point inside the exit_certificate output
// directory, the L1 sync parameters reuse the L1 RPC/contracts and tuning, and L1 sync is enabled
// so the claimer keeps the L1 Info Tree DB up to date on its own.
//
// The RollupManager address is not present in the exit_certificate config, so it is always resolved
// on-chain by calling RollupManager() on the aggchainbase contract at SovereignRollupAddr; this
// requires L1RpcUrl to be reachable.
func DeriveFromExitCertificate(ctx context.Context, ec *exitcertificate.Config) (*Config, error) {
	outputDir := ec.Options.OutputDir

	rollupManager, err := resolveRollupManager(ctx, ec)
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Address:               defaultAddress,
		Port:                  defaultPort,
		ReadTimeoutSeconds:    defaultReadTimeoutSeconds,
		WriteTimeoutSeconds:   defaultWriteTimeoutSeconds,
		SignedCertificatePath: filepath.Join(outputDir, "exit-certificate-signed.json"),
		LocalExitTreeDBPath:   filepath.Join(outputDir, "step-g-l2bridgesyncerlite.sqlite"),
		L1InfoTreeDBPath:      filepath.Join(outputDir, "L1InfoTreeSync.sqlite"),
		NetworkID:             ec.L2NetworkID,
		L1Sync: L1SyncConfig{
			Enabled:            true,
			RPCURL:             ec.L1RPCURL,
			GlobalExitRootAddr: ec.L1GlobalExitRootAddress.Hex(),
			RollupManagerAddr:  rollupManager,
			InitialBlock:       ec.Options.L1StartBlock,
			SyncBlockChunkSize: uint64(ec.Options.BlockRange),
			BlockFinality:      derivedBlockFinality,
		},
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("derived config is invalid: %w", err)
	}
	return cfg, nil
}

// resolveRollupManager dials L1 and reads RollupManager() from the aggchainbase contract at
// SovereignRollupAddr, returning its hex address.
func resolveRollupManager(ctx context.Context, ec *exitcertificate.Config) (string, error) {
	if ec.L1RPCURL == "" {
		return "", fmt.Errorf("cannot resolve RollupManager: l1RpcUrl is not set")
	}
	if (ec.SovereignRollupAddr == common.Address{}) {
		return "", fmt.Errorf("cannot resolve RollupManager: sovereignRollupAddr is not set")
	}

	l1Client, err := ethclient.DialContext(ctx, ec.L1RPCURL)
	if err != nil {
		return "", fmt.Errorf("dialing L1 RPC %q: %w", ec.L1RPCURL, err)
	}
	defer l1Client.Close()

	caller, err := aggchainbase.NewAggchainbaseCaller(ec.SovereignRollupAddr, l1Client)
	if err != nil {
		return "", fmt.Errorf("creating aggchainbase caller (addr=%s): %w", ec.SovereignRollupAddr.Hex(), err)
	}

	rollupManager, err := caller.RollupManager(&bind.CallOpts{Context: ctx})
	if err != nil {
		return "", fmt.Errorf("querying RollupManager() on %s: %w", ec.SovereignRollupAddr.Hex(), err)
	}
	return rollupManager.Hex(), nil
}
