package backward_forward_let

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/agglayer"
	"github.com/agglayer/aggkit/aggsender/rpcclient"
	bridgeservice "github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

const (
	dialTimeout     = 10 * time.Second
	recoveryTimeout = 10 * time.Minute
)

// Env holds all connections and contract bindings needed by the backward/forward LET tool.
type Env struct {
	// L2Client is the L2 Ethereum RPC client.
	L2Client *ethclient.Client

	// BridgeService is the aggkit bridge service REST client.
	BridgeService *bridgeservice.Client

	// AgglayerClient is the gRPC client for the AggLayer node.
	AgglayerClient agglayer.AgglayerClientInterface

	// AggsenderRPC is the JSON-RPC client for the running aggsender process.
	AggsenderRPC *rpcclient.Client

	// L2Bridge is the bound L2 bridge contract.
	L2Bridge *agglayerbridgel2.Agglayerbridgel2

	// L2NetworkID is the network ID of the L2 chain.
	L2NetworkID uint32

	// Config holds the loaded configuration.
	Config *Config
}

// Close closes the L2 RPC connection.
func (e *Env) Close() error {
	if e == nil {
		return nil
	}
	if e.L2Client != nil {
		e.L2Client.Close()
	}
	return nil
}

// SetupEnv dials L2, initialises contract bindings, bridge service, agglayer, and aggsender clients.
func SetupEnv(ctx context.Context, cfg *Config) (*Env, error) {
	if cfg.BackwardForwardLET.BridgeServiceURL == "" {
		return nil, fmt.Errorf("BackwardForwardLET.BridgeServiceURL is required")
	}

	bridgeSvc := bridgeservice.New(bridgeservice.Config{BaseURL: cfg.BackwardForwardLET.BridgeServiceURL})
	if _, err := bridgeSvc.HealthCheck(ctx); err != nil {
		return nil, fmt.Errorf("bridge service health check at %s: %w",
			cfg.BackwardForwardLET.BridgeServiceURL, err)
	}

	l2Client, err := ethclient.DialContext(ctx, cfg.Common.L2RPC.URL)
	if err != nil {
		return nil, fmt.Errorf("connect to L2: %w", err)
	}

	agglayerClient, err := agglayer.NewAgglayerClient(cfg.AgglayerClient,
		log.GetDefaultLogger())
	if err != nil {
		l2Client.Close()
		return nil, fmt.Errorf("create agglayer client: %w", err)
	}

	aggsenderRPC := rpcclient.NewClient(cfg.BackwardForwardLET.AggsenderRPCURL)

	l2Bridge, err := agglayerbridgel2.NewAgglayerbridgel2(cfg.BridgeL2Sync.BridgeAddr, l2Client)
	if err != nil {
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 bridge binding: %w", err)
	}

	return &Env{
		L2Client:       l2Client,
		BridgeService:  bridgeSvc,
		AgglayerClient: agglayerClient,
		AggsenderRPC:   aggsenderRPC,
		L2Bridge:       l2Bridge,
		L2NetworkID:    cfg.BackwardForwardLET.L2NetworkID,
		Config:         cfg,
	}, nil
}

// Run is the main entry point for the backward/forward LET CLI.
func Run(c *cli.Context) error {
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

	diagnosis, err := Diagnose(c.Context, env)
	if err != nil {
		return fmt.Errorf("diagnosis failed: %w", err)
	}

	PrintDiagnosis(os.Stdout, diagnosis)

	if diagnosis.Case == NoDivergence {
		fmt.Println("Nothing to do: L1 settled state and L2 on-chain state are in sync.")
		return nil
	}

	if diagnosis.AggsenderAPIFailed {
		fmt.Printf("\nAggsender RPC was unreachable. Cannot proceed with recovery.\n")
		fmt.Printf("Contact your AggLayer admin with the failed certificate details above.\n")
		return nil
	}

	if !c.Bool("yes") {
		fmt.Print("\nProceed with recovery? [y/N] ")
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

	if err := ExecuteRecovery(recoveryCtx, env, diagnosis); err != nil {
		return fmt.Errorf("recovery failed: %w", err)
	}

	fmt.Println("\nRecovery completed successfully.")
	return nil
}
