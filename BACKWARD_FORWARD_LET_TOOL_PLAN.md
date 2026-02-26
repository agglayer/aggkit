# Backward/Forward LET Tool — Implementation Plan

This document describes the implementation plan for the `tools/backward_forward_let` tool, which automates the recovery process described in `docs/backward_forward_let_runbook.md`.

---

## Overview

The tool diagnoses LET (Local Exit Tree) divergence between L1 (AggLayer-settled state) and L2 (on-chain bridge state), reports the situation with undercollateralization details, and optionally executes the recovery (backwardLET/forwardLET) with user confirmation.

**Key clients used**: Bridge Service REST client (`bridgeservice/client`), AggSender debug RPC client (`aggsender/rpcclient`), AggLayer gRPC client (`agglayer`).

---

## Chunk 1: New AggSender RPC Endpoint — `getCertificateBridgeExits`

### Goal
Add a new JSON-RPC endpoint to the aggsender RPC server that returns the bridge exits (leaf data) for a certificate at a given height. This is needed by the tool (and the runbook's "Option 1") to retrieve the full leaf data of settled certificates stored in the aggsender's local DB.

### Non-Goals
- Exposing the full signed certificate body (only the bridge exits are needed).
- Modifying how certificates are stored.
- Adding new gRPC endpoints to the AggLayer client (we use existing ones).

### Steps

1. **Add `GetCertificateBridgeExitsFromSignedCert` method to `aggsender/db/aggsender_db_storage.go`**
   - New method on `AggSenderStorage` interface: `GetCertificateBridgeExits(height uint64) ([]*agglayertypes.BridgeExit, error)`
   - Implementation: call `GetCertificateByHeight(height)`, extract `SignedCertificate` field, JSON-unmarshal to `agglayertypes.Certificate`, return `.BridgeExits`.
   - Handle the `@<filepath>` prefix for external file storage (use existing `SignedCertificateData()` helper on `certificateInfo`).

2. **Add `GetCertificateBridgeExits` RPC method to `aggsender/rpc/aggsender_rpc.go`**
   - Method signature: `GetCertificateBridgeExits(height *uint64) (interface{}, rpc.Error)` — if height is nil, use last sent certificate.
   - Delegates to storage to retrieve bridge exits.
   - Returns the bridge exits as JSON array.

3. **Add `GetCertificateBridgeExits` to `aggsender/rpcclient/client.go`**
   - New method: `GetCertificateBridgeExits(height *uint64) ([]*agglayertypes.BridgeExit, error)`
   - Calls `aggsender_getCertificateBridgeExits` JSON-RPC method.

4. **Update `AggsenderStorer` interface** in `aggsender/rpc/aggsender_rpc.go` to include `GetCertificateBridgeExits`.

### Tests
- Unit test for the storage method with a mock DB containing a certificate with bridge exits.
- Unit test for the RPC handler.
- Unit test for the RPC client (mock JSON-RPC responses).

### Acceptance Criteria
- `aggsender_getCertificateBridgeExits` returns bridge exits for a given height.
- `aggsender_getCertificateBridgeExits` with no params returns bridge exits of the last sent certificate.
- Returns proper error if certificate not found.
- RPC client correctly deserializes the response.

---

## Chunk 2: Authenticated "Send Certificate" Endpoint (Test-Only)

### Goal
Add a new RPC endpoint on the aggsender that allows sending an arbitrary certificate passed as a parameter. This is needed by e2e tests to simulate "malicious certificates" without having to compromise the aggsender flow. The endpoint is security-hardened: disabled by default, requires Ethereum signature auth, and when enabled it disables the aggsender's normal certificate-sending loop.

### Non-Goals
- Making this endpoint available in production (disabled by default).
- Changing the aggsender's normal certificate-sending flow.
- Adding UI/CLI for this endpoint.

### Steps

1. **Add config fields to `aggsender/config/config.go`**
   - `EnableDebugSendCertificate bool` — default `false`. When `true`, enables the debug endpoint.
   - `DebugSendCertificateAuthAddress common.Address` — the Ethereum address authorized to sign requests.
   - When `EnableDebugSendCertificate` is `true`, the aggsender MUST NOT run its normal certificate sending loop (it should be disabled/paused). Enforce this at startup validation.

2. **Implement certificate hash-for-auth in `aggsender/rpc/`**
   - Define a function: `HashCertificateForDebugAuth(cert *agglayertypes.Certificate) common.Hash` — takes the certificate, serializes it deterministically (JSON marshal), and returns `Keccak256` hash.
   - Reuse patterns from `aggsender/validator/cert_hash.go` where applicable.

3. **Add `DebugSendCertificate` RPC method to `aggsender/rpc/aggsender_rpc.go`**
   - Method signature: `DebugSendCertificate(signedRequest DebugSendCertificateRequest) (interface{}, rpc.Error)`
   - `DebugSendCertificateRequest` struct: `{ Certificate: agglayertypes.Certificate, Signature: []byte }` (65-byte Ethereum signature).
   - Validation:
     a. Check `EnableDebugSendCertificate` is `true` (return error if disabled).
     b. Compute hash of the certificate using `HashCertificateForDebugAuth`.
     c. Recover signer address from signature using `crypto.SigToPub` / `crypto.PubkeyToAddress`.
     d. Compare recovered address with `DebugSendCertificateAuthAddress`. Reject if mismatch.
   - On success: send the certificate to the AggLayer via the agglayer client (`SendCertificate`), store it in the aggsender DB, return the certificate hash.
   - The RPC will need access to the agglayer client — extend the `AggsenderRPC` struct to hold it (or add a new `DebugAggsenderRPC` struct registered conditionally).

4. **Conditionally register the endpoint in `aggsender/aggsender.go` → `GetRPCServices()`**
   - Only register `DebugSendCertificate` when `EnableDebugSendCertificate` is `true`.

5. **Enforce aggsender disable when debug endpoint is enabled**
   - In the aggsender's main loop (`aggsender.go`), check the config flag and skip certificate building/sending if `EnableDebugSendCertificate` is `true`.
   - Log a warning at startup: `"Debug send certificate endpoint enabled — aggsender certificate sending is DISABLED"`.

6. **Add `DebugSendCertificate` to `aggsender/rpcclient/client.go`**
   - New method: `DebugSendCertificate(cert *agglayertypes.Certificate, privateKey *ecdsa.PrivateKey) (common.Hash, error)`
   - Internally: compute hash via `HashCertificateForDebugAuth`, sign with `crypto.Sign`, send request.
   - This method handles hashing + signing so callers just pass the cert and key.

### Tests
- Unit test: auth validation (valid signature accepted, invalid rejected, wrong address rejected).
- Unit test: endpoint returns error when disabled.
- Unit test: RPC client correctly signs and sends.

### Acceptance Criteria
- Endpoint is NOT registered when `EnableDebugSendCertificate` is `false` (default).
- When enabled, the aggsender does not send certificates on its own.
- Only requests signed by the configured address are accepted.
- The certificate is sent to the AggLayer and stored in the aggsender DB.
- The RPC client abstracts away all hashing/signing complexity.

---

## Chunk 3: Tool Core — Diagnosis

### Goal
Implement the diagnosis logic in `tools/backward_forward_let/` that determines whether LET divergence exists, classifies it into one of the 4 runbook cases, collects the bridge exits from both sides, and computes undercollateralization.

### Non-Goals
- Executing recovery (that's Chunk 4).
- CLI wiring (that's Chunk 5).
- Computing Merkle proofs for backwardLET (that's Chunk 4).

### Steps

1. **Create file structure** following `tools/remove_ger/` pattern:
   ```
   tools/backward_forward_let/
   ├── cmd/main.go
   ├── config.go
   ├── run.go
   ├── diagnosis.go
   ├── recovery.go
   ├── helpers.go
   └── types.go
   ```

2. **Define types in `types.go`**
   - `DiagnosisResult` struct:
     ```go
     type DiagnosisResult struct {
         Case                   RecoveryCase // Case1, Case2, Case3, Case4, NoDivergence
         L1SettledLER           common.Hash
         L1SettledDepositCount  uint64
         L1SettledHeight        uint64
         L1SettledCertificateID common.Hash
         L2CurrentLER           common.Hash
         L2CurrentDepositCount  uint64
         DivergencePoint        uint64  // last matching deposit count
         ExtraL2Bridges         []bridgesync.LeafData  // bridges on L2 after divergence
         DivergentLeaves        []bridgesync.LeafData  // leaves settled on L1 but not on L2
         Undercollateralization  []UndercollateralizedToken
         IsEmergencyState       bool
     }
     ```
   - `UndercollateralizedToken` struct:
     ```go
     type UndercollateralizedToken struct {
         TokenOriginNetwork uint32
         TokenOriginAddress common.Address
         Amount             *big.Int
     }
     ```
   - `RecoveryCase` enum: `NoDivergence`, `Case1`, `Case2`, `Case3`, `Case4`

3. **Define `Env` struct in `run.go`** (following `remove_ger` pattern)
   ```go
   type Env struct {
       L2Client          *ethclient.Client
       BridgeService     *client.Client
       AgglayerClient    agglayer.AgglayerClientInterface
       AggsenderRPC      *rpcclient.Client
       L2Bridge          *agglayerbridgel2.Agglayerbridgel2
       L2NetworkID       uint32
       Config            *Config
   }
   ```
   - `SetupEnv(cfg *Config) (*Env, error)` — dials L2 RPC, creates bridge service client, creates agglayer gRPC client, creates aggsender RPC client, initializes L2 contract bindings.
   - `Close()` — cleanup.

4. **Implement diagnosis in `diagnosis.go`**

   Step 1: **Query AggLayer for settled state**
   - Call `agglayerClient.GetNetworkInfo(ctx, networkID)` → extract `SettledLER`, `SettledLETLeafCount` (deposit count), `SettledHeight`, `SettledCertificateID`.
   - Call `agglayerClient.GetLatestSettledCertificateHeader(ctx, networkID)` → get `PrevLocalExitRoot`, `NewLocalExitRoot`, etc.
   - Call `agglayerClient.GetLatestPendingCertificateHeader(ctx, networkID)` → check for InError status.

   Step 2: **Query L2 contract for current state**
   - Call `L2Bridge.DepositCount()` → `L2DepositCount`.
   - Call `L2Bridge.GetRoot()` → `L2LER`.
   - Call `L2Bridge.IsEmergencyState()` → bool.
   - Call `L2Bridge.NetworkID()` → sanity check.

   Step 3: **Compare and determine divergence** (per runbook Step 4)
   - If `L2DepositCount < L1DepositCount` → divergence certain.
   - If `L2DepositCount == L1DepositCount` → compare roots.
   - If `L2DepositCount > L1DepositCount` → L2 is ahead, needs historical check. For the tool, since we don't require archive node, we can use the bridge service to find the bridge at L1DepositCount and compare heuristically, OR we rely on the aggsender's certificate data to determine divergence.
   - Determine `DivergencePoint` (the last deposit count where L1 and L2 agree).

   Step 4: **Classify into case** (per runbook summary table)
   - `extraL2 = L2DepositCount > DivergencePoint`
   - `extraL1 = L1DepositCount > DivergencePoint + 1` (more than one divergent leaf settled)
   - Case matrix from runbook.

   Step 5: **Collect L2 bridges after divergence point**
   - For each deposit count from `DivergencePoint+1` to `L2DepositCount-1`:
     - Call `bridgeServiceClient.GetBridgeByDepositCount(ctx, networkID, depositCount)`.
     - Convert response to `LeafData`.

   Step 6: **Collect L1-settled divergent leaves**
   - Try `aggsenderRPCClient.GetCertificateBridgeExits(height)` for each certificate height from the divergence onward.
   - For each certificate, extract the bridge exits that are "divergent" (not matching L2 bridges).
   - If the aggsender API fails → set a flag indicating the user must contact the AggLayer admin, output enough context (certificate ID, network ID, height) for them to make the request.

   Step 7: **Compute undercollateralization**
   - Sum amounts of divergent leaves, grouped by `{TokenOriginNetwork, TokenOriginAddress}`.

5. **Implement `PrintDiagnosis` in `diagnosis.go`**
   - Human-readable CLI output showing:
     - L1 vs L2 state comparison.
     - Case classification.
     - List of divergent leaves with full data.
     - List of extra L2 bridges that would be rolled back and re-added.
     - Undercollateralization table: `{Token Origin Network, Token Origin Address, Amount}`.
     - If aggsender API failed: message instructing user to contact AggLayer admin with specific certificate info.

### Tests
- Unit test for case classification logic with all 4 cases + NoDivergence.
- Unit test for undercollateralization computation.
- Unit test for `PrintDiagnosis` output format.

### Acceptance Criteria
- Given L1 settled state and L2 state, correctly classifies the case.
- Correctly fetches divergent leaves from aggsender API.
- If aggsender API is unreachable, outputs a clear message with certificate ID, network ID, and height for the user to share with AggLayer admin.
- Correctly computes undercollateralization per token.
- Prints a clear, actionable summary to CLI.

---

## Chunk 4: Tool Core — Recovery

### Goal
Implement the recovery execution logic: activate emergency state, execute `backwardLET` and/or `forwardLET` on the L2 bridge contract, deactivate emergency state, and verify the result.

### Non-Goals
- Re-collateralization automation (the tool reports undercollateralization; re-collateralization is manual).
- Stopping/starting the aggsender (the tool advises the user to do this).

### Steps

1. **Implement Merkle tree utilities in `helpers.go`**
   - `ComputeFrontier(leaves []common.Hash, targetIndex uint32) ([32]common.Hash, error)` — given all leaf hashes up to `targetIndex`, compute the 32-element frontier array at `targetIndex`.
     - Use the existing `tree` package's logic: build an in-memory append-only tree with the leaves, extract the frontier.
   - `ComputeLeafHash(leaf bridgesync.LeafData) common.Hash` — hash a leaf using the same algorithm as `bridgesync.Bridge.Hash()` / `agglayertypes.BridgeExit.Hash()`.
   - `ComputeExpectedLER(existingLeaves []common.Hash, newLeaves []bridgesync.LeafData) (common.Hash, error)` — compute the Merkle root after appending new leaves.
   - `ComputeBackwardLETParams(allLeafHashes []common.Hash, targetIndex uint32) (frontier [32]common.Hash, nextLeaf common.Hash, proof [32]common.Hash, error)` — compute all params for the `backwardLET` call.

2. **Implement recovery step functions in `recovery.go`**

   - `stepActivateEmergency(ctx, env, transactOpts)` — call `ActivateEmergencyState`, wait for receipt, verify.
   - `stepBackwardLET(ctx, env, transactOpts, diagnosis)` — call `backwardLET` with computed params, wait, verify deposit count.
   - `stepForwardLET(ctx, env, transactOpts, diagnosis)` — call `forwardLET` with leaves array and expected LER, wait, verify deposit count and root.
   - `stepDeactivateEmergency(ctx, env, transactOpts)` — call `DeactivateEmergencyState`, wait, verify.

3. **Implement `ExecuteRecovery(ctx, env, diagnosis, transactOpts) error` in `recovery.go`**
   - Orchestrates the steps based on `diagnosis.Case`:
     - Case 1: Activate → ForwardLET → Deactivate
     - Case 2: Activate → BackwardLET → ForwardLET → Deactivate
     - Case 3: Activate → ForwardLET → Deactivate
     - Case 4: Activate → BackwardLET → ForwardLET → Deactivate
   - For Cases 2 and 4, the forwardLET leaves = divergent leaves + extra L2 bridges (in that order).
   - For Cases 1 and 3, the forwardLET leaves = divergent leaves only.
   - Verify final state matches L1 settled state where applicable.

4. **Collect all leaf hashes for backwardLET computation**
   - Query bridge service for all L2 bridges from deposit 0 to L2DepositCount.
   - Hash each to get the full leaf hash list needed for frontier/proof computation.

5. **Implement transactor setup in `helpers.go`**
   - `buildTransactOpts(cfg *Config) (*bind.TransactOpts, error)` — following the `remove_ger` pattern using `go_signer`.
   - Support for SovereignAdminKey (for backwardLET/forwardLET), EmergencyPauserKey (for activate), EmergencyUnpauserKey (for deactivate).

### Tests
- Unit test: Merkle frontier computation against known test vectors.
- Unit test: Leaf hash computation matches `bridgesync.Bridge.Hash()`.
- Unit test: Expected LER computation for known leaf sets.
- Unit test: Recovery orchestration logic (mock contract calls).

### Acceptance Criteria
- For each of the 4 cases, the recovery executes the correct sequence of contract calls.
- `backwardLET` params (frontier, nextLeaf, proof) are correctly computed.
- `forwardLET` params (leaves, expectedLER) are correctly computed.
- All contract calls are verified (receipt + on-chain state check).
- Emergency state is always deactivated at the end (even on error, best-effort).

---

## Chunk 5: CLI and Config

### Goal
Wire the tool into a CLI entry point with config file support, following the `tools/remove_ger` patterns.

### Non-Goals
- Accepting command-line parameters for the diagnosis (the tool auto-detects).
- Building a REST API or daemon mode.

### Steps

1. **Implement `config.go`**
   - `Config` struct with embedded configs:
     ```go
     type Config struct {
         Common          etherman.CommonConfig  // L2 RPC URL
         BridgeL2Sync    bridgesync.Config      // L2 bridge contract address
         AgglayerClient  agglayer.ClientConfig   // AggLayer gRPC config
         AggsenderRPCURL string                  // AggSender RPC URL
         BackwardForwardLET BackwardForwardLETConfig
     }
     type BackwardForwardLETConfig struct {
         GERRemoverKey          signertypes.SignerConfig
         EmergencyPauserKey     signertypes.SignerConfig
         EmergencyUnpauserKey   signertypes.SignerConfig
         BridgeServiceURL       string
         L2NetworkID            uint32
     }
     ```
   - `LoadConfig(cliCtx *cli.Context) (*Config, error)` — following the `remove_ger` pattern with template rendering, TOML parsing, Viper unmarshal.

2. **Implement `cmd/main.go`**
   - CLI app with flags:
     - `--cfg` / `-c`: Config file(s) (required, repeatable).
     - `--yes`: Skip interactive confirmation.
   - No `--ger` or other diagnosis parameters (auto-detected).

3. **Implement `Run(cliCtx *cli.Context) error` in `run.go`**
   - Main orchestration:
     1. `LoadConfig(cliCtx)`
     2. `SetupEnv(cfg)`
     3. `Diagnose(ctx, env)` → `DiagnosisResult`
     4. If `NoDivergence` → print "No divergence detected" and exit.
     5. `PrintDiagnosis(diagnosis)` — detailed output.
     6. If diagnosis has `AggsenderAPIFailed` flag → print fallback message and exit (cannot proceed without divergent leaf data).
     7. Interactive confirmation (unless `--yes`).
     8. `ExecuteRecovery(ctx, env, diagnosis)`
     9. Print post-recovery summary.

### Tests
- Unit test for config loading with template variables.

### Acceptance Criteria
- Running `go run tools/backward_forward_let/cmd/main.go -c config.toml` performs full diagnosis.
- Without `--yes`, prompts user before executing recovery.
- Config follows exact same patterns as `tools/remove_ger/config.go`.

---

## Chunk 6: E2E Test Infrastructure — Env Helpers

### Goal
Add helper functions to `test/e2e/envs/loader.go` that support stopping aggkit, editing its config file, and restarting it with modified config. This is needed by the e2e tests to enable the debug send certificate endpoint.

### Non-Goals
- Modifying existing e2e test infrastructure beyond what's needed.
- Adding generic config editing utilities.

### Steps

1. **Add `StopAggkitAndEditConfig` method to `Env`**
   ```go
   func (e *Env) StopAggkitAndEditConfig(ctx context.Context, editFn func(configPath string) error) error
   ```
   - Calls `StopAggkit(ctx)`.
   - Finds the aggkit config file path: `{envDir}/config/001/aggkit-config.toml`.
   - Calls `editFn(configPath)` to let the caller modify the config.
   - Does NOT restart (caller does that explicitly after the edit).

2. **Add `StartAggkitWithModifiedConfig` or reuse existing `StartAggkit`**
   - If docker compose picks up the edited config automatically (bind mount), `StartAggkit` suffices.
   - If not, may need to `docker compose up -d --force-recreate aggkit-001`.

3. **Add `GetAggkitConfigPath` helper**
   ```go
   func (e *Env) GetAggkitConfigPath() string
   ```
   - Returns `{envDir}/config/001/aggkit-config.toml`.

4. **Add `RestartAggkitWithConfig` method**
   ```go
   func (e *Env) RestartAggkitWithConfig(ctx context.Context, editFn func(configPath string) error) error
   ```
   - Convenience: stop → edit → start. With wait for bridge service readiness.

### Tests
- Verified through Chunk 7 e2e tests.

### Acceptance Criteria
- Can stop aggkit, edit its config file, and restart it with the new config.
- Bridge service is confirmed ready after restart.
- Config changes take effect in the running aggkit container.

---

## Chunk 7: E2E Tests

### Goal
Add e2e tests that exercise the backward/forward LET tool for all 4 runbook cases. Tests use the Go interfaces directly (not the CLI). Tests use the authenticated debug send certificate endpoint to create divergence scenarios.

### Non-Goals
- Testing the CLI binary (tested via unit tests in Chunk 5).
- Testing re-collateralization (manual process).
- Testing with real compromised aggsender (we simulate via the debug endpoint).

### Steps

1. **Create `test/e2e/backwardforwardlet_test.go`**
   - `TestMain` uses same pattern as existing: `envs.LoadEnv(ctx, envs.EnvOpPP)`, `env.CheckEnv(ctx)`.
   - Global `testEnv *envs.Env`.

2. **Implement test helper: `sendMaliciousCertificate`**
   ```go
   func sendMaliciousCertificate(t *testing.T, env *envs.Env, cert *agglayertypes.Certificate, signerKey *ecdsa.PrivateKey) common.Hash
   ```
   - Uses `aggsender/rpcclient.Client.DebugSendCertificate(cert, signerKey)`.
   - Returns the certificate hash.

3. **Implement test helper: `enableDebugSendCertEndpoint`**
   ```go
   func enableDebugSendCertEndpoint(t *testing.T, env *envs.Env, authAddress common.Address)
   ```
   - Calls `env.RestartAggkitWithConfig` to edit the config:
     - Set `[AggSender].EnableDebugSendCertificate = true`
     - Set `[AggSender].DebugSendCertificateAuthAddress = <authAddress>`
   - Waits for aggkit to be ready.

4. **Implement test helper: `disableDebugSendCertAndRestart`**
   ```go
   func disableDebugSendCertAndRestart(t *testing.T, env *envs.Env)
   ```
   - Restores original config and restarts aggkit normally.

5. **Implement test helper: `prepareBackwardForwardLETConfig`**
   - Similar to `remove_ger` test's `prepareToolConfig`: reads base aggkit config, patches URLs, adds tool-specific section.

6. **Test Case 1: Forward only — single divergent leaf, no extra L2 bridges**
   ```go
   func TestBackwardForwardLET_Case1(t *testing.T)
   ```
   - Setup:
     a. Wait for at least one settled certificate (poll agglayer `GetNetworkInfo`).
     b. Record the current L2 deposit count and LER.
     c. Enable debug endpoint, stop normal aggsender.
     d. Build a certificate with one fake bridge exit (BX) using the correct `PrevLocalExitRoot` from the last settled cert.
     e. Send the malicious certificate via debug endpoint.
     f. Wait for it to be settled on the AggLayer (poll `GetLatestSettledCertificateHeader`).
     g. Disable debug endpoint, restart normal aggsender (which will now be stuck due to divergence).
   - Diagnosis:
     a. Load tool config.
     b. Call `backward_forward_let.SetupEnv(cfg)`.
     c. Call `backward_forward_let.Diagnose(ctx, env)`.
     d. Assert `diagnosis.Case == Case1`.
     e. Assert `len(diagnosis.DivergentLeaves) == 1`.
     f. Assert undercollateralization matches BX amount.
   - Recovery:
     a. Call `backward_forward_let.ExecuteRecovery(ctx, env, diagnosis)`.
     b. Verify L2 deposit count = original + 1.
     c. Verify L2 LER matches L1 settled LER.
     d. Verify emergency state is false.

7. **Test Case 2: Backward + forward — single divergent leaf, extra L2 bridges**
   ```go
   func TestBackwardForwardLET_Case2(t *testing.T)
   ```
   - Setup:
     a. Same as Case 1 through step (f) — send malicious cert and wait for settle.
     b. **Before** disabling debug endpoint: disable debug endpoint and restart normal aggsender briefly to let new L2 bridges happen, OR send L2 bridge transactions directly while aggsender is stopped (bridges happen on L2 independent of aggsender).
     c. Create 1-2 additional L2 bridges (B3, B4) by sending bridge txs on L2.
     d. Now the aggsender will be stuck because LER diverged.
   - Diagnosis:
     a. Assert `diagnosis.Case == Case2`.
     b. Assert extra L2 bridges are correctly identified.
     c. Assert divergent leaves match.
   - Recovery:
     a. Execute recovery.
     b. Verify L2 deposit count = L1 settled + extra L2 bridges.
     c. Verify emergency state is false.

8. **Test Case 3: Forward only — multiple divergent leaves, no extra L2 bridges**
   ```go
   func TestBackwardForwardLET_Case3(t *testing.T)
   ```
   - Setup:
     a. Send two malicious certificates (BX and BY) sequentially via debug endpoint.
     b. Wait for both to settle.
     c. Ensure no L2 bridges happen between them.
   - Diagnosis:
     a. Assert `diagnosis.Case == Case3`.
     b. Assert `len(diagnosis.DivergentLeaves) == 2`.
     c. Assert undercollateralization = amount(BX) + amount(BY).
   - Recovery:
     a. Execute recovery.
     b. Verify L2 deposit count = original + 2.
     c. Verify LER matches last settled.

9. **Test Case 4: Backward + forward — multiple divergent leaves, extra L2 bridges**
   ```go
   func TestBackwardForwardLET_Case4(t *testing.T)
   ```
   - Setup:
     a. Send two malicious certificates via debug endpoint.
     b. Wait for both to settle.
     c. Create additional L2 bridges.
   - Diagnosis:
     a. Assert `diagnosis.Case == Case4`.
   - Recovery:
     a. Execute recovery.
     b. Verify correct final state.

10. **Test: No divergence detected**
    ```go
    func TestBackwardForwardLET_NoDivergence(t *testing.T)
    ```
    - Run diagnosis on a healthy system.
    - Assert `diagnosis.Case == NoDivergence`.

11. **Test: Aggsender API unavailable fallback**
    ```go
    func TestBackwardForwardLET_AggsenderAPIFallback(t *testing.T)
    ```
    - Point aggsender RPC URL to an invalid address.
    - Run diagnosis.
    - Assert that the tool reports it cannot fetch bridge exits and provides the certificate info for the user to contact AggLayer admin.

### Tests
- All tests use Go interfaces directly, not CLI.
- All tests follow the `testmain_test.go` pattern with env loading and post-test validation.
- Each test is independent and can run in isolation.

### Acceptance Criteria
- All 4 runbook cases are tested end-to-end.
- NoDivergence case works.
- Aggsender API fallback case correctly reports the issue.
- After recovery, the system is healthy (aggsender can send new certificates).
- Tests clean up properly (emergency state deactivated, aggsender restarted).

---

## Implementation Order & Dependencies

```
Chunk 1 (AggSender RPC: getCertificateBridgeExits)
  │
  ├──► Chunk 3 (Diagnosis) ──► Chunk 4 (Recovery) ──► Chunk 5 (CLI)
  │                                                         │
Chunk 2 (Debug Send Cert Endpoint)                          │
  │                                                         │
  ├──► Chunk 6 (E2E Env Helpers)                            │
  │         │                                               │
  └─────────┴──► Chunk 7 (E2E Tests) ◄──────────────────────┘
```

**Recommended execution order:**
1. **Chunk 1** — foundational, needed by diagnosis
2. **Chunk 2** — can be done in parallel with Chunk 1, needed by e2e tests
3. **Chunk 3** — depends on Chunk 1
4. **Chunk 4** — depends on Chunk 3
5. **Chunk 5** — depends on Chunks 3+4
6. **Chunk 6** — depends on Chunk 2, can be done in parallel with Chunks 3-5
7. **Chunk 7** — depends on all previous chunks

---

## Key Design Decisions

1. **No parameters**: The tool auto-detects divergence by comparing AggLayer settled state vs L2 on-chain state. No GER hash or other inputs needed.

2. **Graceful fallback**: When the aggsender API cannot provide bridge exits (DB lost, different instance), the tool outputs a clear message with all info needed to contact the AggLayer admin, rather than failing silently.

3. **Undercollateralization report**: Grouped by `{TokenOriginNetwork, TokenOriginAddress}` with summed amounts for each token. This helps the operator understand the re-collateralization needed.

4. **Debug endpoint security**: Three layers — disabled by default, requires Ethereum signature auth, disables aggsender sending loop. This makes it safe to have in the codebase without risk of accidental production use.

5. **E2E test strategy**: Tests create divergence via the debug endpoint (sending malicious certs), then use the tool's Go API to diagnose and recover. This tests the full flow without needing to actually compromise the aggsender.

6. **Merkle proof computation**: Uses the existing `tree` package for root/proof computation, building an in-memory tree from bridge service data. This avoids depending on an archive node for historical state.
