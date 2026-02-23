# Implementation Plan: Remove GER Tooling & E2E Tests

This document describes the implementation plan split into two deliverables:

1. **Tooling** (`tools/remove_ger/`): A production-ready CLI tool that diagnoses and recovers from invalid GER injection.
2. **Tests** (`test/e2e/removeger_test.go`): E2E tests that create each scenario, invoke the tool, and assert the network heals.

---

## Prerequisites & Key References

- **Runbook**: `docs/remove_ger_runbook.md`
- **Env loader**: `test/e2e/envs/loader.go` (provides `envs.Env` struct)
- **Existing test**: `test/e2e/bridge_test.go` (uses same env, must run in parallel)
- **Existing tool pattern**: `tools/aggsender_find_imported_bridge/` (standalone `main` package under `tools/`)
- **CLI pattern**: `cmd/main.go` uses `urfave/cli/v2`, `config.Load()` reads TOML config files
- **Config struct**: `config/config.go` defines `Config` with all component configs
- **Contract bindings** (from `cdk-contracts-tooling`):
  - `agglayergerl2.Agglayergerl2`: `InsertGlobalExitRoot`, `RemoveGlobalExitRoots`, `GlobalExitRootMap`
  - `agglayerbridgel2.Agglayerbridgel2`: `ActivateEmergencyState`, `DeactivateEmergencyState`, `IsEmergencyState`, `UnsetMultipleClaims`, `SetMultipleClaims`, `ForceEmitDetailedClaimEvent`, `IsClaimed`, `ClaimAsset`, `BridgeAsset`
- **Bridge service client** (`bridgeservice/client`): `GetRemoveGEREvents`, `GetUnsetClaims`, `GetSetClaims`, `GetClaims`, `GetBridges`, `GetClaimProof`, `GetL1InfoTreeIndex`, `GetInjectedL1InfoLeaf`
- **Env config**: `test/e2e/envs/op-pp/config/001/aggkit-config.toml`

### Key Accounts Available in the Environment

The `op-pp` env (`summary.json`) provides:
- **20 L1 accounts** with private keys (pre-funded with 1000000000 ETH each)
- **~20 L2 accounts** with private keys (pre-funded)
- Keystores for aggoracle and sovereign admin (encrypted, password: `pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m`)

---

## Part A: Tooling (`tools/remove_ger/`)

### Design Overview

The tool is a standalone CLI binary (following the `tools/aggsender_find_imported_bridge/` pattern) that:

1. Accepts an invalid GER hash as input
2. Loads the standard aggkit config file (reusing `config.Load()` machinery) for L1/L2 RPC URLs and contract addresses
3. Extends the config with a `[RemoveGER]` section for the sovereign admin private key
4. Validates the GER doesn't exist on L1
5. Queries the L2 bridge for claims made using that GER
6. Classifies each claim (Category A, B.1, B.2) or determines there are no claims
7. Prints a human-readable recovery plan
8. Asks the user to confirm (interactive prompt)
9. Executes the recovery plan step by step, printing progress

### File Layout

```
tools/remove_ger/
├── main.go            # CLI entry point (urfave/cli/v2)
├── config.go          # Extended config struct
├── diagnosis.go       # GER validation + claim classification logic
├── recovery.go        # Recovery execution (freeze, remove, unset, set, emit, restore)
├── helpers.go         # Shared utilities (tx helpers, polling, formatting)
└── README.md          # Usage documentation
```

---

## Chunk 0: Environment Infrastructure — Shared Env & Private Key Management

### Goals

1. Refactor the e2e test infrastructure so that `loader_test.go`, `bridge_test.go`, and the new `removeger_test.go` **share a single environment instance** (start once, stop once).
2. The `Env` struct already exposes all available private keys from `summary.json` via `KeyPool`, plus `AggOracle` and `SovereignAdmin` keys — this is done.
3. Create `TestMain` for shared env lifecycle.

### Scope

- `test/e2e/envs/loader_test.go` → move to `test/e2e/loader_test.go` (change package to `e2e`, use shared env).
- `test/e2e/testmain_test.go` (new): `TestMain` function that starts the env once and provides it to all tests.
- `test/e2e/bridge_test.go`: Adapt to use shared `testEnv`.

### Non-Goals

- Do not change the docker compose setup or `summary.json`.
- Do not implement the GER removal test logic or tooling in this chunk.

### Detailed Changes

#### 0.1 Create shared env via `TestMain`

Create `test/e2e/testmain_test.go`:

```go
package e2e

var testEnv *envs.Env

func TestMain(m *testing.M) {
    ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
    defer cancel()

    env, err := envs.LoadEnv(ctx, envs.EnvOpPP)
    if err != nil {
        log.Fatalf("failed to load env: %v", err)
    }
    testEnv = env

    code := m.Run()

    stopCtx, stopCancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer stopCancel()
    if err := env.Stop(stopCtx); err != nil {
        log.Printf("failed to stop env: %v", err)
    }

    os.Exit(code)
}
```

#### 0.2 Move `loader_test.go` to `e2e` package

- Move `test/e2e/envs/loader_test.go` to `test/e2e/loader_test.go`.
- Change package from `envs` to `e2e`.
- Rename `TestLoadEnv` to `TestEnvLoader` and refactor to use `testEnv`.
- Remove the `defer env.Stop()` since `TestMain` handles lifecycle.
- Keep `TestLoadEnv_InvalidEnvName` and `TestFindEnvsDir` — these are fast unit-style tests that don't need the shared env; they can stay in `envs` package or be adapted.

#### 0.3 Refactor `bridge_test.go`

- Remove `envs.LoadEnv()` call and `defer env.Stop()`.
- Use `testEnv` instead.
- Use `testEnv.Keys.L1Keys.Checkout()` / `testEnv.Keys.L2Keys.Checkout()` for dedicated keys.

### Acceptance Criteria

- [x] All existing tests pass with the shared env pattern.
- [x] `loader_test.go` tests run as part of the `e2e` package.
- [x] Only one docker compose up/down cycle per `go test ./test/e2e/` invocation.

---

## Chunk 1: Tool — Config & CLI Skeleton

### Goals

Set up the `tools/remove_ger/` package with the CLI entry point, extended config struct, and config loading. The tool should accept a config file and a GER hash, load the config, connect to L1/L2, and exit (no logic yet).

### Scope

- `tools/remove_ger/main.go`
- `tools/remove_ger/config.go`

### Non-Goals

- No diagnosis or recovery logic yet.
- No README yet (will be written in the final chunk).

### Detailed Changes

#### 1.1 Extended config struct (`config.go`)

The tool reuses the standard aggkit config file (`aggkit-config.toml`) for all L1/L2 connectivity. It adds a `[RemoveGER]` section for the sovereign admin private key (needed to execute recovery transactions).

```go
package removeger

import (
    "github.com/agglayer/aggkit/config"
    // ...
)

// Config extends the main aggkit config with fields specific to the remove-GER tool.
type Config struct {
    config.Config

    RemoveGER RemoveGERConfig
}

// RemoveGERConfig contains configuration specific to the remove-GER tool.
type RemoveGERConfig struct {
    // SovereignAdminPrivateKey is the private key with privileges to:
    // - activateEmergencyState / deactivateEmergencyState on the L2 bridge
    // - removeGlobalExitRoots on the L2 GER manager
    // - unsetMultipleClaims / setMultipleClaims on the L2 bridge
    // - forceEmitDetailedClaimEvent on the L2 bridge
    SovereignAdminPrivateKey KeyConfig

    // BridgeServiceURL is the URL of the aggkit bridge service REST API.
    // Used for querying claims, bridges, and proofs.
    BridgeServiceURL string
}

type KeyConfig struct {
    Path     string
    Password string
}
```

**Rationale**: By embedding `config.Config`, the tool can be invoked with the same config file that runs the aggkit node. The `[RemoveGER]` section only adds what's missing: the sovereign admin key (not present in the standard config) and bridge service URL.

#### 1.2 Config loading

Reuse the `config.LoadFile()` machinery to parse the TOML into the extended struct. Since `config.LoadFile()` returns `*config.Config`, we load into the extended struct via viper directly (same approach as `config.LoadFile` but targeting our struct):

```go
func LoadConfig(configPaths []string) (*Config, error) {
    // Use the same config loading pipeline as the main aggkit binary:
    // 1. Read files
    // 2. Merge defaults
    // 3. Render variables
    // 4. Unmarshal into Config (which embeds config.Config + RemoveGERConfig)
    // ...
}
```

Alternatively, if the config loading pipeline doesn't easily extend, load the base config via `config.LoadFile()` and then re-parse only the `[RemoveGER]` section from the same TOML files via a second viper pass. Choose whichever approach is cleanest.

#### 1.3 CLI entry point (`main.go`)

Follow the `urfave/cli/v2` pattern from `cmd/main.go`:

```go
package main

import (
    "os"
    "github.com/urfave/cli/v2"
    removeger "github.com/agglayer/aggkit/tools/remove_ger"
)

func main() {
    app := cli.NewApp()
    app.Name = "remove-ger"
    app.Usage = "Diagnose and recover from invalid GER injection on L2"
    app.Flags = []cli.Flag{
        &cli.StringSliceFlag{
            Name:     "cfg",
            Aliases:  []string{"c"},
            Usage:    "Configuration file(s) (same format as aggkit-config.toml)",
            Required: true,
        },
        &cli.StringFlag{
            Name:     "ger",
            Usage:    "The invalid GER hash to diagnose and remove (hex, 0x-prefixed)",
            Required: true,
        },
        &cli.BoolFlag{
            Name:  "yes",
            Usage: "Skip interactive confirmation and execute the recovery plan immediately",
        },
    }
    app.Action = removeger.Run

    if err := app.Run(os.Args); err != nil {
        os.Exit(1)
    }
}
```

The `Run` function in this chunk will:
1. Parse the GER flag
2. Load the config
3. Connect to L1 and L2 RPC
4. Initialize contract bindings (L2 bridge, L2 GER manager) and bridge service client
5. Print "Connected to L1 (chain X) and L2 (chain Y)" and exit

#### 1.4 Makefile integration

Add a build target following the existing pattern:

```makefile
$(GOBIN)/remove_ger: ## Build remove_ger tool
	$(GOENVVARS) go build -o $(GOBIN)/remove_ger ./tools/remove_ger
```

### Acceptance Criteria

- [x] `go build ./tools/remove_ger` succeeds.
- [x] `./remove_ger --cfg aggkit-config.toml --ger 0xabc...` connects to L1/L2 and exits cleanly.
- [x] Extended config loads `[RemoveGER]` section alongside standard config.
- [x] Build target added to Makefile.

---
ter
## Chunk 2: Tool — Diagnosis (GER Validation & Claim Classification)

### Goals

Implement the diagnosis phase: validate the GER doesn't exist on L1, find claims on L2 that used the GER, and classify each claim as Category A, B.1, B.2, or determine there are no claims.

### Scope

- `tools/remove_ger/diagnosis.go`

### Non-Goals

- No recovery execution yet.
- No interactive confirmation yet.

### Detailed Changes

#### 2.1 GER validation

```go
type DiagnosisResult struct {
    InvalidGER     common.Hash
    GERExistsOnL1  bool     // should be false for a truly invalid GER
    GERExistsOnL2  bool     // should be true (it was injected)
    GERTimestampL2 *big.Int // timestamp from globalExitRootMap on L2
    Claims         []ClaimDiagnosis
    Scenario       Scenario // NoClaims, CategoryA, CategoryB1, CategoryB2
}

type Scenario string

const (
    ScenarioNoClaims  Scenario = "no_claims"
    ScenarioCategoryA Scenario = "category_a"
    ScenarioCategoryB1 Scenario = "category_b1"
    ScenarioCategoryB2 Scenario = "category_b2"
)

type ClaimDiagnosis struct {
    GlobalIndex     *big.Int
    DepositCount    uint32
    OriginNetwork   uint32
    Category        Scenario // per-claim classification
    // For B.1/B.2: the correct L1 bridge data
    CorrectBridge   *BridgeData // nil for Category A
}
```

#### 2.2 Step 1 — Validate GER on L1

Call `L1 GlobalExitRoot contract.globalExitRootMap(gerHash)`. If timestamp > 0, the GER exists on L1 — warn the user this may not be an invalid GER and exit (or continue with `--force`).

#### 2.3 Step 2 — Validate GER on L2

Call `L2 GER Manager.globalExitRootMap(gerHash)`. If timestamp == 0, the GER doesn't exist on L2 either — nothing to do.

#### 2.4 Step 3 — Find claims using the GER

Query the bridge service API for claims that reference the invalid GER. Use `GetClaims()` with appropriate filters. The bridge service exposes claims indexed by `global_exit_root`.

If no claims are found: `Scenario = NoClaims`.

#### 2.5 Step 4 — Classify each claim

For each claim found, follow the decision tree from `docs/remove_ger_runbook.md`:

1. Decode `global_index` → extract `origin_network`, `deposit_count`, `mainnet_flag` (this logic should exist already on the code base, most likely on the l1infotreesync package. Re-use it if possible)
2. Query the origin bridge (L1 bridge service or L1 bridgesync) for a bridge at that `deposit_count`
3. If bridge doesn't exist on L1 → **Category A**
4. If bridge exists, compare content (leaf_type, origin_network, origin_address, destination_network, destination_address, amount, metadata)
5. If content differs → **Category A**
6. If content matches and same deposit_count → compare GER components → **Category B.1**
7. If content matches but different deposit_count → **Category B.2**

The overall scenario is determined by the "worst" category among all claims (A > B.2 > B.1 > NoClaims). If there are mixed categories, the tool should report each claim's category and use the most complex recovery flow that covers all.

#### 2.6 Diagnosis output

Print a human-readable summary:

```
=== Remove GER Diagnosis ===

Invalid GER: 0xabc...
  L1: NOT FOUND (confirmed invalid)
  L2: EXISTS (timestamp: 1234567890)

Claims using this GER: 2

  Claim 1:
    Global Index: 0x010000000000000a
    Origin Network: 0 (L1)
    Deposit Count: 10
    Category: A (under-collateralization — bridge does not exist on L1)

  Claim 2:
    Global Index: 0x010000000000000b
    Origin Network: 0 (L1)
    Deposit Count: 11
    Category: B.1 (GER mismatch, same index — bridge exists on L1 with correct content)

Overall Scenario: Category A (most restrictive)

=== Recovery Plan ===

The following steps will be executed:
  1. Freeze bridge (activateEmergencyState)
  2. Remove GER 0xabc... (removeGlobalExitRoots)
  3. Unset claim 0x010000000000000a (unsetMultipleClaims)
  4. Force emit corrected claim event for 0x010000000000000b (forceEmitDetailedClaimEvent)
  5. Restore bridge (deactivateEmergencyState)

Proceed? [y/N]
```

### Acceptance Criteria

- [x] `DiagnosisResult` correctly identifies NoClaims, A, B.1, B.2 scenarios.
- [x] GER validation checks both L1 and L2 on-chain state.
- [x] Claim classification follows the decision tree from the runbook.
- [x] Human-readable output clearly shows per-claim classification and overall plan.

---

## Chunk 3: Tool — Recovery Execution

### Goals

Implement the recovery execution phase: given a `DiagnosisResult`, execute the appropriate recovery steps, printing progress and verifying each step.

### Scope

- `tools/remove_ger/recovery.go`
- `tools/remove_ger/helpers.go`
- Wire everything together in `tools/remove_ger/main.go` (`Run` function)

### Non-Goals

- Tests are in a separate chunk.

### Detailed Changes

#### 3.1 Recovery executor

```go
func ExecuteRecovery(ctx context.Context, cfg *Config, diagnosis *DiagnosisResult) error
```

The function executes the appropriate recovery flow based on the scenario:

**No Claims:**
1. `freezeBridge()` → verify `isEmergencyState == true`
2. `removeGERs([gerHash])` → verify `globalExitRootMap == 0`
3. `restoreBridge()` → verify `isEmergencyState == false`

**Category A:**
1. `freezeBridge()`
2. `removeGERs([gerHash])`
3. `unsetClaims(globalIndexes)` → verify each `isClaimed == false`
4. `restoreBridge()`

**Category B.1:**
1. `freezeBridge()`
2. `removeGERs([gerHash])`
3. `forceEmitDetailedClaimEvents(correctedClaimData)` → verify via DB
4. `restoreBridge()`

**Category B.2:**
1. `freezeBridge()`
2. `removeGERs([gerHash])`
3. `unsetClaims(wrongGlobalIndexes)` → verify on smart contract
4. `setClaims(correctGlobalIndexes)` → verify each `isClaimed == true`
5. `forceEmitDetailedClaimEvents(correctedClaimData)`
6. `restoreBridge()`

Each step:
- Prints what it's about to do
- Executes the transaction
- print tx hash
- Waits for the receipt
- Verifies the on-chain state
- Prints the result
- Fails fast on error (bridge remains in emergency state for manual intervention)

#### 3.2 Transaction helpers (`helpers.go`)

Shared utilities for building and sending transactions:

```go
func buildSovereignAdminTransactor(cfg *Config, chainID *big.Int) (*bind.TransactOpts, error)
func waitForReceipt(ctx context.Context, client *ethclient.Client, txHash common.Hash) (*types.Receipt, error)
func pollBridgeService(ctx context.Context, client *bridgeclient.Client, check func() (bool, error), timeout time.Duration) error
```

#### 3.3 Interactive confirmation

In `Run()`, after diagnosis:
- Print the recovery plan
- If `--yes` flag is not set, prompt the user for confirmation via `fmt.Scanln`
- If confirmed, call `ExecuteRecovery()`
- If not confirmed, exit cleanly

#### 3.4 Full `Run` flow

```go
func Run(cliCtx *cli.Context) error {
    // 1. Parse flags
    // 2. Load config
    // 3. Connect to L1/L2, initialize contracts and bridge service client
    // 4. Run diagnosis
    // 5. Print diagnosis + recovery plan
    // 6. Ask for confirmation (unless --yes)
    // 7. Execute recovery
    // 8. Print post-recovery verification summary
    return nil
}
```

### Acceptance Criteria

- [x] Recovery flows for all 4 scenarios are implemented.
- [x] Each step verifies on-chain state before and after.
- [x] Interactive confirmation works (and `--yes` skips it).
- [x] Errors at any step abort cleanly with clear messages.
- [x] `go build ./tools/remove_ger` succeeds.

---

## Chunk 4: Tool — README

### Goals

Write documentation for the `remove-ger` tool explaining how to use it, what the config file needs, and how the tool relates to the `docs/remove_ger_runbook.md`.

### Scope

- `tools/remove_ger/README.md`

### Detailed Changes

The README should cover:

1. **Overview**: What the tool does, when to use it, relationship to `docs/remove_ger_runbook.md`
2. **Building**: `go build ./tools/remove_ger` or `make build-tools`
3. **Config file**: Explain that the tool uses the standard `aggkit-config.toml` with an additional `[RemoveGER]` section. Document all fields in `RemoveGERConfig`
4. **Example config addition**
5. **Usage examples**:
   ```bash
   # Diagnose and interactively recover
   ./remove_ger --cfg aggkit-config.toml --ger 0xabc...

   # Non-interactive (for automation)
   ./remove_ger --cfg aggkit-config.toml --ger 0xabc... --yes
   ```
6. **Scenarios**: Brief description of each scenario (NoClaims, A, B.1, B.2) and what recovery steps are performed
7. **Troubleshooting**: Common issues (wrong private key, bridge service not reachable, GER actually exists on L1)

### Acceptance Criteria

- [x] README is clear and complete.
- [x] Config changes vs the main config file are documented.
- [x] All CLI flags are documented.

---

## Part B: E2E Tests

### Design Overview

The E2E tests live in `test/e2e/removeger_test.go` and use the shared environment from Chunk 0. The goal is to **assert that the procedure described in `docs/remove_ger_runbook.md` works end-to-end**. A central part of that procedure is **realising that there is a problematic GER through aggkit logs**; the runbook does not assume the operator already knows the GER hash.

Therefore, each test must **detect** the problematic GER by inspecting the logs of the aggkit running on the env (using the same error patterns and locations as the runbook), and only then pass the **detected** GER to the tool. The test must not pass the injected GER directly to the tool, even though the test setup knows which GER was injected. This keeps the test aligned with the runbook and validates that the detection step works in practice.

Each test:

1. **Creates** the scenario (injects invalid GER, optionally executes dummy/real claims)
2. **Detects** the problematic GER by inspecting aggkit logs (aggsender and/or l2gersync) for the runbook’s error patterns and extracting the GER hash (see "GER detection from logs" below)
3. **Invokes** the tool's diagnosis and recovery logic programmatically with the **detected** GER (importing the `tools/remove_ger` package directly, not shelling out to the binary)
4. **Asserts** that:
   - The scenario is detected correctly (correct claim classification)
   - The recovery plan completes successfully
   - The network is healed: settlements keep happening, a new L1→L2 bridge + claim succeeds

#### GER detection from logs (runbook-aligned)

Detection must use the same sources and patterns as `docs/remove_ger_runbook.md` (section "Detection"):

- **AggSender logs**: Look for error patterns that include the GER hash, e.g.  
  `error getting proof for GER: <GER_HASH>`,  
  `error getting L1 Info tree merkle proof for GER: <GER_HASH>`,  
  `error getting info by global exit root: ...`,  
  `error sending certificate: ...`,  
  `certificate validation failed: ...`  
  (Runbook references: aggsender/query/ger_query.go, imported_bridge_exit_converter.go, l1info_tree_data_query.go, aggsender.go, local_validator.go.)
- **L2 GER Sync logs**: Look for  
  `failed to fetch l1 info tree for global exit root <GER_HASH>`,  
  `GER <GER_HASH> not found in L1 contract globalExitRootMap`,  
  `GER lookup for <GER_HASH> failed in L1 contract`.  
  (Runbook reference: l2gersync/evm_downloader_sovereign.go.)

The test helper that implements this (e.g. `detectInvalidGERFromAggkitLogs(ctx, t, env) (common.Hash, error)`) should read the aggkit process logs (from the env’s running aggkit or from a log stream/capture), match these patterns, and parse out the GER hash. If multiple GERs appear (e.g. in B.2), the test may need to collect all distinct GER hashes and pass the one(s) relevant to the scenario. The implementation may need to trigger certificate generation (e.g. by waiting for aggsender to attempt a cert that uses the invalid GER) so that the runbook’s error messages actually appear in the logs before parsing.

### Config file handling for tests

The tests use the config file at `test/e2e/envs/op-pp/config/001/aggkit-config.toml`. Since this file doesn't contain the `[RemoveGER]` section, the test will:

1. Copy the original config to a temp file
2. Append the `[RemoveGER]` section programmatically (sovereign admin key path + password, bridge service URL from the env)
3. Pass the temp file path to the tool's config loader

```go
func prepareToolConfig(t *testing.T, env *envs.Env) string {
    // 1. Find the original config file
    envsDir, _ := envs.FindEnvsDir()
    originalCfg := filepath.Join(envsDir, "op-pp", "config", "001", "aggkit-config.toml")

    // 2. Read original content
    content, err := os.ReadFile(originalCfg)
    require.NoError(t, err)

    // 3. Append [RemoveGER] section
    appendSection := fmt.Sprintf(`
[RemoveGER]
BridgeServiceURL = "%s"
L2BridgeAddr = "%s"
L2GERAddr = "%s"

[RemoveGER.SovereignAdminPrivateKey]
Path = "%s"
Password = "%s"
`,
        bridgeServiceURL,    // from env
        l2BridgeAddr,        // from env
        l2GERAddr,           // from env
        sovereignAdminKeyPath, // keystore path in env
        keystorePassword,
    )

    // 4. Write to temp file
    tmpFile := filepath.Join(t.TempDir(), "aggkit-config-test.toml")
    err = os.WriteFile(tmpFile, append(content, []byte(appendSection)...), 0o600)
    require.NoError(t, err)

    return tmpFile
}
```

---

## Chunk 5: Test Helpers — GER Injection & Dummy Claims

### Goals

Implement reusable helper functions for the E2E test scenarios. These helpers set up the "broken" state that the tool will then diagnose and recover from.

### Scope

- `test/e2e/removeger_test.go`: Helper functions only (no test cases yet).

### Non-Goals

- No test scenarios in this chunk.

### Detailed Changes

#### 5.1 GER injection helper

Inject a fake/invalid GER into the L2 GER Manager contract using the aggoracle private key:

```go
func injectInvalidGER(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash) *ethtypes.Receipt
```

Uses `env.L2.Contracts.GlobalExitRoot.InsertGlobalExitRoot()` with a transactor built from `env.Keys.AggOracle`.

#### 5.2 GER verification helpers

```go
func assertGERExistsOnL2(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash)
func assertGERRemovedFromL2(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash)
```

#### 5.3 Dummy claim execution helper

Execute a claim using fabricated/invalid data (for Category A setup):

```go
func executeDummyClaim(ctx context.Context, t *testing.T, env *envs.Env, params dummyClaimParams) *ethtypes.Receipt
```

Calls `ClaimAsset` on the L2 bridge with fabricated merkle proofs, global index, exit roots, etc. The data is crafted so the claim succeeds on L2 (because the GER was injected by the aggoracle) but references an invalid GER that doesn't exist on L1.

#### 5.4 Real bridge helper

For B.1/B.2 scenarios that need a real L1→L2 bridge:

```go
func performRealBridgeL1ToL2(ctx context.Context, t *testing.T, env *envs.Env) *bridgeResult
```

#### 5.5 Bridge service polling helpers

Retry-based helpers to wait for bridge service sync:

```go
func waitForGEROnBridgeService(ctx context.Context, t *testing.T, env *envs.Env, gerHash common.Hash, timeout time.Duration)
func waitForClaimOnBridgeService(ctx context.Context, t *testing.T, env *envs.Env, globalIndex *big.Int, timeout time.Duration)
```

#### 5.6 Network health assertion helper

Verifies the network is healthy after recovery:

```go
func assertNetworkHealthy(ctx context.Context, t *testing.T, env *envs.Env)
```

This helper:
1. Checks that settlements keep happening (aggsender produces valid certificates)
2. Performs a fresh L1→L2 bridge
3. Claims the bridge on L2
4. Asserts the claim succeeds

### Acceptance Criteria

- [x] All helper functions compile.
- [x] Helpers use proper transactors (aggoracle key for injection, sovereign admin for admin operations, pool keys for bridge operations).
- [x] Retry-based helpers have configurable timeouts and exponential backoff.

---

## Chunk 6: Test Scenario — No Problematic Claims

### Goals

Test the simplest recovery scenario: an invalid GER is injected, the operator (test) discovers it via aggkit logs as per the runbook, the tool diagnoses no claims and removes it. Network heals.

### Scope

- `test/e2e/removeger_test.go`: `testRemoveGER_NoProblematicClaims`

### Test Structure

```
testRemoveGER_NoProblematicClaims
├── Setup:
│   ├── Inject invalid GER via aggoracle key
│   └── Verify: GER exists on L2
├── GER detection (runbook-aligned):
│   ├── Ensure aggkit is running on env (aggsender/l2gersync will hit the invalid GER)
│   ├── Trigger or wait for log output (e.g. aggsender attempting cert with invalid GER)
│   ├── Inspect aggkit logs for runbook error patterns (AggSender or L2 GER Sync)
│   ├── Parse and extract the problematic GER hash from logs
│   └── Assert: exactly one GER detected, and it matches the injected GER (sanity check)
├── Tool — Diagnosis:
│   ├── Call removeger.Diagnose() with the **detected** GER (not the injected value directly)
│   ├── Assert: scenario == NoClaims
│   └── Assert: GER confirmed invalid on L1
├── Tool — Recovery:
│   ├── Call removeger.ExecuteRecovery()
│   ├── Assert: GER removed on L2 (globalExitRootMap == 0)
│   ├── Assert: bridge service reports removed GER event
│   └── Assert: bridge not in emergency state
└── Post-recovery:
    └── assertNetworkHealthy() — settlement continues, fresh bridge+claim works
```

### Key Implementation Details

- Generate a random GER hash (doesn't exist on L1) for injection.
- **Do not** pass the injected GER to the tool. Obtain the GER to pass to the tool only via `detectInvalidGERFromAggkitLogs()` (or equivalent) using runbook log patterns; optionally assert that the detected GER equals the injected one to ensure detection is correct.
- The tool is invoked programmatically (import the package, call `Diagnose` and `ExecuteRecovery` with the detected GER), not as a subprocess.
- The config file is prepared via `prepareToolConfig()`.
- Post-recovery network health check verifies the whole system is working.

### Acceptance Criteria

- [x] Invalid GER injection via aggoracle key succeeds.
- [x] Tool correctly diagnoses NoClaims scenario.
- [x] GER removal verified both on-chain and via bridge service API.
- [x] Post-recovery: settlements keep happening, fresh bridge+claim succeeds.

---

## Chunk 7: Test Scenario — Category A (Under-Collateralization)

### Goals

Test Category A: invalid GER injected + dummy claims made (bridge doesn't exist on L1). Operator discovers the GER via aggkit logs; tool diagnoses Category A and executes full recovery.

### Scope

- `test/e2e/removeger_test.go`: `testRemoveGER_CategoryA`

### Test Structure

```
testRemoveGER_CategoryA
├── Setup:
│   ├── Inject invalid GER via aggoracle key
│   ├── Execute dummy claim using the invalid GER
│   └── Verify: claim recorded (isClaimed == true)
├── GER detection (runbook-aligned):
│   ├── Ensure aggkit is running; trigger or wait for log output showing invalid GER
│   ├── Inspect aggkit logs for runbook error patterns; extract problematic GER hash
│   └── Assert: detected GER matches injected GER (sanity check)
├── Tool — Diagnosis:
│   ├── Call removeger.Diagnose() with the **detected** GER
│   ├── Assert: scenario == CategoryA
│   ├── Assert: 1 claim found with correct global_index
│   └── Assert: claim classified as Category A
├── Tool — Recovery:
│   ├── Call removeger.ExecuteRecovery()
│   ├── Assert: bridge was frozen then restored
│   ├── Assert: GER removed
│   ├── Assert: claim unset (isClaimed == false)
│   └── Assert: bridge not in emergency state
└── Post-recovery:
    ├── Assert: unset claim remains unset
    └── assertNetworkHealthy()
```

### Key Implementation Details

- The dummy claim execution mimics the bats test approach: use `ClaimAsset` with fabricated merkle proofs and exit roots that correspond to the injected invalid GER.
- The `global_index` for the dummy claim encodes: `mainnetFlag=true` (origin=L1), a large `depositCount` that doesn't exist on L1.
- **Obtain the GER for the tool only from log detection** (runbook patterns); do not pass the injected GER directly to `Diagnose`/`ExecuteRecovery`.
- After recovery, the claim remains permanently unset (under-collateralized claims are not re-claimed).

### Acceptance Criteria

- [x] Dummy claim execution succeeds using invalid GER.
- [x] Tool correctly diagnoses Category A.
- [x] Full Category A recovery flow executes via tool.
- [x] Claims properly unset after recovery.
- [x] Post-recovery: network heals.

---

### E2E Implementation Notes (Findings from Chunk 7)

These apply to **all** remove_ger E2E scenarios (Chunks 7–10) when the tool runs on the host against aggkit in Docker:

1. **Tool DB paths (host vs container)**  
   The tool loads config whose `PathRWData` may resolve to the base file value (e.g. `/tmp`). The aggkit container has `AggkitE2EHostDataDir` (e.g. `/tmp/aggkit-e2e-testing`) bind-mounted as `/tmp` inside the container. So the tool would open SQLite DBs under `/tmp` on the host and **not** see the same files the container writes to. **Fix:** After `remove_ger.LoadConfig()`, patch the sync DB paths so the tool uses the host data dir:
   - `cfg.BridgeL2Sync.DBPath = filepath.Join(envs.AggkitE2EHostDataDir, "bridgel2sync.sqlite")`
   - `cfg.BridgeL1Sync.DBPath = filepath.Join(envs.AggkitE2EHostDataDir, "bridgel1sync.sqlite")`
   - `cfg.L1InfoTreeSync.DBPath = filepath.Join(envs.AggkitE2EHostDataDir, "L1InfoTreeSync.sqlite")`  
   Do **not** add a duplicate `[BridgeL2Sync]` (or similar) block in the test config TOML—that causes "duplicated tables" when merging.

2. **Wait for bridge L2 sync before diagnosis**  
   After sending any claim (dummy or real), the bridge L2 sync must index it before the tool runs; otherwise `getClaimsByGER` returns no rows and the tool reports `no_claims`. Use a helper that polls the bridge service (e.g. `GetClaims` by `global_index`) until the claim appears, with a timeout (e.g. 2 minutes).

3. **Merkle proof and claim parameters**  
   GER and claims use standard merkle proof verification: with the correct root (GER) and correct proof, the proof verifies. If `ClaimAsset` reverts, the issue is likely **formatting or wrong parameters**. The contract hashes claim parameters to get the leaf; the proof must be for that same leaf. Use exact params (destination network, destination address, metadata, and proofs) that match the proof data (e.g. bats-style constants) so the leaf hashes match.

---

## Chunk 8: Test Scenario — Category B.1 (GER Mismatch, Same Index)

### Goals

Test Category B.1: invalid GER injected, claim made with correct bridge data but wrong GER. Operator discovers the GER via aggkit logs; tool classifies as B.1 and emits corrected claim events.

### Scope

- `test/e2e/removeger_test.go`: `testRemoveGER_CategoryB1`

### Test Structure

```
testRemoveGER_CategoryB1
├── Setup:
│   ├── Perform a real L1→L2 bridge (creates valid bridge data on L1)
│   ├── Wait for bridge to be indexed
│   ├── Inject invalid GER via aggoracle key
│   ├── Execute claim using invalid GER but with correct bridge data
│   └── Verify: claim recorded with invalid GER
├── GER detection (runbook-aligned):
│   ├── Ensure aggkit is running; trigger or wait for log output showing invalid GER
│   ├── Inspect aggkit logs for runbook error patterns; extract problematic GER hash
│   └── Assert: detected GER matches injected GER (sanity check)
├── Tool — Diagnosis:
│   ├── Call removeger.Diagnose() with the **detected** GER
│   ├── Assert: scenario == CategoryB1
│   ├── Assert: claim classified as B.1 (same index, different GER)
│   └── Assert: correct bridge data found on L1
├── Tool — Recovery:
│   ├── Call removeger.ExecuteRecovery()
│   ├── Assert: GER removed
│   ├── Assert: corrected claim event emitted (forceEmitDetailedClaimEvent)
│   ├── Assert: bridge service indexes corrected claim
│   └── Assert: bridge not in emergency state
└── Post-recovery:
    └── assertNetworkHealthy()
```

### Key Implementation Details

- Requires a **real** L1→L2 bridge first so valid bridge data exists on L1.
- The claim is executed on L2 using the injected (invalid) GER instead of the real one.
- **Obtain the GER for the tool only from log detection** (runbook patterns); do not pass the injected GER directly to the tool.
- The tool's `forceEmitDetailedClaimEvent` corrects the GER reference.
- No claim unsetting needed (claim remains set with corrected data).
- **Apply E2E notes above:** After the claim is sent, wait for the bridge L2 sync to index it (e.g. `waitForClaimOnBridgeService`) before running diagnosis. After `LoadConfig`, patch `cfg.BridgeL2Sync.DBPath`, `cfg.BridgeL1Sync.DBPath`, and `cfg.L1InfoTreeSync.DBPath` to the host data dir so the tool reads the same SQLite DBs as the aggkit container.

### Acceptance Criteria

- [x] Real L1→L2 bridge succeeds and is indexed.
- [x] Claim with invalid GER succeeds on L2.
- [x] Tool correctly diagnoses Category B.1.
- [x] B.1 recovery (remove GER + force emit) completes via tool.
- [x] Post-recovery: network heals.

---

## Chunk 9: Test Scenario — Category B.2 (GER and Index Mismatch)

### Goals

Test Category B.2: invalid GERs injected, claims made with wrong GER and wrong index. Operator discovers the problematic GER(s) via aggkit logs; tool performs full B.2 recovery (the most complex flow).

### Scope

- `test/e2e/removeger_test.go`: `testRemoveGER_CategoryB2`

### Test Structure

```
testRemoveGER_CategoryB2
├── Setup:
│   ├── Perform a real L1→L2 bridge (valid bridge on L1)
│   ├── Wait for bridge to be indexed and claimed normally
│   ├── Inject 2 invalid GERs via aggoracle key
│   ├── Execute 2 dummy claims using invalid GERs
│   │   (fabricated data referencing wrong deposit_counts)
│   └── Verify: both claims recorded
├── GER detection (runbook-aligned):
│   ├── Ensure aggkit is running; trigger or wait for log output showing invalid GER(s)
│   ├── Inspect aggkit logs for runbook error patterns; extract all problematic GER hashes
│   └── Assert: detected GER set matches the 2 injected GERs (sanity check)
├── Tool — Diagnosis:
│   ├── Call removeger.Diagnose() for each **detected** GER (or batch if tool supports it)
│   ├── Assert: scenario == CategoryB2
│   ├── Assert: claims classified as B.2 (different index, same content)
│   └── Assert: correct bridge data found on L1 at different deposit_count
├── Tool — Recovery:
│   ├── Call removeger.ExecuteRecovery()
│   ├── Assert: both GERs removed
│   ├── Assert: both invalid claims unset
│   ├── Assert: correct claims set (with correct global indexes)
│   ├── Assert: corrected claim events emitted
│   └── Assert: bridge not in emergency state
```

### Key Implementation Details

- Follows the bats test structure: two invalid GERs, two dummy claims, then full B.2 recovery.
- **Obtain the GER(s) for the tool only from log detection** (runbook patterns). When multiple invalid GERs are present, logs may mention one or both; the test should collect all detected GER hashes and pass them to the tool (or run diagnosis/recovery per detected GER as the runbook implies).
- Do not pass the injected GERs directly to the tool.
- **Apply E2E notes above:** Wait for the bridge L2 sync to index both claims before diagnosis. For dummy claims, use params and proofs that match the expected leaf (formatting/params must be consistent so proofs verify).

### Acceptance Criteria

- [x] Multiple invalid GER injection and dummy claim execution succeeds.
- [x] Tool correctly diagnoses Category B.2.
- [x] Full B.2 recovery flow (remove + unset + set + force emit) completes via tool.

---

## Chunk 10: Test Integration & Parallel Execution

### Goals

Wire all test scenarios together, ensure they run with proper isolation, and verify the test infrastructure is solid. All GER removal scenarios must use runbook-aligned GER detection (from aggkit logs) before invoking the tool.

### Scope

- `test/e2e/removeger_test.go`: Parent test function, parallel execution setup, shared GER-detection-from-logs helper used by all scenarios.
- `test/e2e/bridge_test.go`: Verify compatibility with shared env.

### E2E Notes (from Chunk 7)

All remove_ger scenarios that run the tool on the host against aggkit in Docker must:

- **Patch tool DB paths** after `LoadConfig`: set `cfg.BridgeL2Sync.DBPath`, `cfg.BridgeL1Sync.DBPath`, and `cfg.L1InfoTreeSync.DBPath` to `filepath.Join(envs.AggkitE2EHostDataDir, "<name>.sqlite")` so the tool reads the same SQLite files the container writes to.
- **Wait for bridge L2 sync** after sending claims (e.g. `waitForClaimOnBridgeService`) before calling the tool’s diagnosis.
- Reuse the same `prepareToolConfig` / path-patching pattern as in Category A to avoid "no_claims" or "no such table" when the tool runs.

### Detailed Changes

#### 10.1 Test structure

Since GER removal tests mutate global bridge state (emergency mode), they must run sequentially among themselves but can run in parallel with `TestBridgeFlows`:

```go
func TestRemoveGER(t *testing.T) {
    if testing.Short() {
        t.Skip("Skipping E2E test in short mode")
    }
    t.Parallel()

    // These run sequentially within TestRemoveGER
    t.Run("NoProblematicClaims", testRemoveGER_NoProblematicClaims)
    t.Run("CategoryA", testRemoveGER_CategoryA)
    t.Run("CategoryB1", testRemoveGER_CategoryB1)
    t.Run("CategoryB2", testRemoveGER_CategoryB2)
}
```

#### 10.2 Key isolation

Each test checks out dedicated keys from the pool and returns them via `defer`:

```go
l1Auth, l1Key, err := testEnv.Keys.L1Keys.Checkout()
require.NoError(t, err)
defer testEnv.Keys.L1Keys.Return(l1Key)
```

#### 10.3 Bridge state cleanup

Each GER removal test ensures the bridge is restored to normal state even on failure, via `defer`:

```go
defer func() {
    // Best-effort: restore bridge if we left it frozen
    restoreCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
    defer cancel()
    isEmergency, err := env.L2.Contracts.L2Bridge.IsEmergencyState(nil)
    if err == nil && isEmergency {
        // attempt deactivateEmergencyState
    }
}()
```

#### 10.4 Test ordering

```go
func TestEnvLoader(t *testing.T) {
    // Runs first (no t.Parallel()) — fast sanity check
}

func TestBridgeFlows(t *testing.T) {
    t.Parallel()
    // ...
}

func TestRemoveGER(t *testing.T) {
    t.Parallel()
    // Sequential subtests...
}
```

### Acceptance Criteria

- [x] All tests pass when run together: `go test ./test/e2e/ -v -count=1`
- [x] No race conditions detected with `-race` flag.
- [x] Environment starts exactly once and stops exactly once.
- [x] Each test uses its own private keys.
- [x] Bridge emergency state is properly managed (cleanup on failure).
- [x] Total execution time < 30 minutes.
- [x] Every GER removal scenario obtains the GER to pass to the tool via log inspection (runbook-aligned detection), not from the test’s injected value.

---

## Implementation Order & Dependencies

```
Chunk 0 (Env Infrastructure) ───────────────────────────┐
                                                         │
Chunk 1 (Tool: Config & CLI) ─── independent ───────────┤
                                                         │
Chunk 2 (Tool: Diagnosis) ──── depends on 1 ────────────┤
                                                         │
Chunk 3 (Tool: Recovery) ──── depends on 2 ─────────────┤
                                                         │
Chunk 4 (Tool: README) ──── depends on 3 ───────────────┤
                                                         │
Chunk 5 (Test: Helpers) ──── depends on 0, 3 ───────────┤
                                                         │
Chunk 6 (Test: No Claims) ──── depends on 5 ────────────┤
                                                         │
Chunk 7 (Test: Cat A) ──── depends on 5 ────────────────┤
                                                         │
Chunk 8 (Test: Cat B.1) ──── depends on 5 ──────────────┤
                                                         │
Chunk 9 (Test: Cat B.2) ──── depends on 5 ──────────────┤
                                                         │
Chunk 10 (Test: Integration) ── depends on 6–9 ─────────┘
```

All chunks are executed sequentially. No parallel development assumed.

---

## Open Questions / Risks

1. **Config loading reuse**: The main `config.LoadFile()` returns `*config.Config` which doesn't include `[RemoveGER]`. We need to either: (a) do a two-pass load (base config + re-parse for `[RemoveGER]`), or (b) duplicate the config loading pipeline targeting our extended struct. Option (a) is recommended for minimal duplication.

2. **Bridge service claim queries**: The tool needs to query claims by GER hash. Verify that the bridge service API supports this filter. If not, the tool may need to query the L2 bridgesync SQLite DB directly (less portable but works for local/test environments). The tool should support both paths.

3. **Dummy claim fabrication**: The bats test uses hardcoded merkle proofs and exit roots for dummy claims. We need to verify these work with the `op-pp` env or generate appropriate test data. The zero-filled rollup merkle proofs and specific local exit root proofs from the bats test may need adaptation.

4. **Certificate settlement verification**: Post-recovery health checks include waiting for certificate settlement. This may add significant wait time (~2-5 min). Consider making the timeout configurable.

5. **Sovereign admin roles**: The sovereign admin key needs the correct roles on both the bridge contract (`emergencyBridgePauser`, `emergencyBridgeUnpauser`) and the GER contract (`globalExitRootRemover`). These should already be configured in the `op-pp` snapshot but should be verified during Chunk 0.

6. **Key pool sizing**: The `op-pp` env has 20 L1 keys and ~20 L2 keys. Each GER removal scenario uses ~2 keys. With parallel execution of `TestBridgeFlows` and sequential GER tests, key contention should not be an issue.

7. **Tool invocation in tests**: Tests import the tool package directly (`tools/remove_ger`) and call `Diagnose()` / `ExecuteRecovery()` programmatically. This means the tool's core logic must be exported as a clean API, not just wired to CLI flags. The `Run` function orchestrates, but the core logic is in exported functions.
