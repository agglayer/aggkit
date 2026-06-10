# exit-certificate

Generate exit certificates for a chain migration — scans L2 state, computes balances, and builds a certificate that bridges all value back to L1.

## Overview

**What it does:** The `exit-certificate` CLI scans an L2 chain from genesis to a target block, discovers all addresses with value, and produces an agglayer `Certificate` containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens) to the destination network. The certificate uses the native agglayer types directly — no conversion step is needed before submission.

**When to use it:** Use when an aggchain needs to exit the Agglayer ecosystem. The tool ensures all value on the L2 is accounted for and packaged into a single certificate.

## Known limitations

- **FEP (Finality by Execution Proof) is not supported.** The tool only handles Pessimistic Proof (PP) certificates. Chains running FEP mode cannot use this tool as-is.
- **`SetClaim` and `UpdatedUnsetGlobalIndexHashChain` events are not supported.** Transactions that emit these events on the bridge contract ([see contracts](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3)) are not detected or accounted for. Value associated with these flows may be missing from the generated certificate.

## Quick start

```bash
cd tools/exit_certificate

# Build
go build -o exit-certificate ./cmd

# Create your config from the example
cp parameters.json.example parameters.json

# Edit parameters.json with your RPC URLs, bridge address, etc.
# Then run the tool
./exit-certificate --config parameters.json
```

## Building

From `tools/exit_certificate/`:

```bash
go build -o exit-certificate ./cmd
```

## Config file

The tool uses a standalone config file in **JSON or TOML** format — the format is selected by the
file extension (`.toml` is parsed as TOML, anything else as JSON). Copy the example and fill in your
values:

```bash
# JSON
cp parameters.json.example parameters.json

# or TOML
cp parameters.toml.example parameters.toml
```

The field names are identical in both formats. Pass whichever you created with `--config`.

> **Note:** `parameters.json`, `parameters.toml` and the `output/` directory are git-ignored — they are not committed to the repository.

### Config fields

| Field | Required | Description |
| :---: | :------: | :---------: |
| `l2RpcUrl` | Yes | L2 JSON-RPC endpoint. Must support `debug_traceTransaction` for Step A. |
| `l1RpcUrl` | Yes* | L1 JSON-RPC endpoint. Required by Step E (unclaimed deposit detection) and Step I (`L1InfoTreeLeafCount`). Without it Step E is silently skipped and Step I fails — the resulting certificate will be incomplete. |
| `l2BridgeAddress` | Yes | L2 bridge contract address. |
| `l1BridgeAddress` | No | L1 bridge contract address. Defaults to `l2BridgeAddress`. |
| `l2NetworkId` | No | L2 network ID. Defaults to `1`. |
| `targetBlock` | No | Target block for state capture. Accepts a decimal number (`"21000000"`), hex (`"0x1406f40"`), or a finality keyword: `"LatestBlock"`, `"FinalizedBlock"`, `"SafeBlock"`, `"PendingBlock"`. An optional negative offset can be appended (e.g. `"LatestBlock/-10"` = ten blocks before latest). Omitting the field or setting it to `""` defaults to `"LatestBlock"`. The keyword is resolved to a concrete block number at the start of Step 0 and saved to `step-0-l2_target_block.json`. All subsequent steps use that fixed number. |
| `exitAddress` | Yes | Address that receives SC-locked value exits on `destinationNetwork`. **Must be an address whose private key you control**, and **must not be the zero address** (`0x00…00`) — `LoadConfig` rejects both an empty value and the zero address, since these funds can only be recovered by signing from this address. **A multisig (e.g. a Gnosis Safe) is strongly recommended** over a single EOA, so that recovering these funds does not depend on a single private key. |
| `destinationNetwork` | No | Destination network for bridge exits. Defaults to `0` (L1). |
| `sovereignRollupAddr` | Yes* | Address of the `aggchainbase` contract on L1. Required by Step CHECK (network type and threshold verification). |
| `l1GlobalExitRootAddress` | Yes* | Address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. |
| `signerConfig` | No | Signer configuration object for Step SIGN. Same format as aggsender's `AggsenderPrivateKey`. Example: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}`. |

> **\*Required for specific steps:** `l1RpcUrl` is required by Steps E and I; `sovereignRollupAddr` is required by Step CHECK; `l1GlobalExitRootAddress` is required by Step I. Without them those steps fail.

### Options

| Field | Default | Description |
| :---: | :-----: | :---------: |
| `blockRange` | `5000` | Block range per `eth_getLogs` query (Steps 0, B, E). |
| `stepAWindowSize` | `5000` | Number of blocks loaded into memory per iteration in Step A (address collection via `debug_traceTransaction`). Set independently when trace calls need a different chunk size than log queries. |
| `concurrencyLimit` | `20` | Max concurrent RPC requests. |
| `rpcBatchSize` | `200` | Max calls per JSON-RPC batch request. |
| `rpcDelayMs` | `0` | Delay between RPC batches (rate limiting). |
| `outputDir` | `./output` | Directory for intermediate and final output files. Relative paths resolve from the config file directory. |
| `l1StartBlock` | `0` | L1 block to start scanning from (Step E). |
| `l2StartBlock` | `0` | L2 block to start scanning from (Step A). Useful when genesis activity can be skipped. |
| `agglayerAdminURL` | `""` | Agglayer admin RPC endpoint. Required for Step F (Step F errors if it runs without this set). To skip Step F when no admin endpoint is available, set `useAgglayerAdminToStepFCheck: false`. |
| `agglayerAdminToken` | `""` | Bearer token for authenticating requests to `agglayerAdminURL`. Required when the admin endpoint is protected by Google Cloud IAP. See [Authenticating with IAP](#authenticating-with-iap) for how to obtain it. |
| `agglayerClient` | `{}` | Agglayer gRPC client config (same as aggsender's `agglayer.ClientConfig`). Set at least `agglayerClient.GRPC.URL`. Required for Steps H, SUBMIT, and WAIT. |
| `useAgglayerAdminToStepFCheck` | `true` | When `true` (default), Step F runs: it queries the agglayer admin API (`admin_getTokenBalance`) and verifies the per-token balances against the certificate and LBT. When `false`, Step F is skipped entirely (no agglayer admin query, no balance check) — useful when no admin endpoint is available. |
| `ignoreGenesisBalance` | `false` | When `false` (default), Step B aborts if any address has a non-zero ETH balance at block 0 (genesis preload guard). Set `true` to downgrade it to a warning, only for Kurtosis or test environments. |
| `ignoreOnTraceError` | `false` | When `true`, Step A skips transactions whose `debug_traceTransaction` call fails instead of aborting. Failed tx hashes are saved to `step-a-failed-traces.json`. |
| `ignoreBalanceMismatch` | `false` | When `true`, Step F does not abort the pipeline on token balance mismatches. Instead it produces a capped certificate (`step-f-capped-certificate.json`) where each token's bridge exits are proportionally scaled down to `min(agglayer, lbt)`. See [Step F](#step-f--agglayer-token-balance-verification) for details. |
| `ignoreUnclaimed` | `false` | When `true`, Step E detects and logs unclaimed deposits but leaves the certificate unchanged. When `false` (default), any unclaimed asset deposit causes the pipeline to error. |
| `bridgeServiceURL` | `""` | Base URL of the bridge service REST API. When set, Step E cross-checks its unclaimed deposit set against the bridge service and returns an error on any discrepancy. |
| `bridgeServiceType` | `"aggkit"` | Bridge service API flavour. `"aggkit"` uses `GET /bridge/v1/bridges` (aggkit bridge service); `"zkevm"` uses `GET /pending-bridges` (zkevm-bridge-service). |
| `extraErc20Contracts` | `[]` | Optional list of ERC-20 contract addresses to decompose into individual holder balances in Step B3. For each address the tool calls `balanceOf` for every EOA collected in Step A. Example: `["0xAbc...123", "0xDef...456"]`. |

### Important configuration notes

**`l1RpcUrl` — required in practice**

Although marked optional, `l1RpcUrl` is needed for Step E (unclaimed deposit detection) and Step I (`L1InfoTreeLeafCount`). In a real exit scenario you should always set it. Without it, Step E is silently skipped and the certificate may be missing unclaimed L1→L2 deposits.

**`exitAddress` — required, keep the private key**

SC-locked value (tokens held in smart contracts) is bridged to `exitAddress` on the destination network. The field is **mandatory**: `LoadConfig` errors if it is missing or set to the zero address (`0x00…00`). Use an address **whose private key you control** — once the certificate is settled, those funds can only be recovered by signing transactions from that address. If the key is lost, the value is permanently inaccessible.

For this reason, **a multisig wallet (e.g. a [Gnosis Safe](https://safe.global/)) is strongly recommended** over a single EOA. Because these funds can only ever be recovered by signing from `exitAddress`, spreading control across several signers removes the single point of failure: no single lost or compromised key can lock up or steal the exited value.

**`agglayerClient` — required for Steps H, SUBMIT, and WAIT**

Uses the same `agglayer.ClientConfig` struct as aggsender. At minimum provide the gRPC URL; unset fields default to the same values used by aggsender:

```json
"agglayerClient": {
  "GRPC": {
    "URL": "localhost:50051"
  }
}
```

Full example with all fields (timeouts accept Go duration strings: `"5s"`, `"1m"`, etc.):

```json
"agglayerClient": {
  "GRPC": {
    "URL": "localhost:50051",
    "RequestTimeout": "30s",
    "MinConnectTimeout": "5s",
    "UseTLS": false,
    "Retry": {
      "MaxAttempts": 3,
      "InitialBackoff": "1s",
      "MaxBackoff": "10s",
      "BackoffMultiplier": 2.0
    }
  }
}
```

**`signerConfig` — required to sign and submit**

Step SIGN requires a signer configuration. Use the same JSON format as aggsender's `AggsenderPrivateKey`:

```json
"signerConfig": {
  "Method": "local",
  "Path": "/path/to/keystore.json",
  "Password": "your-password"
}
```

Without this field, Step SIGN is skipped when running the full pipeline and you will need to sign manually.

The example above uses a local keystore file. Other backends (GCP KMS, AWS KMS, etc.) are also supported. For the full list of signer methods and their configuration options see the [go_signer](https://github.com/agglayer/go_signer) repository.

#### Authenticating with IAP

When `agglayerAdminURL` points to a production endpoint protected by Google Cloud IAP (Identity-Aware Proxy), requests must include a Bearer token. Obtain it with `gcloud`:

```bash
export JWT=$(gcloud auth print-identity-token \
  --impersonate-service-account=<SERVICE_ACCOUNT_EMAIL> \
  --audiences=<AUDIENCE> \
  --include-email)
```

Then set `agglayerAdminToken` in your config to the value of `$JWT`.

Environment-specific values:

| Environment | `SERVICE_ACCOUNT_EMAIL` | `AUDIENCE` | `agglayerAdminURL` |
| ----------- | ----------------------- | ---------- | ------------------ |
| spec | `agglayer-spec-admin-iap@prj-polygonlabs-cdk-dev.iam.gserviceaccount.com` | `593545957356-gnjisnf3rad64es8uh4isj8lindaa05f.apps.googleusercontent.com` | `https://admin-agglayer-spec.polygon.technology` |
| bali | `agglayer-bali-admin-iap@prj-polygonlabs-cdk-dev.iam.gserviceaccount.com` | `593545957356-hi10sk8kqkm8aee4qe6n0rbad4krjla0.apps.googleusercontent.com` | `https://admin-agglayer-dev.polygon.technology` |
| cardona | `agglayer-cardona-admin-iap@prj-polygonlabs-cdk-test.iam.gserviceaccount.com` | `515506276380-m2s53r0hfd0ppfjh7kdv92rc1g3taet8.apps.googleusercontent.com` | `https://admin-agglayer-test.polygon.technology` |
| mainnet | `agglayer-mainnet-admin-iap@prj-polygonlabs-cdk-prod.iam.gserviceaccount.com` | `837347663102-9et4sc5kokg8rdbrehcut9bl3qpg2gc6.apps.googleusercontent.com` | `https://admin-agglayer.polygon.technology` |

The IAP token expires after ~1 hour. If Step F returns an `Invalid IAP credentials` error, regenerate the token and update the config.

#### Options to skip failing checks

Some options let you continue past conditions that would otherwise abort the pipeline. Use them with care:

| Option | Default | When to change |
| ------ | ------- | -------------- |
| `ignoreOnTraceError` | `false` | Set to `true` if some transactions fail `debug_traceTransaction` (e.g. the node does not have full archive traces for old blocks). Failed hashes are saved to `step-a-failed-traces.json` — review them to confirm the missing value is acceptable. |
| `ignoreGenesisBalance` | `false` | Set to `true` only for Kurtosis or test environments where addresses are pre-funded at genesis. In production, a non-zero genesis balance indicates a misconfiguration, so leave it `false` to abort. |
| `ignoreUnclaimed` | `false` | Set to `true` to proceed even when unclaimed L1→L2 asset deposits are detected. The deposits are logged with a warning but the certificate is left unchanged. Only safe if you have independently verified the unclaimed deposits are negligible or already handled. |

## Commands

### Run full pipeline

```bash
./exit-certificate --config parameters.json
```

Runs all steps sequentially: CHECK → 0 → A → B → C → D → E → F → G → H → I → SIGN (if `signerConfig` is set).

| Step | Name | What it does |
| :--: | ---- | ------------ |
| CHECK | Verify prerequisites | Checks Anvil, L1 RPC, network type (PP only), threshold = 1, no custom gas token. |
| 0 | Generate LBT | Resolves `targetBlock` to a concrete block number, then scans `NewWrappedToken` events and fetches `totalSupply` per wrapped token at that block. |
| A | Collect addresses | Traces every L2 transaction via `debug_traceTransaction` and collects all addresses that touched state. |
| B | EOA balances + ERC-20 detection | B1: classifies addresses and fetches ETH/token balances for EOAs. B2: probes contracts for the ERC-20 interface and checks if they hold tracked wrapped tokens. B3: fetches holder breakdowns for `extraErc20Contracts` (skips any already processed by B2). |
| C | SC-locked value | Computes value locked in contracts: `SC_locked = LBT_totalSupply − EOA_accumulated` per token. |
| D | Build certificate | Creates the `Certificate` with `BridgeExit` entries for every (EOA, token) pair and every token with SC-locked value. |
| E | Unclaimed deposits | Scans L1 for unclaimed `BridgeEvent` deposits targeting L2. Message deposits (`leaf_type=1`) are saved to `step-e-unclaimed-messages.json` and never added to the certificate. Asset deposits (`leaf_type=0`): if none are found the certificate is passed through unchanged; if any are found and `ignoreUnclaimed=true` they are logged but the certificate remains unchanged; if found and `ignoreUnclaimed=false` the pipeline errors (Merkle proof support not yet implemented). Optionally cross-checks against a bridge service. |
| F | Balance verification | Three-way comparison (LBT, agglayer, certificate) per token. Aborts on mismatch by default; with `ignoreBalanceMismatch=true` produces a proportionally capped certificate. Skipped entirely when `useAgglayerAdminToStepFCheck=false`. |
| G | NewLocalExitRoot | Shadow-forks L2 at `targetBlock` via Anvil, replays all bridge exits, and reads the resulting `localExitRoot` from the forked bridge contract. |
| H | PreviousLocalExitRoot | Fetches `settled_ler` from the agglayer gRPC to obtain the previous LER and the next certificate height. |
| I | Assemble final cert | Applies `NewLocalExitRoot` (G), `PreviousLocalExitRoot` + height (H), bridge exit metadata, and `L1InfoTreeLeafCount` (from the latest `UpdateL1InfoTreeV2` event on L1). |
| SIGN | Sign certificate | Hashes the certificate and signs it with the configured keystore; wraps the signature in `AggchainDataMultisig`. |
| SUBMIT | Send to agglayer | Sends the signed certificate to the agglayer via gRPC. **Not part of the default pipeline.** |
| WAIT | Wait for settlement | Polls `GetCertificateHeader` every 5 s until the certificate is `Settled` or `InError`. **Not part of the default pipeline.** |

Steps SUBMIT and WAIT are **not** part of the default pipeline — they must be triggered explicitly.

### Run one or more steps

```bash
# Single step
./exit-certificate --config parameters.json --step h

# Multiple steps (comma-separated, run in the given order)
./exit-certificate --config parameters.json --step h,i,sign
./exit-certificate --config parameters.json --step "sign, submit"
```

Each step reads its dependencies from the output directory (files written by prior steps).
Spaces around commas are ignored. Execution stops at the first step that fails.

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Step(s) to run: `all`, a single step name, or a comma-separated list (e.g. `h,i,sign`). Valid names: `check`, `0`, `a`, `a1`, `a2`, `b`, `b1`, `b2`, `b3`, `c`–`i`, `sign`, `submit`, `wait`. The aliases `a` and `b` expand to their sub-steps. |
| `--verbose` | — | `false` | Enable debug logging. Without this flag only `info`, `warn` and `error` messages are shown. |

## Pipeline steps

### Step CHECK — Verify prerequisites

Runs automatically as the first step of the full pipeline. Can also be run individually:

```bash
./exit-certificate --config parameters.json --step check
```

All checks run regardless of individual failures; a combined error lists every failed check.

1. **Anvil installed** — `anvil` must be in `$PATH` (required by Step G2 only when `options.verifyNewLocalExitRootUsingShadowFork=true`). Fails with a clear error pointing to [getfoundry.sh](https://getfoundry.sh) if missing.
2. **L1 RPC reachable** — dials `l1RpcUrl` and calls `eth_blockNumber`. Fails if not set or unreachable.
3. **L2 network ID matches bridge** — calls `NetworkID()` on the L2 bridge contract and verifies it matches `l2NetworkId` in config.
4. **`sovereignRollupAddr` is set** — required; fails if zero address.
5. **Network type is PP** — queries `AGGCHAINTYPE()` on the `aggchainbase` contract at `sovereignRollupAddr` on L1. FEP is not supported. Only runs if checks 2 and 4 passed.
6. **Threshold is 1** — queries the multisig threshold. Fails if > 1. Also verifies the bridge address on the contract matches config. Logs all committee signers and their URLs. Only runs if checks 2 and 4 passed.
7. **No custom gas token** — calls `gasTokenAddress()`/`gasTokenNetwork()` on the L2 bridge. Fails if a non-zero gas token is configured (not supported).

**Output:** `step-check-result.json`

### Step 0 — Generate LBT (Local Balance Tree)

#### Target block resolution

The `targetBlock` config field accepts a finality keyword, an optional offset, or a concrete block number. Step 0 resolves it to a `uint64` before doing any work:

| `targetBlock` value | How it is resolved |
| ------------------- | ------------------ |
| `""` or omitted | Equivalent to `"LatestBlock"` |
| `"LatestBlock"` | `eth_getBlockByNumber("latest")` on the L2 RPC |
| `"FinalizedBlock"` | `eth_getBlockByNumber("finalized")` on the L2 RPC |
| `"SafeBlock"` | `eth_getBlockByNumber("safe")` on the L2 RPC |
| `"PendingBlock"` | `eth_getBlockByNumber("pending")` on the L2 RPC |
| `"LatestBlock/-10"` | Latest block number minus 10 |
| `"21000000"` / `"0x1406f40"` | Used directly, no RPC call needed |

The resolved number is written to `step-0-l2_target_block.json` and used as a fixed reference by all subsequent steps (A, B, G). When running individual steps the file must exist (produced by a prior Step 0 run).

#### LBT generation

After resolution, Step 0 scans the L2 bridge contract for `NewWrappedToken` events and fetches the `totalSupply` of each wrapped token at the resolved block. It also applies any `SetSovereignTokenAddress` overrides (remapped wrapped addresses), computes the unlocked native token balance, and checks for a WETH entry if the chain has a custom gas token.

This step replaces the need for the external [`getLBT`](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3/tools/getLBT) tool.

**Output:** `step-0-l2_target_block.json` (resolved block number), `step-0-lbt.json` (LBT entries)

### Step A — Collect addresses

Scans all blocks from `l2StartBlock` to `targetBlock` and collects every address that participated in any transaction, using `debug_traceTransaction` (prestateTracer, diffMode).

1. Scan — `eth_getBlockByNumber` (headers only, `false`) across all blocks → tx hashes are included directly in the response
2. Trace — `debug_traceTransaction` (prestateTracer, diffMode) per hash to extract pre/post state addresses

**Output:** `step-a-addresses.json`

### Step B — EOA balance checking + ERC-20 detection

Step B runs three sub-steps in sequence: B1, B2, and B3. Running `--step b` executes all three.

#### Step B1 — EOA classification and balance fetching

Classifies addresses as EOA vs contract, then queries ETH balance and every wrapped-token balance at `targetBlock` for all EOAs. The wrapped token list comes from the LBT data (Step 0).

**Phases:**

1. `eth_getCode` to classify EOA vs contract
2. `eth_getBalance` for all EOAs
3. `balanceOf` calls per token × per EOA (token list from LBT)

**Output:** `step-b-eoa-balances.json`, `step-b-accumulated.json`, `step-b-contract-addresses.json`

#### Step B2 — ERC-20 detection in contracts

Probes every contract address for the ERC-20 interface by calling `totalSupply()`. For each ERC-20 found, checks whether it holds any of the tracked wrapped tokens:

- Holds at least one tracked token → **DetectedERC20** (relevant to the certificate)
- Holds none → **DiscardedERC20** (no tracked value locked inside)

**Output:** `step-b2-detected-erc20s.json`, `step-b2-discarded-erc20s.json`

#### Step B3 — Extra ERC-20 holder decomposition

Fetches the per-EOA token balance for each contract listed in `options.extraErc20Contracts`. These are ERC-20 contracts that should be decomposed into individual holder balances regardless of whether they were discovered by Step B2.

Skipped automatically when `options.extraErc20Contracts` is empty.

**Output:** `step-b3-erc20-holders.json`

### Step C — SC-locked value extraction

Computes value locked in smart contracts using: `SC_locked = LBT_totalSupply - accumulated_EOA_balances`. Uses the LBT data (Step 0) for total supply per token.

**Output:** `step-c-sc-locked-values.json`

### Step D — Build exit certificate

Creates the agglayer `Certificate` with `BridgeExit` entries for:

1. Every (EOA, token) pair with a non-zero balance → exits to the same address on the destination network
2. Every token with SC-locked value → exits to `exitAddress` on the destination network

**Output:** `step-d-exit-certificate.json`

### Step E — Unclaimed L1→L2 bridge deposits

Scans L1 for `BridgeEvent` events targeting the L2 and checks each deposit against `isClaimed` on the L2 bridge. Deposits are split by leaf type:

- **Message deposits (`leaf_type=1`)** — never added to the certificate. Saved to `step-e-unclaimed-messages.json` for review.
- **Asset deposits (`leaf_type=0`)** — three outcomes depending on what is found:
  - **No unclaimed asset deposits** → step completes, certificate passed through unchanged.
  - **Unclaimed asset deposits found + `ignoreUnclaimed=true`** → deposits are detected, amounts logged with a warning, certificate left unchanged.
  - **Unclaimed asset deposits found + `ignoreUnclaimed=false`** → pipeline **errors**. Adding unclaimed deposits to the certificate requires Merkle proofs which are not yet implemented.

When `bridgeServiceURL` is set, Step E compares its detected unclaimed set against the bridge service's pending-bridges and errors if the sets differ. Supports both aggkit (`/bridge/v1/bridges`) and zkevm-bridge-service (`/pending-bridges`) via `bridgeServiceType`.

Requires `l1RpcUrl`.

**Output:** `step-e-unclaimed-bridges.json`, `step-e-unclaimed-messages.json`, `step-e-exit-certificate.json`

### Step F — Agglayer token balance verification

Queries the agglayer admin API (`admin_getTokenBalance`) for the L2 network and performs a **three-way comparison** per token:

| Source | What it represents |
| ------ | ------------------ |
| **LBT** (Step 0) | `totalSupply` of the wrapped token at `targetBlock` — what the L2 contract holds |
| **Agglayer** | What the agglayer believes is locked for this L2 network |
| **Certificate** | Sum of all `BridgeExit` amounts for that token |

All three values must be equal. Each token is logged with ✅ or ❌:

```text
✅ (network=1 addr=0xabc...): lbt=1000  certificate=1000  agglayer=1000
❌ MISMATCH (network=1 addr=0xdef...): lbt=800  certificate=1000  agglayer=900
```

**If mismatches are found:**

- By default Step F **aborts the pipeline** with an error.
- Set `options.ignoreBalanceMismatch: true` to continue instead. In that case the step produces `step-f-capped-certificate.json`, where each mismatched token's bridge exits are proportionally scaled down to `min(agglayer, lbt)`. Subsequent steps in the pipeline (G, H, I) automatically use this capped certificate.

When running Step G individually it also prefers `step-f-capped-certificate.json` over `step-e-exit-certificate.json` if the capped file exists (logged with ⚠️).

LBT data comes from `step-0-lbt.json`. If not available, the comparison falls back to two-way (certificate vs agglayer only).

Skipped entirely when `options.useAgglayerAdminToStepFCheck` is `false`. Requires `agglayerAdminURL` to be set in options (errors otherwise).

**Reads:** `step-d-exit-certificate.json`, `step-0-lbt.json`

**Output:** `step-f-token-balances.json`, `step-f-checks.json`, `step-f-capped-certificate.json` *(only when mismatches exist and `ignoreBalanceMismatch=true`)*

### Step G — Compute NewLocalExitRoot

Split into **G1** (sync the L2 bridge history from genesis up to the target block into a lite DB, resolving the shadow-fork block) and **G2** (compute the `new_local_exit_root`). By default (`options.verifyNewLocalExitRootUsingShadowFork=true`) G2 replays every `bridge_exit` against a shadow-fork of the L2 chain via [Anvil](https://getfoundry.sh), reorders the certificate to the on-chain deposit order, and verifies the lite exit tree root against the forked contract's `getRoot()`. Set the option to `false` to instead compute the root **off-chain** from the lite exit tree (G1's bridges + the certificate's exits, in order) without Anvil — faster, but it trusts the off-chain leaf encoding/metadata.

**Anvil is required in the default shadow-fork mode** (`anvil` binary in `$PATH`); the off-chain mode (`verifyNewLocalExitRootUsingShadowFork=false`) needs no Anvil. When the certificate has no bridge exits, the canonical empty LER is used.

**Reads:** `step-f-capped-certificate.json` if it exists (produced by Step F when `ignoreBalanceMismatch=true`), otherwise `step-e-exit-certificate.json`.

**Output:**

- **G1:** `step-g1-shadow-fork-block.json` (resolved shadow-fork block) and the lite syncer DB `output/step-g1-l2bridgesyncerlite.sqlite`.
- **G2:** `step-g-new-local-exit-root.json`, `step-g-reordered-certificate.json` (the deposit-order certificate Step I consumes) and `step-g-l2bridgesyncerlite.sqlite` (working copy of the G1 DB with the tree built); in shadow-fork mode also `step-g-failed-exit.json` *(only on replay failure)*.

### Step H — Fetch PreviousLocalExitRoot

Calls `interop_getNetworkInfo` on the agglayer JSON-RPC and reads the `settled_ler` for the L2 network. If no certificate has been settled yet, `PreviousLocalExitRoot` is zero.

Requires `agglayerClient.GRPC.URL` in options.

**Output:** `step-h-previous-local-exit-root.json`

### Step I — Assemble final certificate

Takes the deposit-order certificate produced by Step G and applies:

- `NewLocalExitRoot` from Step G
- `PreviousLocalExitRoot` and certificate height from Step H
- `L1InfoTreeLeafCount` — scans L1 backwards from the latest L1 block for the most recent `UpdateL1InfoTreeV2` event on the `l1GlobalExitRootAddress` contract. Requires `l1RpcUrl` and `l1GlobalExitRootAddress` in config.

**Reads:** `step-g-reordered-certificate.json` (run Step G first — there is no fallback to the Step E / Step F certificates, so the final certificate always matches the computed `NewLocalExitRoot`); plus `step-g-new-local-exit-root.json` and `step-h-previous-local-exit-root.json`.

**Output:** `exit-certificate-final.json`

### Step SIGN — Sign the certificate

Signs `exit-certificate-final.json` with the configured keystore and writes `exit-certificate-signed.json`. The signature is embedded in `AggchainData` as an `AggchainDataMultisig` ECDSA entry.

Requires `signerConfig` in config (same format as aggsender's `AggsenderPrivateKey`). Skipped automatically in `all` mode when `signerConfig` is not set.

**Reads:** `exit-certificate-final.json`

**Output:** `exit-certificate-signed.json`

### Step SUBMIT — Send certificate to agglayer

Sends `exit-certificate-signed.json` to the agglayer via gRPC and returns the certificate hash. **Not part of the default pipeline** — must be triggered with `--step submit`.

Requires `agglayerClient.GRPC.URL` in options.

**Reads:** `exit-certificate-signed.json`

**Output:** `step-submit-result.json`

### Step WAIT — Wait for certificate settlement

Polls the agglayer until the submitted certificate reaches a final state. **Not part of the default pipeline** — must be triggered with `--step wait`.

Requires `agglayerClient.GRPC.URL` in options. Reads `step-submit-result.json` for the certificate hash.

Two phases:

1. If a different pending certificate is already in flight on the network, waits for it to settle (or enter error) before proceeding.
2. Polls `GetCertificateHeader` every 5 seconds until the submitted certificate is `Settled` or `InError`. Returns an error if `InError`.

**Reads:** `step-submit-result.json`

**Output:** `step-wait-result.json`

## Output

The final output is `exit-certificate-final.json` in the output directory. It is a standard agglayer `Certificate` JSON object with:

- `bridge_exits` — all value to be exited from the chain: EOA balances (Step B/D) and SC-locked value (Step C/D).
- `imported_bridge_exits` — empty unless a future implementation adds Merkle-proof-backed unclaimed L1→L2 deposits (Step E does not populate this field today).

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
