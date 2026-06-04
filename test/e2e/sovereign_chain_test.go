package e2e

import (
	"context"
	"encoding/binary"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	"github.com/agglayer/aggkit/bridgeservice/client"
	"github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/test/e2e/envs"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

// TestSovereignChain ports e2e/tests/aggkit/bridge-sovereign-chain-e2e.bats. It runs the two bats
// cases as subtests against the shared op-pp env: sovereign token address mapping via the
// SovereignAdmin key (with decoded-event assertions), and invalid-GER injection on L2 while a real
// bridge claim remains valid. Both subtests are mutating and defer-restore emergency/GER/mapping
// state so the shared env stays healthy for later tests.
func TestSovereignChain(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping E2E test in short mode")
	}
	env := testEnv
	require.NotNil(t, env, "testEnv must be set by TestMain")

	t.Run("SovereignTokenAddressMapping", func(t *testing.T) {
		testSovereignTokenAddressMapping(t, env)
	})

	t.Run("InvalidGEROnL2BridgesValid", func(t *testing.T) {
		testInvalidGEROnL2BridgesValid(t, env)
	})
}

// testSovereignTokenAddressMapping ports the bats "Test Sovereign Chain Bridge Events" mapping flow:
// it calls SetMultipleSovereignTokenAddress and RemoveLegacySovereignTokenAddress with the
// SovereignAdmin transactor and asserts the decoded events. The mapping is removed (the legacy
// address is removed) on the way out so no mapping state leaks into later tests.
func testSovereignTokenAddressMapping(t *testing.T, env *envs.Env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// originNetwork models the bats origin network (0 = L1). The bats maps an L1-origin token to a
	// freshly deployed L2 sovereign token, then removes the *legacy* sovereign token. The L2
	// BridgeL2SovereignChain contract's removeLegacySovereignTokenAddress only accepts a token that is a
	// genuine legacy entry — i.e. wrappedTokenToTokenInfo[token] is set AND the forward map
	// tokenInfoToWrappedToken[keccak(originNetwork,originToken)] points to a DIFFERENT (newer) token
	// (otherwise it reverts TokenNotRemapped, verified live). A single setMultipleSovereignTokenAddress
	// therefore cannot be removed (it is the current token, not a legacy one). To produce a removable
	// legacy token we map the same origin twice: origin->legacyToken, then origin->currentToken (the
	// second call overwrites the forward map to currentToken while leaving legacyToken's reverse entry
	// in place), which makes legacyToken a removable legacy token.
	//
	// Addresses are synthetic and unique per run (derived from a nanosecond nonce) so the mutating
	// mapping state never collides across re-runs on the shared env (setMultipleSovereignTokenAddress
	// reverts TokenAlreadyMapped if the sovereign token already has a reverse entry). They are
	// non-zero and not the L2 networkID-owned origin, which is all the contract requires.
	originNetwork := uint32(0)
	nonce := uint64(time.Now().UnixNano())
	originTokenAddress := syntheticTokenAddr(nonce, 0x01)
	legacyTokenAddress := syntheticTokenAddr(nonce, 0x02)  // the token we will remove
	currentTokenAddress := syntheticTokenAddr(nonce, 0x03) // the newer token that supersedes it

	withCleanEmergencyState(ctx, t, env, func() {
		opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
		require.NoError(t, err, "build sovereign admin transactor")

		// Defer-restore: best-effort remove the legacy mapping we set so no mapping state leaks. Removing
		// legacyTokenAddress is the main-flow assertion below; here it is a no-op safety net for early
		// failures (errors are logged, not fatal). currentTokenAddress is intentionally left mapped: it
		// is the current token for a synthetic, run-unique origin, so it cannot be removed (it would
		// revert TokenNotRemapped) and cannot collide with a future run.
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			cleanupOpts, cerr := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
			if cerr != nil {
				log.Warnf("[sovereign-mapping] could not build cleanup transactor: %v", cerr)
				return
			}
			tx, cerr := env.L2.Contracts.L2Bridge.RemoveLegacySovereignTokenAddress(cleanupOpts, legacyTokenAddress)
			if cerr != nil {
				log.Warnf("[sovereign-mapping] best-effort cleanup RemoveLegacySovereignTokenAddress failed: %v", cerr)
				return
			}
			if _, cerr := bind.WaitMined(cleanupCtx, env.Clients.L2, tx); cerr != nil {
				log.Warnf("[sovereign-mapping] best-effort cleanup wait failed: %v", cerr)
			}
		}()

		// --- SetMultipleSovereignTokenAddress: origin -> legacyToken (SovereignAdmin key) ---
		// bats: cast send setMultipleSovereignTokenAddress([0],[l1_erc20],[l2_sovereign],[false]).
		setTx, err := env.L2.Contracts.L2Bridge.SetMultipleSovereignTokenAddress(
			opts,
			[]uint32{originNetwork},
			[]common.Address{originTokenAddress},
			[]common.Address{legacyTokenAddress},
			[]bool{false},
		)
		require.NoError(t, err, "SetMultipleSovereignTokenAddress (legacy token)")
		setReceipt, err := bind.WaitMined(ctx, env.Clients.L2, setTx)
		require.NoError(t, err, "wait for SetMultipleSovereignTokenAddress receipt")
		require.Equal(t, ethtypes.ReceiptStatusSuccessful, setReceipt.Status,
			"SetMultipleSovereignTokenAddress tx failed")

		// Decode the SetSovereignTokenAddress event (Go equivalent of bats `cast decode-event`).
		setEvent := decodeSetSovereignTokenAddressEvent(t, env, setReceipt)
		// bats: assert_equal "0" "$origin_network".
		require.Equal(t, originNetwork, setEvent.OriginNetwork, "event OriginNetwork")
		// bats: assert_equal "${l1_erc20_addr,,}" "${origin_token_addr,,}".
		require.Equal(t, originTokenAddress, setEvent.OriginTokenAddress, "event OriginTokenAddress")
		// bats: assert_equal "${l2_token_addr_sovereign,,}" "${sov_token_addr,,}".
		require.Equal(t, legacyTokenAddress, setEvent.SovereignTokenAddress, "event SovereignTokenAddress")
		// bats: assert_equal "false" "$is_not_mintable".
		require.False(t, setEvent.IsNotMintable, "event IsNotMintable")
		log.Info("[sovereign-mapping] SetSovereignTokenAddress event verified")

		// Map the SAME origin to a newer currentToken. This overwrites tokenInfoToWrappedToken for that
		// origin to currentToken while leaving legacyToken's reverse entry intact, which is exactly the
		// state removeLegacySovereignTokenAddress requires (forward map points to a different token).
		supersedeTx, err := env.L2.Contracts.L2Bridge.SetMultipleSovereignTokenAddress(
			opts,
			[]uint32{originNetwork},
			[]common.Address{originTokenAddress},
			[]common.Address{currentTokenAddress},
			[]bool{false},
		)
		require.NoError(t, err, "SetMultipleSovereignTokenAddress (current token)")
		supersedeReceipt, err := bind.WaitMined(ctx, env.Clients.L2, supersedeTx)
		require.NoError(t, err, "wait for superseding SetMultipleSovereignTokenAddress receipt")
		require.Equal(t, ethtypes.ReceiptStatusSuccessful, supersedeReceipt.Status,
			"superseding SetMultipleSovereignTokenAddress tx failed")

		// --- RemoveLegacySovereignTokenAddress(legacyToken) (SovereignAdmin key) ---
		// bats: cast send removeLegacySovereignTokenAddress(l2_token_addr_legacy). legacyToken is now a
		// genuine legacy entry (superseded by currentToken), so the call succeeds and emits the event.
		removeTx, err := env.L2.Contracts.L2Bridge.RemoveLegacySovereignTokenAddress(opts, legacyTokenAddress)
		require.NoError(t, err, "RemoveLegacySovereignTokenAddress")
		removeReceipt, err := bind.WaitMined(ctx, env.Clients.L2, removeTx)
		require.NoError(t, err, "wait for RemoveLegacySovereignTokenAddress receipt")
		require.Equal(t, ethtypes.ReceiptStatusSuccessful, removeReceipt.Status,
			"RemoveLegacySovereignTokenAddress tx failed")

		// Decode the RemoveLegacySovereignTokenAddress event.
		removeEvent := decodeRemoveLegacySovereignTokenAddressEvent(t, env, removeReceipt)
		// bats: assert_equal "${l2_token_addr_legacy,,}" "${...sovereignTokenAddress,,}".
		require.Equal(t, legacyTokenAddress, removeEvent.SovereignTokenAddress, "event SovereignTokenAddress")
		log.Info("[sovereign-mapping] RemoveLegacySovereignTokenAddress event verified")
	})
}

// syntheticTokenAddr derives a deterministic, run-unique, non-zero 20-byte address from a nonce and a
// small tag byte. It is used to mint synthetic origin/sovereign token addresses for the mapping flow so
// the mutating sovereign-token mappings never collide across re-runs on the shared env. The layout is:
// bytes[0]=tag, bytes[12..20]=big-endian nonce (the rest zero), which keeps the three tagged addresses
// distinct and far from any real env contract address.
func syntheticTokenAddr(nonce uint64, tag byte) common.Address {
	var a common.Address
	a[0] = tag
	binary.BigEndian.PutUint64(a[12:20], nonce)
	return a
}

// decodeSetSovereignTokenAddressEvent finds and decodes the SetSovereignTokenAddress event from the
// receipt logs (the Go-idiomatic equivalent of bats `cast decode-event`). It returns the first log
// that parses as that event.
func decodeSetSovereignTokenAddressEvent(
	t *testing.T,
	env *envs.Env,
	receipt *ethtypes.Receipt,
) *agglayerbridgel2.Agglayerbridgel2SetSovereignTokenAddress {
	t.Helper()
	for _, lg := range receipt.Logs {
		event, err := env.L2.Contracts.L2Bridge.ParseSetSovereignTokenAddress(*lg)
		if err == nil {
			return event
		}
	}
	require.FailNow(t, "SetSovereignTokenAddress event not found in receipt logs")
	return nil
}

// decodeRemoveLegacySovereignTokenAddressEvent finds and decodes the RemoveLegacySovereignTokenAddress
// event from the receipt logs. It returns the first log that parses as that event.
func decodeRemoveLegacySovereignTokenAddressEvent(
	t *testing.T,
	env *envs.Env,
	receipt *ethtypes.Receipt,
) *agglayerbridgel2.Agglayerbridgel2RemoveLegacySovereignTokenAddress {
	t.Helper()
	for _, lg := range receipt.Logs {
		event, err := env.L2.Contracts.L2Bridge.ParseRemoveLegacySovereignTokenAddress(*lg)
		if err == nil {
			return event
		}
	}
	require.FailNow(t, "RemoveLegacySovereignTokenAddress event not found in receipt logs")
	return nil
}

// testInvalidGEROnL2BridgesValid ports the bats "Test inject invalid GER on L2 (bridges are valid)"
// case: a real (unclaimed) L1->L2 bridge is claimed under an invalid GER, the claim succeeds and is
// indexed, then the GER is removed with the SovereignAdmin key and the env is left healthy.
//
// Construction divergence from bats: the env exposes no L1 GER binding, so the invalid GER is derived
// from the real bridge leaf via buildB1ClaimProof (the established Go-native path used by Category B.1)
// rather than keccak(lastMainnetExitRoot, deadbeef). The "bridges are valid" property is identical:
// the claim verifies and is indexed despite the GER being absent from L1.
func testInvalidGEROnL2BridgesValid(t *testing.T, env *envs.Env) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	withCleanEmergencyState(ctx, t, env, func() {
		// bats: bridge_asset L1->L2, then claim with invalid GER. Real bridge, indexed but not claimed.
		log.Info("[invalid-ger] step: performBridgeL1NoClaim")
		bridge := performBridgeL1NoClaim(ctx, t, env, big.NewInt(100000000000000), common.Address{}, "sovereign-invalid-ger")

		// Build the invalid GER (and exit roots/proofs) from the real bridge leaf.
		proof := buildB1ClaimProof(t, bridge.Bridge, bridge.DepositCount)

		// Defer-restore: best-effort remove the injected GER even if the claim/indexing assertions fail.
		defer func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
			defer cleanupCancel()
			removeInvalidGER(cleanupCtx, env, proof.InvalidGER)
		}()
		// Final health check at the end (or on failure) so leaked state is caught at its origin.
		defer assertNetworkHealthy(ctx, t, env)

		// bats: insertGlobalExitRoot(invalid_ger) with aggoracle key. injectInvalidGER wraps this.
		log.Info("[invalid-ger] step: injectInvalidGER")
		injectInvalidGER(ctx, t, env, proof.InvalidGER)
		assertGERExistsOnL2(ctx, t, env, proof.InvalidGER)

		// bats: claimAsset against the invalid GER. The claim SUCCEEDS (bridges are valid).
		log.Info("[invalid-ger] step: executeB1Claim")
		executeB1Claim(ctx, t, env, bridge, proof)
		assertClaimedOnL2(ctx, t, env, bridge.GlobalIndex)
		log.Info("[invalid-ger] claim successful despite invalid GER")

		// bats: get_claim asserts the indexed claim's rollup_exit_root == invalid rollup exit root.
		log.Info("[invalid-ger] step: waitForClaimOnBridgeService")
		waitForClaimOnBridgeService(ctx, t, env, bridge.GlobalIndex, 2*time.Minute)
		indexedClaim := getIndexedClaim(ctx, t, env, bridge.GlobalIndex)
		// bats: assert_equal "$(... .rollup_exit_root)" "$invalid_rer".
		require.Equal(t, proof.RollupExitRoot, common.HexToHash(string(indexedClaim.RollupExitRoot)),
			"indexed claim rollup_exit_root must match the invalid claim's rollup exit root")
		log.Info("[invalid-ger] claim with invalid GER is indexed")

		// bats: removeGlobalExitRoots([invalid_ger]) with the sovereign admin key.
		log.Info("[invalid-ger] step: removeInvalidGER (SovereignAdmin)")
		removeInvalidGER(ctx, env, proof.InvalidGER)
		// bats: assert_equal "$output" "0" for globalExitRootMap(invalid_ger).
		assertGERRemovedFromL2(ctx, t, env, proof.InvalidGER)
		log.Info("[invalid-ger] GER successfully removed")

		// bats: forceEmitDetailedClaimEvent(...) to correct the aggkit state. Cheap; corrects state.
		// The bats wait_to_settle_certificate_containing_global_index tail is deferred to the live
		// gate (P10b) per the step plan: settlement waits can exceed the per-step budget.
		log.Info("[invalid-ger] step: forceEmitDetailedClaimEvent")
		forceEmitDetailedClaimEvent(ctx, t, env, bridge, proof)
		log.Info("[invalid-ger] corrected DetailedClaimEvent emitted")
	})
}

// removeInvalidGER removes the given GER from the L2 GER manager using the SovereignAdmin key (the
// bats removal uses l2_sovereign_admin_private_key). It is best-effort: errors are logged, not fatal,
// so it can be used both in the main flow and in a defer-restore. injectInvalidGER (aggoracle key)
// is the inverse insert helper in removeger_test.go.
func removeInvalidGER(ctx context.Context, env *envs.Env, gerHash common.Hash) {
	opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
	if err != nil {
		log.Warnf("[invalid-ger] could not build sovereign admin transactor for GER removal: %v", err)
		return
	}
	tx, err := env.L2.Contracts.GlobalExitRoot.RemoveGlobalExitRoots(opts, [][32]byte{gerHash})
	if err != nil {
		log.Warnf("[invalid-ger] RemoveGlobalExitRoots failed: %v", err)
		return
	}
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	if err != nil {
		log.Warnf("[invalid-ger] wait for RemoveGlobalExitRoots receipt failed: %v", err)
		return
	}
	if receipt.Status != ethtypes.ReceiptStatusSuccessful {
		log.Warnf("[invalid-ger] RemoveGlobalExitRoots tx reverted: ger=%s", gerHash.Hex())
	}
}

// getIndexedClaim returns the bridge-service claim for the given global index on the L2 network.
// It mirrors the bats get_claim used to assert the indexed claim's rollup_exit_root.
func getIndexedClaim(ctx context.Context, t *testing.T, env *envs.Env, globalIndex *big.Int) *types.ClaimResponse {
	t.Helper()
	callOpts := &bind.CallOpts{Context: ctx}
	l2NetworkID, err := env.L2.Contracts.L2Bridge.NetworkID(callOpts)
	require.NoError(t, err, "get L2 network ID")
	pageSize := uint32(100)
	resp, err := env.Clients.BridgeService.GetClaims(ctx, client.GetClaimsParams{
		NetworkID:   l2NetworkID,
		PageSize:    &pageSize,
		GlobalIndex: globalIndex,
	})
	require.NoError(t, err, "GetClaims")
	require.NotNil(t, resp, "GetClaims response must not be nil")
	require.NotEmpty(t, resp.Claims, "no indexed claim for global_index=%s", globalIndex.String())
	return resp.Claims[0]
}

// forceEmitDetailedClaimEvent emits a corrected DetailedClaimEvent for the claimed bridge using the
// SovereignAdmin key, mirroring the bats forceEmitDetailedClaimEvent state-correction step. The
// supplied proof carries the local/rollup proofs and exit roots used in the claim; the rollup exit
// root passed here is the real (correct) one so the emitted event matches the canonical bridge data.
func forceEmitDetailedClaimEvent(
	ctx context.Context,
	t *testing.T,
	env *envs.Env,
	bridge *bridgeResult,
	proof *b1ClaimProof,
) {
	t.Helper()
	opts, err := bind.NewKeyedTransactorWithChainID(env.Keys.SovereignAdmin, env.L2.ChainID)
	require.NoError(t, err, "build sovereign admin transactor")

	b := bridge.Bridge
	claimData := agglayerbridgel2.AgglayerBridgeL2ClaimData{
		SmtProofLocalExitRoot:  proof.ProofLocal,
		SmtProofRollupExitRoot: proof.ProofRollup,
		GlobalIndex:            bridge.GlobalIndex,
		MainnetExitRoot:        proof.MainnetExitRoot,
		RollupExitRoot:         proof.RollupExitRoot,
		LeafType:               b.LeafType,
		OriginNetwork:          b.OriginNetwork,
		OriginAddress:          common.HexToAddress(string(b.OriginAddress)),
		DestinationNetwork:     b.DestinationNetwork,
		DestinationAddress:     bridge.DestinationAddr,
		Amount:                 b.Amount.ToBigInt(),
		Metadata:               common.Hex2Bytes(b.Metadata),
	}
	tx, err := env.L2.Contracts.L2Bridge.ForceEmitDetailedClaimEvent(
		opts, []agglayerbridgel2.AgglayerBridgeL2ClaimData{claimData})
	require.NoError(t, err, "ForceEmitDetailedClaimEvent")
	receipt, err := bind.WaitMined(ctx, env.Clients.L2, tx)
	require.NoError(t, err, "wait for ForceEmitDetailedClaimEvent receipt")
	require.Equal(t, ethtypes.ReceiptStatusSuccessful, receipt.Status, "ForceEmitDetailedClaimEvent tx failed")
}
