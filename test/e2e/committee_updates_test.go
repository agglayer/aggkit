package e2e

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggchainbase"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// committeeUpdatesTestTimeout bounds the whole TestCommitteeUpdates run. The two subtests each wait
// for a fresh certificate to settle under a changed committee; cert cadence on op-pp is multi-minute
// (see certSettlementWaitTimeout), and we do this twice, so a generous budget is used while staying
// below the suite -timeout.
const committeeUpdatesTestTimeout = 28 * time.Minute

// committeeUpdatesHeightTimeout bounds the wait for the agglayer certificate height to strictly
// advance under a changed committee. It mirrors certSettlementWaitTimeout (15m) which proved
// sufficient for op-pp settlement, applied per add/remove subtest.
const committeeUpdatesHeightTimeout = 15 * time.Minute

// committeeUpdatesEnsureCertTimeout bounds the initial wait for the old committee to settle at least
// one certificate (faithful to the bats ensure_non_null_cert). It mirrors certSettlementWaitTimeout.
const committeeUpdatesEnsureCertTimeout = 15 * time.Minute

// committeeSovereignRollupAddr is the sovereign rollup contract on L1 that holds the aggchain
// multisig committee (signers + threshold). It matches polygonZkEVMAddress in the op-pp aggkit
// config and the rollup_address the legacy bats derived from combined.json. updateSignersAndThreshold
// on this contract is gated by OnlyAggchainManager, and on op-pp the aggchainManager is the sovereign
// admin key (env.Keys.SovereignAdmin), which is what the bats signs the update tx with.
const committeeSovereignRollupAddr = "0x414e9E227e4b589aF92200508aF5399576530E4e"

// committeeValidatorAddress is the address the test ADDS to the committee. It is the address of the
// keystore the on-demand validator container (aggsender-validator-004) actually signs with
// (config/validator-004/aggsendervalidator-4.keystore). The aggsender, when collecting the added
// member's signature, recovers the signature's address and requires it to equal the on-chain
// member's Addr (see aggsender/validator/remote_validator.go), so the ADDED address MUST equal the
// validator container's signing-key address for certificates to settle at the raised threshold.
//
// This deviates from the legacy bats, which added the sovereign-admin address while running a
// validator container that signs with this validator keystore (a mismatch). We instead add the
// validator keystore's own address so the added signer and the container's signing identity are the
// same, which is what makes threshold=2 satisfiable additively without stalling the shared env.
const committeeValidatorAddress = "0x77A21F79994876973BeF5bbcbbd617a5B32B2f57"

// committeeValidatorURL is the on-chain Url stored for the added committee member. It must resolve to
// the validator container on the op-pp_default compose network: the aggsender trims the http:// prefix
// and dials aggkit-001-aggsender-validator-004:5578 (the container hostname/port). This is identical
// to the URL the legacy bats used.
const committeeValidatorURL = "http://aggkit-001-aggsender-validator-004:5578"

// TestCommitteeUpdates ports the two cases from the legacy
// e2e/tests/aggkit/aggsender-committee-updates.bats: "Add single validator to committee" and
// "Remove single validator from committee". It exercises the aggchain multisig committee on the
// shared sovereign rollup contract by (1) adding a second committee signer and raising the threshold,
// starting an on-demand validator container that signs as that member, and asserting the agglayer
// certificate height strictly advances under the 2-of-2 committee; then (2) removing that signer and
// lowering the threshold and asserting the height advances again under the restored single-signer
// committee.
//
// It is faithful to the bats flow (updateSignersAndThreshold add/remove, verify_is_in_signers_list,
// verify_threshold_updated, check_height_increase) but does everything in pure Go via the
// aggchainbase bindings and the agglayer read RPC, per the repo's prefer-Go-over-cast rule. Both the
// on-chain committee state and the extra container are fully restored/removed in cleanup so the
// shared op-pp env is left exactly as it was for the other tests and the post-suite health check.
func TestCommitteeUpdates(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}

	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	ctx, cancel := context.WithTimeout(context.Background(), committeeUpdatesTestTimeout)
	defer cancel()

	readRPCURL := agglayerReadRPCURL(t, env)
	rollupAddr := common.HexToAddress(committeeSovereignRollupAddr)

	// Read-only binding for committee queries (threshold + signers).
	committeeCaller, err := aggchainbase.NewAggchainbaseCaller(rollupAddr, env.Clients.L1)
	require.NoError(t, err, "create aggchainbase caller")

	// Transactor binding signed by the sovereign admin (== on-chain aggchainManager), which is the
	// only identity authorized to call updateSignersAndThreshold.
	adminAuth, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L1.ChainID)
	require.NoError(t, err, "create sovereign admin transactor")
	committeeTransactor, err := aggchainbase.NewAggchainbaseTransactor(rollupAddr, env.Clients.L1)
	require.NoError(t, err, "create aggchainbase transactor")

	addedSigner := common.HexToAddress(committeeValidatorAddress)

	// Snapshot the original committee so cleanup can restore it byte-for-byte regardless of which
	// subtests ran or failed.
	originalThreshold, err := committeeCaller.GetThreshold(&bind.CallOpts{Context: ctx})
	require.NoError(t, err, "read original threshold")
	originalSigners, err := committeeCaller.GetAggchainSignerInfos(&bind.CallOpts{Context: ctx})
	require.NoError(t, err, "read original signers")
	log.Infof("[TestCommitteeUpdates] original committee: threshold=%s signers=%d",
		originalThreshold.String(), len(originalSigners))

	// Cleanup ALWAYS runs: stop+remove the validator container and restore the original committee
	// (remove the added signer if still present and reset the threshold). Mirrors the bats
	// teardown_file plus the shared-env restore obligation.
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cleanupCancel()

		if err := env.StopAggsenderValidator(cleanupCtx); err != nil {
			t.Errorf("cleanup: stop aggsender validator: %v", err)
		}

		restoreCommittee(cleanupCtx, t, env, committeeCaller, committeeTransactor, adminAuth,
			addedSigner, originalThreshold)
	})

	t.Run("Add single validator to committee", func(t *testing.T) {
		// 1. Wait until the old committee settles at least one certificate (bats ensure_non_null_cert).
		baselineHeight := waitForAgglayerSettledCertificate(ctx, t, readRPCURL, env.L2.NetworkID,
			certSettlementTarget, committeeUpdatesEnsureCertTimeout)
		log.Infof("[Add] old committee settled at baseline height %d", baselineHeight)

		// 2. Read current threshold and increment by 1.
		current, err := committeeCaller.GetThreshold(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read current threshold")
		newThreshold := new(big.Int).Add(current, big.NewInt(1))

		// 3. Add the validator as a new committee member and raise the threshold.
		signersToAdd := []aggchainbase.IAggchainSignersSignerInfo{
			{Addr: addedSigner, Url: committeeValidatorURL},
		}
		updateSignersAndThreshold(ctx, t, env, committeeTransactor, adminAuth, nil, signersToAdd, newThreshold)

		// 4. Assert the new signer appears and the threshold updated (bats verify_is_in_signers_list +
		//    verify_threshold_updated).
		signers, err := committeeCaller.GetAggchainSignerInfos(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read signers after add")
		require.True(t, signersContain(signers, addedSigner),
			"added signer %s must be in the committee signers list", addedSigner)
		gotThreshold, err := committeeCaller.GetThreshold(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read threshold after add")
		require.Equal(t, newThreshold.String(), gotThreshold.String(), "threshold must be incremented")

		// 5. Start the validator container on demand. It joins op-pp_default at the hostname used in
		//    the added member Url and signs as the added member.
		require.NoError(t, env.StartAggsenderValidator(ctx), "start aggsender validator container")

		// 6. Assert the certificate height strictly advances under the 2-of-2 committee (bats
		//    check_height_increase). A height beyond the pre-update baseline can only be produced by
		//    the new committee, since the threshold now requires the added validator's signature too.
		newHeight := waitForCertificateHeightAbove(ctx, t, readRPCURL, env.L2.NetworkID,
			baselineHeight, committeeUpdatesHeightTimeout)
		log.Infof("[Add] new committee advanced height %d -> %d", baselineHeight, newHeight)
	})

	t.Run("Remove single validator from committee", func(t *testing.T) {
		// Record a baseline height to assert advancement after the removal.
		baselineHeight := waitForAgglayerSettledCertificate(ctx, t, readRPCURL, env.L2.NetworkID,
			certSettlementTarget, committeeUpdatesEnsureCertTimeout)

		// 1. Read current threshold and decrement by 1.
		current, err := committeeCaller.GetThreshold(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read current threshold")
		newThreshold := new(big.Int).Sub(current, big.NewInt(1))

		// 2. Find the added signer and its index in the on-chain signers list (bats counts the parens
		//    before the address; in Go we find the index of the matching Addr in the returned slice).
		signers, err := committeeCaller.GetAggchainSignerInfos(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read signers before remove")
		index := signerIndex(signers, addedSigner)
		require.GreaterOrEqual(t, index, 0, "added signer %s must be present before removal", addedSigner)

		// 3. Remove the signer and lower the threshold.
		signersToRemove := []aggchainbase.IAggchainSignersRemoveSignerInfo{
			{Addr: addedSigner, Index: big.NewInt(int64(index))},
		}
		updateSignersAndThreshold(ctx, t, env, committeeTransactor, adminAuth, signersToRemove, nil, newThreshold)

		// 4. Assert the signer is gone and the threshold updated.
		signers, err = committeeCaller.GetAggchainSignerInfos(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read signers after remove")
		require.False(t, signersContain(signers, addedSigner),
			"removed signer %s must NOT be in the committee signers list", addedSigner)
		gotThreshold, err := committeeCaller.GetThreshold(&bind.CallOpts{Context: ctx})
		require.NoError(t, err, "read threshold after remove")
		require.Equal(t, newThreshold.String(), gotThreshold.String(), "threshold must be decremented")

		// The added validator is no longer in the committee, so it is no longer needed for settlement.
		// Stop+remove it now (cleanup is still idempotent if this is skipped on failure).
		require.NoError(t, env.StopAggsenderValidator(ctx), "stop aggsender validator container")

		// 5. Assert the certificate height strictly advances again under the restored single-signer
		//    committee (bats check_height_increase).
		newHeight := waitForCertificateHeightAbove(ctx, t, readRPCURL, env.L2.NetworkID,
			baselineHeight, committeeUpdatesHeightTimeout)
		log.Infof("[Remove] restored committee advanced height %d -> %d", baselineHeight, newHeight)
	})
}

// updateSignersAndThreshold sends an updateSignersAndThreshold transaction signed by the sovereign
// admin and waits for it to be mined and succeed (faithful to the bats update_signers_and_threshold).
func updateSignersAndThreshold(
	ctx context.Context, t *testing.T, env *envs.Env,
	transactor *aggchainbase.AggchainbaseTransactor, auth *bind.TransactOpts,
	signersToRemove []aggchainbase.IAggchainSignersRemoveSignerInfo,
	signersToAdd []aggchainbase.IAggchainSignersSignerInfo,
	newThreshold *big.Int,
) {
	t.Helper()
	opts := *auth
	opts.Context = ctx
	tx, err := transactor.UpdateSignersAndThreshold(&opts, signersToRemove, signersToAdd, newThreshold)
	require.NoError(t, err, "send updateSignersAndThreshold")
	receipt, err := bind.WaitMined(ctx, env.Clients.L1, tx)
	require.NoError(t, err, "wait updateSignersAndThreshold mined")
	require.Equal(t, uint64(1), receipt.Status, "updateSignersAndThreshold tx must succeed (tx %s)", tx.Hash())
}

// restoreCommittee best-effort restores the original committee: it removes the added signer if it is
// still present, then resets the threshold to its original value. It is used from cleanup so the
// shared env is left exactly as before. Errors are reported via t.Errorf (cleanup must not abort).
func restoreCommittee(
	ctx context.Context, t *testing.T, env *envs.Env,
	caller *aggchainbase.AggchainbaseCaller, transactor *aggchainbase.AggchainbaseTransactor,
	auth *bind.TransactOpts, addedSigner common.Address, originalThreshold *big.Int,
) {
	t.Helper()
	signers, err := caller.GetAggchainSignerInfos(&bind.CallOpts{Context: ctx})
	if err != nil {
		t.Errorf("cleanup: read signers for restore: %v", err)
		return
	}
	currentThreshold, err := caller.GetThreshold(&bind.CallOpts{Context: ctx})
	if err != nil {
		t.Errorf("cleanup: read threshold for restore: %v", err)
		return
	}

	index := signerIndex(signers, addedSigner)
	alreadyRestored := index < 0 && currentThreshold.Cmp(originalThreshold) == 0
	if alreadyRestored {
		log.Infof("[cleanup] committee already restored (threshold=%s, added signer absent)",
			originalThreshold.String())
		return
	}

	var signersToRemove []aggchainbase.IAggchainSignersRemoveSignerInfo
	if index >= 0 {
		signersToRemove = []aggchainbase.IAggchainSignersRemoveSignerInfo{
			{Addr: addedSigner, Index: big.NewInt(int64(index))},
		}
	}

	opts := *auth
	opts.Context = ctx
	tx, err := transactor.UpdateSignersAndThreshold(&opts, signersToRemove, nil, originalThreshold)
	if err != nil {
		t.Errorf("cleanup: send committee restore tx: %v", err)
		return
	}
	if _, err := bind.WaitMined(ctx, env.Clients.L1, tx); err != nil {
		t.Errorf("cleanup: wait committee restore tx mined: %v", err)
		return
	}
	log.Infof("[cleanup] committee restored to original threshold=%s", originalThreshold.String())
}

// signersContain reports whether the committee signer list contains the given address.
func signersContain(signers []aggchainbase.IAggchainSignersSignerInfo, addr common.Address) bool {
	return signerIndex(signers, addr) >= 0
}

// signerIndex returns the index of the given address in the committee signer list, or -1 if absent.
func signerIndex(signers []aggchainbase.IAggchainSignersSignerInfo, addr common.Address) int {
	for i, s := range signers {
		if s.Addr == addr {
			return i
		}
	}
	return -1
}

// waitForCertificateHeightAbove polls the agglayer read RPC until a SETTLED certificate strictly
// above baselineHeight is observed for the given L2 network, then returns that height. This is the
// Go equivalent of the bats check_height_increase, except the legacy test polls the latest *settled*
// header height while we require both height > baseline AND status == "Settled" so the assertion
// stays faithful (a new certificate was produced AND settled under the changed committee).
func waitForCertificateHeightAbove(
	ctx context.Context, t *testing.T,
	readRPCURL string, l2NetworkID uint32, baselineHeight uint64, timeout time.Duration,
) uint64 {
	t.Helper()
	log.Infof("[waitForCertificateHeightAbove] waiting for settled cert height > %d on network %d",
		baselineHeight, l2NetworkID)

	var lastHeight uint64
	err := pollWithBackoff(ctx, timeout, backoffInitial, backoffMax, "certificate height increase",
		func() (bool, error) {
			header, pollErr := getLatestKnownCertificateHeader(ctx, readRPCURL, l2NetworkID)
			if pollErr != nil {
				log.Debugf("[waitForCertificateHeightAbove] header error (retrying): %v", pollErr)
				return false, nil
			}
			if header == nil {
				return false, nil
			}
			lastHeight = header.Height
			if header.Height > baselineHeight && header.Status == certSettlementStatusSettled {
				return true, nil
			}
			return false, nil
		})
	require.NoError(t, err,
		"wait for settled certificate height above %d within %s", baselineHeight, timeout)
	return lastHeight
}
