package remove_ger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/db"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/urfave/cli/v2"
)

const (
	gerHexPrefix    = "0x"
	gerHexLen       = 64
	dialTimeout     = 10 * time.Second
	recoveryTimeout = 10 * time.Minute
)

// Env holds all connections and contract bindings needed by the remove-ger tool.
// Pass it to diagnosis and recovery methods in later chunks.
type Env struct {
	// SQLite DBs for L1InfoTreeSync, BridgeL1Sync, BridgeL2Sync
	SQLite *SQLiteConnections

	// RPC clients
	L1 *ethclient.Client
	L2 *ethclient.Client

	// Bridge service REST client (nil if not configured)
	BridgeService *client.Client

	// L1 contract bindings
	L1GERManager *agglayerger.Agglayerger

	// L2 contract bindings
	L2Bridge     *agglayerbridgel2.Agglayerbridgel2
	L2GERManager *agglayergerl2.Agglayergerl2
}

// Close closes all connections (SQLite and RPC clients). BridgeService has no Close.
func (e *Env) Close() error {
	if e == nil {
		return nil
	}
	if e.SQLite != nil {
		_ = e.SQLite.Close()
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

	ctx, cancel := context.WithTimeout(c.Context, dialTimeout)
	defer cancel()

	env, err := SetupEnv(ctx, cfg)
	if err != nil {
		return err
	}
	defer env.Close()

	diagnosis, err := Diagnose(ctx, env, gerHash, c.Bool("force"))
	if err != nil {
		return err
	}

	PrintDiagnosis(diagnosis)

	if !diagnosis.GERExistsOnL2 {
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

	if err := ExecuteRecovery(recoveryCtx, cfg, env, diagnosis); err != nil {
		return fmt.Errorf("recovery failed: %w (bridge may remain in emergency state)", err)
	}

	fmt.Println()
	fmt.Println("Recovery completed. GER removed, bridge not in emergency state.")
	return nil
}

// SetupEnv loads SQLite connections, dials L1/L2, initializes contract bindings and bridge service client.
// Exported for use by E2E tests that invoke the tool programmatically.
// If optionalBridgeL2 is non-nil, it is used as the BridgeL2 connection (caller owns it; Env.Close will not close it).
// This allows tests to use the same DB connection for waiting on claims and for diagnosis, avoiding query/visibility divergence.
func SetupEnv(ctx context.Context, cfg *Config, optionalBridgeL2 ...*sql.DB) (*Env, error) {
	var bridgeL2 *sql.DB
	owned := true
	if len(optionalBridgeL2) > 0 && optionalBridgeL2[0] != nil {
		bridgeL2 = optionalBridgeL2[0]
		owned = false
	}
	sqliteConns, err := loadSQLiteConnections(ctx, cfg, bridgeL2, owned)
	if err != nil {
		return nil, err
	}

	l1Client, err := ethclient.DialContext(ctx, cfg.L1NetworkConfig.RPC.URL)
	if err != nil {
		_ = sqliteConns.Close()
		return nil, fmt.Errorf("connect to L1: %w", err)
	}

	l2Client, err := ethclient.DialContext(ctx, cfg.Common.L2RPC.URL)
	if err != nil {
		_ = sqliteConns.Close()
		l1Client.Close()
		return nil, fmt.Errorf("connect to L2: %w", err)
	}

	l2Bridge, err := agglayerbridgel2.NewAgglayerbridgel2(cfg.BridgeL2Sync.BridgeAddr, l2Client)
	if err != nil {
		_ = sqliteConns.Close()
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 bridge binding: %w", err)
	}

	l2GER, err := agglayergerl2.NewAgglayergerl2(cfg.L2GERSync.GlobalExitRootL2Addr, l2Client)
	if err != nil {
		_ = sqliteConns.Close()
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L2 GER manager binding: %w", err)
	}

	l1GER, err := agglayerger.NewAgglayerger(cfg.L2GERSync.GlobalExitRootL1Addr, l1Client)
	if err != nil {
		_ = sqliteConns.Close()
		l1Client.Close()
		l2Client.Close()
		return nil, fmt.Errorf("initialize L1 GER manager binding: %w", err)
	}

	var bridgeSvc *client.Client
	if cfg.RemoveGER.BridgeServiceURL != "" {
		bridgeSvc = client.New(client.Config{BaseURL: cfg.RemoveGER.BridgeServiceURL})
		if _, err := bridgeSvc.HealthCheck(ctx); err != nil {
			_ = sqliteConns.Close()
			l1Client.Close()
			l2Client.Close()
			return nil, fmt.Errorf("bridge service health check at %s: %w", cfg.RemoveGER.BridgeServiceURL, err)
		}
	}

	return &Env{
		SQLite:        sqliteConns,
		L1:            l1Client,
		L2:            l2Client,
		BridgeService: bridgeSvc,
		L1GERManager:  l1GER,
		L2Bridge:      l2Bridge,
		L2GERManager:  l2GER,
	}, nil
}

// SQLiteConnections holds the open SQLite DB connections for L1InfoTreeSync, BridgeL1Sync,
// and BridgeL2Sync. Used by diagnosis and recovery in later chunks.
// When bridgeL2Owned is false, Close() does not close BridgeL2 (caller owns it).
type SQLiteConnections struct {
	L1InfoTree    *sql.DB // L1InfoTreeSync DB
	BridgeL1      *sql.DB // BridgeL1Sync DB
	BridgeL2      *sql.DB // BridgeL2Sync DB
	bridgeL2Owned bool    // if false, caller owns BridgeL2 and Close() must not close it
}

// Close closes DB connections that the Env owns. BridgeL2 is only closed when bridgeL2Owned is true.
func (c *SQLiteConnections) Close() error {
	var errs []error
	if c.L1InfoTree != nil {
		if err := c.L1InfoTree.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.BridgeL1 != nil {
		if err := c.BridgeL1.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if c.bridgeL2Owned && c.BridgeL2 != nil {
		if err := c.BridgeL2.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// loadSQLiteConnections opens SQLite DBs for L1InfoTreeSync, BridgeL1Sync, and BridgeL2Sync,
// verifies each with Ping, and returns the connections for use in later chunks. Caller must call Close.
// If useBridgeL2 is non-nil it is used as BridgeL2 and owned is false; otherwise BridgeL2 is opened and owned.
func loadSQLiteConnections(ctx context.Context, cfg *Config, useBridgeL2 *sql.DB, bridgeL2Owned bool) (*SQLiteConnections, error) {
	l1Info, err := db.NewSQLiteDB(cfg.L1InfoTreeSync.DBPath)
	if err != nil {
		return nil, fmt.Errorf("l1infotreesync DB open: %w", err)
	}
	if err := l1Info.PingContext(ctx); err != nil {
		_ = l1Info.Close()
		return nil, fmt.Errorf("l1infotreesync DB ping: %w", err)
	}

	bridgeL1, err := db.NewSQLiteDB(cfg.BridgeL1Sync.DBPath)
	if err != nil {
		_ = l1Info.Close()
		return nil, fmt.Errorf("bridge L1 sync DB open: %w", err)
	}
	if err := bridgeL1.PingContext(ctx); err != nil {
		_ = l1Info.Close()
		_ = bridgeL1.Close()
		return nil, fmt.Errorf("bridge L1 sync DB ping: %w", err)
	}

	var bridgeL2 *sql.DB
	if useBridgeL2 != nil {
		bridgeL2 = useBridgeL2
		if err := bridgeL2.PingContext(ctx); err != nil {
			_ = l1Info.Close()
			_ = bridgeL1.Close()
			return nil, fmt.Errorf("bridge L2 sync DB ping (provided connection): %w", err)
		}
	} else {
		var err error
		bridgeL2, err = db.NewSQLiteDB(cfg.BridgeL2Sync.DBPath)
		if err != nil {
			_ = l1Info.Close()
			_ = bridgeL1.Close()
			return nil, fmt.Errorf("bridge L2 sync DB open: %w", err)
		}
		if err := bridgeL2.PingContext(ctx); err != nil {
			_ = l1Info.Close()
			_ = bridgeL1.Close()
			_ = bridgeL2.Close()
			return nil, fmt.Errorf("bridge L2 sync DB ping: %w", err)
		}
		bridgeL2Owned = true
	}

	return &SQLiteConnections{
		L1InfoTree:    l1Info,
		BridgeL1:      bridgeL1,
		BridgeL2:      bridgeL2,
		bridgeL2Owned: bridgeL2Owned,
	}, nil
}

// parseGER validates and returns the GER as common.Hash. GER must be 0x-prefixed 32-byte hex.
func parseGER(s string) (common.Hash, error) {
	s = strings.TrimPrefix(s, gerHexPrefix)
	if len(s) != gerHexLen {
		return common.Hash{}, fmt.Errorf("invalid GER: want %s followed by %d hex chars, got %d chars", gerHexPrefix, gerHexLen, len(s))
	}
	for _, c := range s {
		if (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F') {
			continue
		}
		return common.Hash{}, fmt.Errorf("invalid GER: not hex")
	}
	return common.HexToHash(gerHexPrefix + s), nil
}
