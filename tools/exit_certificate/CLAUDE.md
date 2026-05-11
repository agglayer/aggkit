# exit_certificate tool — spec for AI assistants

## Purpose

Standalone CLI tool that generates an agglayer `Certificate` for an L2 chain exiting the Agglayer
ecosystem. It scans L2 state from genesis to a target block, computes all balances, and produces
a certificate containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens)
to the destination network.

## Package layout

```text
tools/exit_certificate/
├── cmd/main.go          — CLI entry point (urfave/cli/v2)
├── run.go               — pipeline orchestration (Run, runAll, runSingleStep)
├── config.go            — Config, Options, LoadConfig, LBT file parsing
├── types.go             — all domain types (StepXResult, LBTEntry, EOABalance, …)
├── rpc.go               — raw JSON-RPC helpers (singleRPC, concurrentBatchRPC, …)
├── worker.go            — generic worker pool (runWorkerPool)
├── hex.go               — hex/uint64 conversion utilities
├── step_0.go            — LBT generation
├── step_a.go            — address collection via debug_traceTransaction
├── step_b.go            — EOA classification + balance fetching
├── step_c.go            — SC-locked value computation
├── step_d.go            — build agglayer Certificate
├── step_e.go            — unclaimed L1→L2 deposits
├── step_f.go            — agglayer token balance verification
├── step_g.go            — NewLocalExitRoot computation
├── step_sign.go         — ECDSA certificate signing
└── parameters.json.example
```

## Pipeline

Full pipeline order: **0 → A → B → C → D → E → F → G → H → I → SIGN**

Each step reads its inputs from disk (output dir) and writes its outputs to disk. The
`runAll` path passes data in memory directly; `runSingleStep` always loads from disk.

### Step 0 — Generate LBT

- **Trigger:** runs unless `lbtFile` is set and the file exists.
- **Does:** scans L2 bridge `NewWrappedToken` events, fetches `totalSupply` per token at `targetBlock`, computes unlocked native balance.
- **Output:** `step-0-lbt.json` (`[]LBTEntry`)

### Step A — Collect addresses

- **RPC:** `eth_getBlockByNumber` (headers, `false`) → tx hashes; then `debug_traceTransaction` with `prestateTracer`+`diffMode` per hash.
- **Output:** `step-a-addresses.json` (`[]common.Address`), `step-a-failed-traces.json` (`[]common.Hash`)
- **Option:** `continueOnTraceError=true` skips failed traces instead of aborting.

### Step B — EOA balance checking

Three phases:

1. `eth_getCode` → classify each address as EOA or contract
2. `eth_getBalance` for all EOAs at `targetBlock`
3. `balanceOf(address)` per wrapped token × per EOA (token list from LBT)

- **Output:** `step-b-eoa-balances.json` (`[]EOABalance`), `step-b-accumulated.json` (`[]AccumulatedBalance`), `step-b-contract-addresses.json` (`[]common.Address`)

### Step C — SC-locked value

- **Formula:** `SC_locked = LBT_totalSupply − accumulated_EOA_balances` per token.
- **Output:** `step-c-sc-locked-values.json` (`[]SCLockedValue`)

### Step D — Build certificate

Creates the `*agglayertypes.Certificate` with `BridgeExit` entries:

- One per (EOA, token) pair with non-zero balance → destination is the EOA address on `destinationNetwork`.
- One per token with SC-locked value > 0 → destination is `exitAddress` on `destinationNetwork`.

- **Output:** `step-d-exit-certificate.json`

### Step E — Unclaimed L1→L2 deposits

- **Requires:** `l1RpcUrl` (skipped otherwise).
- Scans L1 `BridgeEvent` events targeting L2 network, checks each deposit against `isClaimed` on L2 bridge.
- Adds unclaimed deposits as both `bridge_exits` and `imported_bridge_exits` (with `claim_data: null`).
- **Output:** `step-e-unclaimed-bridges.json` (`[]L1Deposit`), `step-e-exit-certificate.json`

### Step F — Agglayer balance verification

- **Requires:** `agglayerAdminURL` in options (skipped otherwise).
- Calls `admin_getTokenBalance` on the agglayer admin RPC and compares per-token totals against the certificate.
- Mismatches are warnings, not errors — step never aborts the pipeline.
- **Output:** `step-f-token-balances.json`, `step-f-checks.json` (`[]TokenBalanceCheck`)


### Step G — Compute NewLocalExitRoot (shadow-fork)

Computes the correct `NewLocalExitRoot` by replaying every `bridge_exit` from the certificate
against a shadow-fork of the L2 chain, then reading the resulting `localExitRoot` storage slot
directly from the forked bridge contract.

**Why shadow-fork instead of local Merkle math:**
The `AgglayerBridge` contract maintains its own Local Exit Tree internally. Recomputing it
off-chain requires matching the exact leaf encoding and tree implementation. Driving the actual
contract on a fork eliminates that divergence risk.

**Approach:**

1. **Fork L2 at `targetBlock`** — spin up an Anvil instance (`anvil --fork-url <l2RpcUrl>
   --fork-block-number <targetBlock>`). Anvil is a required external dependency for this step.
2. **Impersonate a funded sender** — use `anvil_impersonateAccount` + `anvil_setBalance` so
   `bridgeAsset` calls can be sent without a real private key.
3. **Replay bridge exits** — for each `BridgeExit` in the certificate (`bridge_exits` list),
   send an `eth_sendTransaction` calling
   [`bridgeAsset`](https://github.com/agglayer/agglayer-contracts/blob/v12.2.3/contracts/AgglayerBridge.sol)
   on the L2 bridge contract with the same parameters:
   - `destinationNetwork` — from the `BridgeExit`
   - `destinationAddress` — from the `BridgeExit`
   - `amount` — from the `BridgeExit`
   - `token` — derived from `TokenInfo.OriginTokenAddress` / `OriginNetwork`
   - `forceUpdateGlobalExitRoot = false`
   - `permitData = ""`
4. **Read `localExitRoot`** — after all calls, call the `localExitRootManager().localExitRoot()`
   view function (or read the storage slot directly) on the bridge contract.
5. **Return result** — assign the result to `Certificate.NewLocalExitRoot` and return it to the
   caller. Saving `step-g-new-local-exit-root.json` is the orchestrator's responsibility, not Step G's.

**Anvil dependency:** the tool shells out to `anvil` (from the Foundry toolchain). If `anvil`
is not in `$PATH`, Step G must fail with a clear error message pointing to
`https://getfoundry.sh`.

**Empty bridge exits:** if the certificate has no `bridge_exits`, skip the fork entirely and
use the canonical `bridgesynctypes.EmptyLER` value (no Anvil needed).

- **Output:** `step-g-new-local-exit-root.json` (`StepGResult`)

### Step H — Fetch PreviousLocalExitRoot

- **Requires:** `options.agglayerRpcUrl` — step H is mandatory and fails if not set.
- Calls `interop_getNetworkInfo` with `l2NetworkId` on the agglayer JSON-RPC and reads `settled_ler`.
- If no certificate has been settled yet (`settled_ler` is null), `PreviousLocalExitRoot` is zero.
- **Output:** `step-h-previous-local-exit-root.json` (`StepHResult`)

### Step I — Assemble final certificate

- Reads `step-e-exit-certificate.json` (base from E), `step-g-new-local-exit-root.json`, and
  `step-h-previous-local-exit-root.json` (optional).
- Sets `Certificate.NewLocalExitRoot` from G and `Certificate.PrevLocalExitRoot` from H.
- **Output:** `exit-certificate-final.json` (updated with both roots)

### Step SIGN — Sign certificate

- **Requires:** `signerConfig.Method` (skipped in `all` mode when not set; error in single-step mode).
- Uses the same `signertypes.SignerConfig` as aggsender's `AggsenderPrivateKey`. JSON format: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}` (flat, mirrors the TOML inline table).
- Fetches `eth_chainId`, loads keystore via `go_signer`, hashes the certificate with `validator.HashCertificateToSign`, signs, and wraps in `AggchainDataMultisig`.
- **Output:** `exit-certificate-signed.json`

### Step SUBMIT — Send certificate to agglayer

- **Not part of `runAll`** — must be triggered explicitly with `--step submit`.
- **Requires:** `options.agglayerGrpcUrl` — the agglayer gRPC endpoint.
- Loads `exit-certificate-signed.json`, creates an agglayer gRPC client, and calls `SendCertificate`.
- **Output:** `step-submit-result.json` (`StepSubmitResult` with `certificateHash`)

## Key types (`types.go`)

| Type | Description |
| --- | --- |
| `LBTEntry` | LBT row: wrapped token address, origin network/token, total supply |
| `WrappedToken` | Like `LBTEntry` but without the balance field |
| `EOABalance` | Per-address: ETH balance + slice of `EOATokenBalance` |
| `AccumulatedBalance` | Sum across all EOAs for a single token |
| `SCLockedValue` | LBT total − EOA accumulated, per token |
| `L1Deposit` | Parsed `BridgeEvent` log from L1 |
| `TokenBalanceCheck` | Step F comparison: certificate amount vs agglayer amount |
| `StepGResult` | `NewLocalExitRoot` hash + bridge exit count |
| `StepHResult` | `PreviousLocalExitRoot` from agglayer |
| `StepSubmitResult` | `certificateHash` returned by the agglayer after submission |

## Config fields (`config.go`)

Required: `l2RpcUrl`, `l2BridgeAddress`, `targetBlock`.

Defaults applied by `LoadConfig`:

- `l1BridgeAddress` defaults to `l2BridgeAddress`
- `l2NetworkId` defaults to `1`
- `options.blockRange` = 5000, `concurrencyLimit` = 20, `rpcBatchSize` = 200
- `options.abortOnGenesisBalance` = `true` — abort if any address has a non-zero ETH balance at block 0 (genesis preload guard). Set `false` only for Kurtosis/test environments.
- Relative paths in `lbtFile`, `options.outputDir`, and `signerConfig.Path` resolve from the directory containing the config file.

`signerConfig` uses `signertypes.SignerConfig` (same type as aggsender's `AggsenderPrivateKey`). The JSON format is flat — `Method`, `Path`, `Password` are top-level keys (matching the TOML inline table style). Parsed by `parseSignerConfig` which splits `Method` out and puts the rest into `Config map[string]any`.

## RPC layer (`rpc.go`, `worker.go`)

- All RPC is plain JSON-RPC over HTTP — no go-ethereum client.
- `concurrentBatchRPC` sends calls in `rpcBatchSize`-sized batches, dispatches batches with a semaphore of size `concurrencyLimit`.
- `runWorkerPool` is a generic fan-out + fan-in over a slice of inputs with a configurable worker count.
- Retry logic uses `defaultRetries` (3) for `singleRPC`.
- `rpcDelayMs` inserts a sleep between batches for rate-limiting.

## Invariants and gotchas

- **Output dir:** All intermediate files land in `options.outputDir` (default `./output` relative to the config file). The dir is created automatically.
- **`parameters.json` and `output/` are git-ignored** — never commit them.
- **File chain:** Step D → `step-d-exit-certificate.json`; Step E → `step-e-exit-certificate.json` (adds unclaimed deposits); Step I → `exit-certificate-final.json` (sets `NewLocalExitRoot` from G and `PrevLocalExitRoot` from H). Always submit `exit-certificate-final.json` (or the signed variant).
- **LBT resolution:** `resolveOrGenerateLBT` → if `lbtFile` is set and exists, use it and skip Step 0; if set but missing, fall back to Step 0 with a warning; if not set, always run Step 0.
- **Step F reads from `step-d-exit-certificate.json`**, not the final certificate — it verifies the base L2 balances before the E/G additions.
- **SC-locked value can be negative** when genesis state was pre-loaded or the LBT is stale — `abortOnGenesisBalance=true` catches this early.
- **`debug_traceTransaction` must be available** on the L2 RPC (Step A). Archive node required.
- **Step G requires Anvil** (`anvil` binary in `$PATH`, from the Foundry toolchain). The step fails fast with a clear error if it is missing.
- **FEP chains are not supported.** Only Pessimistic Proof certificates are generated.
- **`SetClaim` and `UpdatedUnsetGlobalIndexHashChain` events are not handled** — value from those flows may be missing.


## Testing

Run from the repo root:

```bash
go test ./tools/exit_certificate/...
```

Or a single test:

```bash
go test -v -run TestName ./tools/exit_certificate/
```

Test files: `*_test.go` beside each step file. Use `require` (not `assert`). No mocks for the RPC layer — tests that hit network are integration tests in `integration_test.go` and require a live node.

## Build

```bash
cd tools/exit_certificate
go build -o exit-certificate ./cmd
```

## Coding rules

- **Contract binding**: Use the library "github.com/0xPolygon/cdk-contracts-tooling/contracts/". Here you can find all the contract, for instance, for bridge you can use: "github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
