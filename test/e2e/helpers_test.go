package e2e

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	_ "github.com/mattn/go-sqlite3" // registers the sqlite3 driver used to read the aggsender DB read-only
	"github.com/stretchr/testify/require"
)

// certStatusSettled is the integer value of the "Settled" certificate status as stored in the
// aggsender certificate_info.status column (see agglayer/types: Pending=0, Proven=1, Candidate=2,
// InError=3, Settled=4).
const certStatusSettled = 4

// assertNetworkHealthy asserts that the shared env is operational: it runs the same connectivity
// and contract sanity checks used pre-suite (env.CheckEnv) and additionally probes the bridge
// service health endpoint. Migrated mutating tests should call this in a defer (or at the end) so a
// leaked state is detected close to its origin rather than only in the TestMain post-suite check.
func assertNetworkHealthy(ctx context.Context, t *testing.T, env *envs.Env) {
	t.Helper()
	require.NotNil(t, env, "env must not be nil")
	require.NoError(t, env.CheckEnv(ctx), "network must be healthy (env.CheckEnv)")

	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := env.Clients.BridgeService.HealthCheck(healthCtx)
	require.NoError(t, err, "bridge service health check")
	require.NotNil(t, resp, "bridge service health response must not be nil")
}

// mintAndApproveERC20OnL2 mints the given amount of the env MintableERC20 to opts.From and approves
// the L2 bridge to spend it. L2-native tokens are used because they bypass the Local Balance Tree
// underflow check in the L2 bridge contract (mirrors the TestMain post-suite check setup).
func mintAndApproveERC20OnL2(ctx context.Context, t *testing.T, env *envs.Env, opts *bind.TransactOpts, amount *big.Int) {
	t.Helper()
	mintTx, err := env.L2.Contracts.MintableERC20.Mint(opts, opts.From, amount)
	require.NoError(t, err, "mint ERC20 on L2")
	mintReceipt, err := bind.WaitMined(ctx, env.Clients.L2, mintTx)
	require.NoError(t, err, "wait for ERC20 mint tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, mintReceipt.Status, "ERC20 mint tx failed")

	approveTx, err := env.L2.Contracts.MintableERC20.Approve(opts, env.L2.Contracts.L2BridgeAddress, amount)
	require.NoError(t, err, "approve ERC20 for L2 bridge")
	approveReceipt, err := bind.WaitMined(ctx, env.Clients.L2, approveTx)
	require.NoError(t, err, "wait for ERC20 approve tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, approveReceipt.Status, "ERC20 approve tx failed")
}

// bridgeERC20L2ToL1AndClaim performs a full L2->L1 bridge-and-claim of the env L2-native
// MintableERC20: it mints+approves the token on L2 then runs BridgeL2ToL1 (which bridges and claims
// on L1). It builds on the existing bridge_utils.go helpers and the env MintableERC20 binding. The
// caller supplies transactors (typically checked out from the key pools and returned afterwards).
func bridgeERC20L2ToL1AndClaim(ctx context.Context, t *testing.T, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts, amount *big.Int) {
	t.Helper()
	mintAndApproveERC20OnL2(ctx, t, env, l2Opts, amount)
	require.NoError(t, BridgeL2ToL1(ctx, env, l1Opts, l2Opts, env.L2.Contracts.MintableERC20Address),
		"L2->L1 ERC20 bridge+claim")
}

// bridgeETHL1ToL2AndClaim performs a full L1->L2 ETH bridge-and-claim using BridgeL1ToL2WithResult
// and returns the detailed bridgeResult. It is a thin convenience wrapper so later steps do not have
// to repeat the transactor wiring; the caller supplies transactors (typically pooled).
func bridgeETHL1ToL2AndClaim(ctx context.Context, t *testing.T, env *envs.Env, l1Opts, l2Opts *bind.TransactOpts, amount *big.Int) *bridgeResult {
	t.Helper()
	result, err := BridgeL1ToL2WithResult(ctx, env, l1Opts, l2Opts, amount)
	require.NoError(t, err, "L1->L2 ETH bridge+claim")
	require.NotNil(t, result, "bridge result must not be nil")
	return result
}

// bridgeMessageL1ToL2AndClaim performs a message (LeafType=1) bridge from L1 to L2 and claims it on
// L2, returning the bridge-service record and global index. It mirrors the asset-bridge flow in
// bridge_utils.go but uses BridgeMessage / ClaimMessage so message-oriented migrations (e.g. the
// "Transfer message" cases in bridge-e2e.bats) can reuse it. destination is the L2 recipient and
// metadata is the message payload (may be empty).
func bridgeMessageL1ToL2AndClaim(
	ctx context.Context,
	t *testing.T,
	env *envs.Env,
	l1Opts, l2Opts *bind.TransactOpts,
	destination common.Address,
	amount *big.Int,
	metadata []byte,
) *bridgeResult {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")

	l1Opts.Value = amount
	defer func() { l1Opts.Value = nil }()
	tx, err := env.L1.Contracts.Bridge.BridgeMessage(l1Opts, l2NetworkID, destination, true, metadata)
	require.NoError(t, err, "BridgeMessage on L1")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait for BridgeMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "BridgeMessage tx failed")

	bridge := waitForBridgeByTxHash(ctx, t, env, 0, tx.Hash())
	depositCount := bridge.DepositCount
	l1InfoTreeIndex := waitForL1InfoTreeIndex(ctx, t, env, 0, depositCount)
	waitForInjectedL1InfoLeaf(ctx, t, env, l2NetworkID, l1InfoTreeIndex)

	claimProof, err := env.Clients.BridgeService.GetClaimProof(ctx, 0, l1InfoTreeIndex, depositCount)
	require.NoError(t, err, "get claim proof")
	require.NotNil(t, claimProof, "claim proof must not be nil")
	proofLocal, proofRollup := claimProofToContractProofs(claimProof)

	claimTx, err := env.L2.Contracts.L2Bridge.ClaimMessage(
		l2Opts, proofLocal, proofRollup, bridge.GlobalIndex,
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.MainnetExitRoot)),
		common.HexToHash(string(claimProof.L1InfoTreeLeaf.RollupExitRoot)),
		bridge.OriginNetwork, common.HexToAddress(string(bridge.OriginAddress)),
		bridge.DestinationNetwork, destination, amount, common.FromHex(bridge.Metadata),
	)
	require.NoError(t, err, "ClaimMessage on L2")
	claimReceipt, err := bind.WaitMined(ctx, env.Clients.L2, claimTx)
	require.NoError(t, err, "wait for ClaimMessage tx")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, claimReceipt.Status, "ClaimMessage tx failed")

	return &bridgeResult{
		Bridge:          bridge,
		DepositCount:    depositCount,
		L1InfoTreeIndex: l1InfoTreeIndex,
		ClaimTxHash:     claimTx.Hash(),
		GlobalIndex:     bridge.GlobalIndex,
		DestinationAddr: destination,
		BridgeAmount:    amount,
	}
}

// waitForBridgeByTxHash polls the bridge service until the bridge with the given originating tx hash
// on the given source networkID is indexed, returning the bridge-service record.
func waitForBridgeByTxHash(ctx context.Context, t *testing.T, env *envs.Env, networkID uint32, txHash common.Hash) *types.BridgeResponse {
	t.Helper()
	var found *types.BridgeResponse
	pollCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	err := pollWithBackoff(pollCtx, 2*time.Minute, backoffInitial, backoffMax, "bridge in bridge service", func() (bool, error) {
		pageSize := uint32(100)
		res, err := env.Clients.BridgeService.GetBridges(pollCtx, client.GetBridgesParams{NetworkID: networkID, PageSize: &pageSize})
		if err != nil {
			return false, nil //nolint:nilerr // transient; keep polling until timeout
		}
		if res == nil {
			return false, nil
		}
		for _, b := range res.Bridges {
			if string(b.TxHash) == txHash.Hex() {
				found = b
				return true, nil
			}
		}
		return false, nil
	})
	require.NoError(t, err, "wait for bridge %s in bridge service", txHash.Hex())
	require.NotNil(t, found, "bridge not found in bridge service")
	return found
}

// waitForL1InfoTreeIndex polls the bridge service until the deposit on the given source networkID is
// included in the L1 Info Tree, returning the resulting index.
func waitForL1InfoTreeIndex(ctx context.Context, t *testing.T, env *envs.Env, networkID, depositCount uint32) uint32 {
	t.Helper()
	var idx uint32
	pollCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	err := pollWithBackoff(pollCtx, 5*time.Minute, backoffInitial, backoffMax, "L1 info tree index", func() (bool, error) {
		i, err := env.Clients.BridgeService.GetL1InfoTreeIndex(pollCtx, int(networkID), int(depositCount))
		if err != nil {
			return false, nil //nolint:nilerr // transient; keep polling until timeout
		}
		idx = i
		return idx != 0, nil
	})
	require.NoError(t, err, "wait for L1 info tree index (networkID=%d depositCount=%d)", networkID, depositCount)
	require.NotZero(t, idx, "L1 info tree index must be non-zero")
	return idx
}

// waitForInjectedL1InfoLeaf polls the bridge service until the L1 Info Tree leaf at l1InfoTreeIndex
// has been injected on the destination L2 network.
func waitForInjectedL1InfoLeaf(ctx context.Context, t *testing.T, env *envs.Env, l2NetworkID, l1InfoTreeIndex uint32) {
	t.Helper()
	pollCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	err := pollWithBackoff(pollCtx, 10*time.Minute, backoffInitial, backoffMax, "injected L1 info leaf", func() (bool, error) {
		leaf, err := env.Clients.BridgeService.GetInjectedL1InfoLeaf(pollCtx, int(l2NetworkID), int(l1InfoTreeIndex))
		if err != nil {
			return false, nil //nolint:nilerr // transient; keep polling until timeout
		}
		return leaf != nil, nil
	})
	require.NoError(t, err, "wait for injected L1 info leaf (l2NetworkID=%d index=%d)", l2NetworkID, l1InfoTreeIndex)
}

// claimProofToContractProofs converts a bridge-service ClaimProof's hex proof slices into the
// [32][32]byte fixed-size arrays expected by the L2 bridge ClaimAsset/ClaimMessage bindings.
func claimProofToContractProofs(claimProof *types.ClaimProof) (proofLocal, proofRollup [32][32]byte) {
	for i, p := range claimProof.ProofLocalExitRoot {
		if i >= 32 {
			break
		}
		proofLocal[i] = common.HexToHash(string(p))
	}
	for i, p := range claimProof.ProofRollupExitRoot {
		if i >= 32 {
			break
		}
		proofRollup[i] = common.HexToHash(string(p))
	}
	return proofLocal, proofRollup
}

// withCleanEmergencyState records whether the L2 bridge is in emergency state, runs fn, and on
// return restores the original emergency state (best-effort): if the bridge was NOT in emergency
// state before fn but is afterwards, it deactivates it using the SovereignAdmin key. This
// generalizes the inline defer blocks duplicated across the testRemoveGER_* functions so mutating
// tests leave the shared env healthy for later tests and the post-suite check.
func withCleanEmergencyState(ctx context.Context, t *testing.T, env *envs.Env, fn func()) {
	t.Helper()
	wasEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: ctx})
	require.NoError(t, err, "read initial emergency state")

	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(&bind.CallOpts{Context: restoreCtx})
		if err != nil {
			log.Warnf("[withCleanEmergencyState] could not read emergency state during restore: %v", err)
			return
		}
		// Only restore if fn put the bridge into emergency state that it was not in before.
		if !isEmergency || wasEmergency {
			return
		}
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		if err != nil {
			log.Warnf("[withCleanEmergencyState] could not build sovereign admin transactor: %v", err)
			return
		}
		if _, err := env.L2.Contracts.L2Bridge.DeactivateEmergencyState(opts); err != nil {
			log.Warnf("[withCleanEmergencyState] best-effort DeactivateEmergencyState failed: %v", err)
		}
	}()

	fn()
}

// openAggsenderDBReadOnly opens the aggsender SQLite database read-only via the host bind-mount path
// (env.GetAggsenderDBPath()). It is read-only (mode=ro, immutable) so it never interferes with the
// running aggkit container's writer. The caller must Close the returned *sql.DB.
func openAggsenderDBReadOnly(t *testing.T, env *envs.Env) *sql.DB {
	t.Helper()
	dbPath := env.GetAggsenderDBPath()
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?mode=ro&immutable=1&_busy_timeout=5000", dbPath))
	require.NoError(t, err, "open aggsender db read-only: %s", dbPath)
	return db
}

// querySettledCertHeight returns the highest height of a settled certificate (status == Settled) in
// the aggsender certificate_info table, and whether any settled certificate exists. A missing table
// (e.g. aggsender not yet initialized) is treated as "no settled cert" rather than an error so the
// waiter can keep polling.
func querySettledCertHeight(ctx context.Context, db *sql.DB) (height uint64, found bool, err error) {
	row := db.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(height), 0), COUNT(*) FROM certificate_info WHERE status = ?", certStatusSettled)
	var count uint64
	if err := row.Scan(&height, &count); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, nil
		}
		// Treat "no such table" as not-yet-ready so the caller keeps polling.
		return 0, false, nil //nolint:nilerr // db not yet initialized; keep polling
	}
	return height, count > 0, nil
}

// waitForSettledCertificate waits until the aggsender has settled at least one certificate, polling
// the aggsender SQLite database (read-only) until a row with status==Settled exists or the timeout
// elapses. It returns the highest settled certificate height. This is the cert-settlement waiter
// reused by the certificate-settlement (P2) and trigger-cert-mode (P9) migrations. It reads the DB
// directly (pure-Go) via env.GetAggsenderDBPath() rather than shelling out.
func waitForSettledCertificate(ctx context.Context, t *testing.T, env *envs.Env, timeout time.Duration) uint64 {
	t.Helper()
	db := openAggsenderDBReadOnly(t, env)
	defer db.Close()

	var settledHeight uint64
	pollCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := pollWithBackoff(pollCtx, timeout, backoffInitial, backoffMax, "settled certificate", func() (bool, error) {
		h, found, err := querySettledCertHeight(pollCtx, db)
		if err != nil {
			return false, err
		}
		if found {
			settledHeight = h
		}
		return found, nil
	})
	require.NoError(t, err, "wait for settled certificate within %s", timeout)
	log.Infof("[waitForSettledCertificate] settled certificate found at height %d", settledHeight)
	return settledHeight
}

// TestHelpersCompile keeps the P1 helpers referenced so the linter's unused check passes without
// adding any behavioral test. It skips immediately (no env interaction) and exists only so each new
// helper has at least one caller until the P2-P10 migrations reference them directly. Remove the
// individual references here as real tests adopt the helpers.
func TestHelpersCompile(t *testing.T) {
	t.Skip("compile-only reference for P1 helpers; no behavior under test")

	// Unreachable: references below exist purely to satisfy the unused linter.
	ctx := context.Background()
	var env *envs.Env
	amount := big.NewInt(1)
	var l1Opts, l2Opts *bind.TransactOpts

	assertNetworkHealthy(ctx, t, env)
	mintAndApproveERC20OnL2(ctx, t, env, l2Opts, amount)
	bridgeERC20L2ToL1AndClaim(ctx, t, env, l1Opts, l2Opts, amount)
	_ = bridgeETHL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, amount)
	_ = bridgeMessageL1ToL2AndClaim(ctx, t, env, l1Opts, l2Opts, common.Address{}, amount, nil)
	withCleanEmergencyState(ctx, t, env, func() {})
	_ = waitForSettledCertificate(ctx, t, env, time.Minute)
}
