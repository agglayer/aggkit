package remove_ger

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

const (
	gerHexPrefix    = "0x"
	gerHexLen       = 64
	dialTimeout     = 10 * time.Second
	recoveryTimeout = 10 * time.Minute
)

type l2BridgeContract interface {
	IsEmergencyState(opts *bind.CallOpts) (bool, error)
	ActivateEmergencyState(opts *bind.TransactOpts) (*gethtypes.Transaction, error)
	DeactivateEmergencyState(opts *bind.TransactOpts) (*gethtypes.Transaction, error)
	UnsetMultipleClaims(opts *bind.TransactOpts, globalIndexes []*big.Int) (*gethtypes.Transaction, error)
	SetMultipleClaims(opts *bind.TransactOpts, globalIndexes []*big.Int) (*gethtypes.Transaction, error)
	ForceEmitDetailedClaimEvent(
		opts *bind.TransactOpts,
		claimData []agglayerbridgel2.AgglayerBridgeL2ClaimData,
	) (*gethtypes.Transaction, error)
	IsClaimed(opts *bind.CallOpts, index uint32, originNetwork uint32) (bool, error)
	ParseDetailedClaimEvent(log gethtypes.Log) (*agglayerbridgel2.Agglayerbridgel2DetailedClaimEvent, error)
	ParseClaimEvent(log gethtypes.Log) (*agglayerbridgel2.Agglayerbridgel2ClaimEvent, error)
}

// Env holds all connections and contract bindings needed by the remove-ger tool.
// Pass it to diagnosis and recovery methods in later chunks.
type Env struct {
	// RPC clients
	L1 *ethclient.Client
	L2 *ethclient.Client

	// Bridge service REST client (required)
	BridgeService *client.Client

	// L2NetworkID is the network ID of the L2 network served by the bridge service.
	L2NetworkID uint32

	// L1 contract bindings
	L1GERManager *agglayerger.Agglayerger

	// L2 contract bindings
	L2Bridge     l2BridgeContract
	L2GERManager *agglayergerl2.Agglayergerl2
	L2BridgeAddr common.Address

	waitReceiptFn func(ctx context.Context, tx *gethtypes.Transaction) (*gethtypes.Receipt, error)
}

// Close closes all RPC connections. BridgeService has no Close.
func (e *Env) Close() error {
	if e == nil {
		return nil
	}
	if e.L1 != nil {
		e.L1.Close()
	}
	if e.L2 != nil {
		e.L2.Close()
	}
	return nil
}

// Run is the main entry point for the remove-ger CLI.
func Run(c *cli.Context) error {
	gerStr := c.String("ger")
	if gerStr == "" {
		return fmt.Errorf("missing required flag: --ger")
	}
	gerHash, err := parseGER(gerStr)
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(c)
	if err != nil {
		return err
	}

	dialCtx, dialCancel := context.WithTimeout(c.Context, dialTimeout)
	env, err := SetupEnv(dialCtx, cfg)
	dialCancel()
	if err != nil {
		return err
	}
	defer env.Close()

	diagnosis, err := Diagnose(c.Context, env, gerHash, c.Bool("force"))
	if err != nil {
		return err
	}

	PrintDiagnosis(diagnosis)

	if !diagnosis.hasRecoveryActions() {
		fmt.Println("Nothing to do (GER is not on L2).")
		return nil
	}

	if !c.Bool("yes") {
		fmt.Print("Proceed? [y/N] ")
		var answer string
		_, _ = fmt.Scanln(&answer)
		answer = strings.TrimSpace(strings.ToLower(answer))
		if answer != "y" && answer != "yes" {
			fmt.Println("Aborted.")
			return nil
		}
	}

	recoveryCtx, recoveryCancel := context.WithTimeout(c.Context, recoveryTimeout)
	defer recoveryCancel()

	if _, err := ExecuteRecovery(recoveryCtx, cfg, env, diagnosis); err != nil {
		return fmt.Errorf("recovery failed: %w (bridge may remain in emergency state)", err)
	}

	fmt.Println()
	fmt.Println("Recovery completed. GER removed, bridge not in emergency state.")
	return nil
}

// SetupEnv dials L1/L2, initializes contract bindings and bridge service client.
// BridgeServiceURL in cfg.RemoveGER is required.
// Exported for use by E2E tests that invoke the tool programmatically.
func SetupEnv(ctx context.Context, cfg *Config) (*Env, error) {
	if cfg.RemoveGER.BridgeServiceURL == "" {
		return nil, fmt.Errorf("RemoveGER.BridgeServiceURL is required")
	}

	bridgeSvc := client.New(client.Config{BaseURL: cfg.RemoveGER.BridgeServiceURL})
	if _, err := bridgeSvc.HealthCheck(ctx); err != nil {
		return nil, fmt.Errorf("bridge service health check at %s: %w", cfg.RemoveGER.BridgeServiceURL, err)
	}

	l1Client, err := ethclient.DialContext(ctx, cfg.L1NetworkConfig.RPC.URL)
	if err != nil {
		return nil, fmt.Errorf("connect to L1: %w", err)
	}

	l2Client, err := ethclient.DialContext(ctx, cfg.Common.L2RPC.URL)
	if err != nil {
		l1Client.Close()
		return nil, fmt.Errorf("connect to L2: %w", err)
	}

	l2Bridge, err := agglayerbridgel2.NewAgglayerbridgel2(cfg.BridgeL2Sync.BridgeAddr, l2Client)
	if err != nil {
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 bridge binding: %w", err)
	}

	l2GER, err := agglayergerl2.NewAgglayergerl2(cfg.L2GERSync.GlobalExitRootL2Addr, l2Client)
	if err != nil {
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 GER manager binding: %w", err)
	}

	l1GER, err := agglayerger.NewAgglayerger(cfg.L2GERSync.GlobalExitRootL1Addr, l1Client)
	if err != nil {
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L1 GER manager binding: %w", err)
	}

	return &Env{
		L1:            l1Client,
		L2:            l2Client,
		BridgeService: bridgeSvc,
		L2NetworkID:   cfg.RemoveGER.L2NetworkID,
		L1GERManager:  l1GER,
		L2Bridge:      l2Bridge,
		L2GERManager:  l2GER,
		L2BridgeAddr:  cfg.BridgeL2Sync.BridgeAddr,
		waitReceiptFn: func(ctx context.Context, tx *gethtypes.Transaction) (*gethtypes.Receipt, error) {
			return waitForReceipt(ctx, l2Client, tx)
		},
	}, nil
}

// parseGER validates and returns the GER as common.Hash.
// GER must be a 0x-prefixed 32-byte hex string (66 characters total).
func parseGER(s string) (common.Hash, error) {
	if !strings.HasPrefix(s, gerHexPrefix) {
		return common.Hash{}, fmt.Errorf("invalid GER: must start with %s", gerHexPrefix)
	}
	hex := s[len(gerHexPrefix):]
	if len(hex) != gerHexLen {
		return common.Hash{}, fmt.Errorf("invalid GER: want %s followed by %d hex chars, got %d chars",
			gerHexPrefix, gerHexLen, len(hex))
	}
	for _, c := range hex {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return common.Hash{}, fmt.Errorf("invalid GER: not hex")
	}
	return common.HexToHash(s), nil
}
