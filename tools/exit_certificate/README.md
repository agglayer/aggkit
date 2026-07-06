# exit-certificate

Generate exit certificates for a chain migration — scans L2 state, computes balances, and builds a certificate that bridges all value back to L1.

## Overview

**What it does:** The `exit-certificate` CLI scans an L2 chain from genesis to a target block, discovers all addresses with value, and produces an agglayer `Certificate` containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens) to the destination network. The certificate uses the native agglayer types directly — no conversion step is needed before submission.

**When to use it:** Use when an aggchain needs to exit the Agglayer ecosystem. The tool ensures all value on the L2 is accounted for and packaged into a single certificate.

## Requirements

The chain being deprecated must meet **all** of the following conditions for the tool to produce a valid certificate. The first two are verified automatically by [Step CHECK](#step-check--verify-prerequisites); the last two are operational prerequisites you must ensure yourself.

- **The network must be Pessimistic Proof (PP).** FEP (Finality by Execution Proof) chains are not supported. Step CHECK queries `AGGCHAINTYPE()` and aborts if the network is FEP.
- **The committee threshold must be 1.** Exactly one committee member must be required to approve certificates. Step CHECK queries the multisig threshold and aborts if it is greater than 1.
- **The network must have settled at least one certificate.** The tool needs a prior certificate to derive the `PreviousLocalExitRoot` (Step H); a chain that has never settled a certificate cannot be exited with this tool.
- **The network's sequencer must be stopped.** Halt the sequencer before running the tool so that no new bridges (or other state changes) are produced while the certificate is being built. New activity after the target block would not be reflected in the certificate.

## Known limitations

- **No unclaimed L1→L2 bridges are allowed.** Every bridge towards L2 must be claimed before starting the process. Outstanding (unclaimed) deposits must be claimed first; otherwise the generated certificate will not reflect them correctly.
- **`SetClaim` and `UpdatedUnsetGlobalIndexHashChain` events are not supported.** Transactions that emit these events on the bridge contract ([see contracts](https://github.com/agglayer/agglayer-contracts/tree/v12.2.3)) are not detected or accounted for. Value associated with these flows may be missing from the generated certificate.

## Quick start

```bash
# Build from the repo root — the binary is written to target/exit_certificate
make build-exit_certificate

# Create your config from the example
cp tools/exit_certificate/parameters.json.example parameters.json

# Edit parameters.json with your RPC URLs, bridge address, etc.
# Then run the tool
./target/exit_certificate --config parameters.json
```

There are also ready-to-use config files for the zkEVM networks in
[config-examples/](config-examples/) (`zkevm-cardona.toml`, `zkevm-mainnet.toml`). Copy the one that
matches your chain and fill in the fields documented in [config-examples/README.md](config-examples/README.md):

```bash
# Use a prepared zkEVM config as a starting point
cp tools/exit_certificate/config-examples/zkevm-mainnet.toml parameters.toml

# Edit parameters.toml (l1RpcUrl, exitAddress, signerConfig, etc.), then run
./target/exit_certificate --config parameters.toml
```

## Building

From the repo root, using the top-level Makefile (binary is written to `target/exit_certificate`):

```bash
make build-exit_certificate
```

Alternatively, build directly with `go` from `tools/exit_certificate/`:

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
| `l2RpcUrl` | Yes | L2 JSON-RPC endpoint. Step A uses `debug_accountRange` when available (an archive node is required to query state at `targetBlock`); without it, the `auto` discovery mode falls back to receipt harvesting. |
| `l1RpcUrl` | Yes* | L1 JSON-RPC endpoint. Required by Step E (unclaimed deposit detection) and Step I (`L1InfoTreeLeafCount`). Without it Step E is silently skipped and Step I fails — the resulting certificate will be incomplete. |
| `l2BridgeAddress` | Yes | L2 bridge contract address. |
| `l1BridgeAddress` | No | L1 bridge contract address. Defaults to `l2BridgeAddress`. |
| `l2NetworkId` | No | L2 network ID. Defaults to `1`. |
| `targetBlock` | No | Target block for state capture. Accepts a decimal number (`"21000000"`), hex (`"0x1406f40"`), or a finality keyword: `"LatestBlock"`, `"FinalizedBlock"`, `"SafeBlock"`, `"PendingBlock"`. An optional negative offset can be appended (e.g. `"LatestBlock/-10"` = ten blocks before latest). Omitting the field or setting it to `""` defaults to `"LatestBlock"`. The keyword is resolved to a concrete block number at the start of Step 0 and saved to `step-0-l2_target_block.json`. All subsequent steps use that fixed number. |
| `exitAddress` | Yes | Address that receives SC-locked value exits on `destinationNetwork`. **Must be an address whose private key you control**, and **must not be the zero address** (`0x00…00`) — `LoadConfig` rejects both an empty value and the zero address, since these funds can only be recovered by signing from this address. **A multisig (e.g. a Gnosis Safe) is strongly recommended** over a single EOA, so that recovering these funds does not depend on a single private key. |
| `destinationNetwork` | No | Destination network for bridge exits. Defaults to `0` (L1). |
| `sovereignRollupAddr` | Yes* | Address of the `aggchainbase` contract on L1. Required by Step CHECK (network type and threshold verification). |
| `l1GlobalExitRootAddress` | Yes* | Address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. |
| `rollupManagerAddress` | No | Address of the `PolygonRollupManager` (AgglayerManager) contract on L1. Used by Step WAIT to confirm the certificate's L1 settlement (`VerifyBatchesTrustedAggregator`). When unset it is resolved on-chain from `sovereignRollupAddr.rollupManager()`. |
| `signerConfig` | No | Signer configuration object for Step SIGN. Same format as aggsender's `AggsenderPrivateKey`. Example: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}`. |

> **\*Required for specific steps:** `l1RpcUrl` is required by Steps E and I; `sovereignRollupAddr` is required by Step CHECK; `l1GlobalExitRootAddress` is required by Step I. Without them those steps fail.

### Options

| Field | Default | Description |
| :---: | :-----: | :---------: |
| `blockRange` | `5000` | Block range per `eth_getLogs` query (Steps 0, B, E). |
| `stepAWindowSize` | `150000` | Number of blocks loaded into memory per iteration by Step A's receipt-harvesting fallback (used when `debug_accountRange` is unavailable in `auto` mode). |
| `concurrencyLimit` | `20` | Max concurrent RPC requests. |
| `rpcBatchSize` | `200` | Max calls per JSON-RPC batch request. |
| `rpcDelayMs` | `0` | Delay between RPC batches (rate limiting). |
| `outputDir` | `./output` | Directory for intermediate and final output files. Relative paths resolve from the config file directory. |
| `l1StartBlock` | `0` | L1 block to start scanning from (Step E). |
| `l1EndBlock` | `0` | Optional L1 cutoff block. When set (> 0), Step E scans L1 for unclaimed deposits only up to this block (and filters the bridge-service cross-check accordingly) and Step I starts its backward `UpdateL1InfoTreeV2` scan from it. This prevents L1 deposits submitted after the L2 snapshot from blocking the pipeline (AET-03): pick a block at or after the moment the sequencer was stopped. `0` (default) means no cutoff — the current latest L1 block is used. A value below `l1StartBlock` is rejected at config load; a value beyond the current L1 head is rejected when the step runs (it is almost surely a misconfiguration, e.g. an L2 block number). |
| `l2StartBlock` | `0` | L2 block to start scanning from (Step A's receipt-harvesting fallback only; the state dump reads the trie at `targetBlock` and the Transfer-log scan always starts at genesis). |
| `addressDiscovery` | `"auto"` | Step A address-discovery strategy: `"auto"` (probe `debug_accountRange` and use state dump + Transfer logs, else fall back to receipt harvesting + Transfer logs), `"stateDump"`, `"logs"`, or `"both"`. Unknown values fall back to `"auto"` with a warning. |
| `agglayerAdminURL` | `""` | Agglayer admin RPC endpoint. Required for Step F in agglayer mode (Step F errors if it runs without this set). Not needed when `useAgglayerAdminToStepFCheck: false` (offline LBT mode). |
| `agglayerAdminToken` | `""` | Optional bearer token for authenticating requests to `agglayerAdminURL`. Leave empty when the admin endpoint is unauthenticated; set it only when the endpoint is protected (e.g. behind Google Cloud IAP). |
| `agglayerClient` | `{}` | Agglayer gRPC client config (same as aggsender's `agglayer.ClientConfig`). Set at least `agglayerClient.GRPC.URL`. Required for Steps H, SUBMIT, and WAIT. |
| `useAgglayerAdminToStepFCheck` | `true` | Selects the Step F comparison source. When `true` (default), Step F queries the agglayer admin API (`admin_getTokenBalance`) and does a three-way check (LBT == agglayer == certificate; requires `agglayerAdminURL`). When `false`, it skips the agglayer query and instead compares the LBT (Step 0) totals against the certificate bridge-exit sums offline (no `agglayerAdminURL` needed; skipped only if no LBT data exists). |
| `ignoreGenesisBalance` | `false` | When `false` (default), Step B aborts if any address has a non-zero ETH balance at block 0 (genesis preload guard). Set `true` to downgrade it to a warning, only for Kurtosis or test environments. |
| `nativeSCLockedFromContracts` | `true` | When `true` (default), Step C computes the **native** token's SC-locked value from the actual ETH balances held by contract accounts (summed at `targetBlock`, excluding the L2 bridge reserve) instead of `LBT − EOA_accumulated`. That formula underflows on chains with a native genesis premint, clamping to 0 and silently dropping contract-held ETH from the certificate. Wrapped tokens are unaffected. Set to `false` to fall back to the `LBT − EOA` derivation. On premint chains combine with `genesisPrefundETHWei` so the Step F comparison also accounts for the premint. |
| `ignoreBalanceMismatch` | `false` | When `true`, Step F does not abort the pipeline on token balance mismatches. Instead it produces a capped certificate (`step-f-capped-certificate.json`) where each token's bridge exits are trimmed so their per-token sum equals the budget `min(agglayer, lbt)`. The allocation order is controlled by `capMode` — the default `"none"` forbids trimming, so combine with `"amount"` or `"appearance"`. See [Step F](#step-f--agglayer-token-balance-verification) for details. |
| `capMode` | `"none"` | Selects how Step F allocates each token's cap budget when it needs to trim exits. `"none"` (default) forbids capping entirely: Step F fails if any exit would have to be trimmed — including the genesis pre-fund trim, so `genesisPrefundETHWei` requires a trimming mode. `"amount"` serves the smallest-amount exits first, so the largest holders are the first to be capped/dropped once the budget runs out; `"appearance"` serves exits in the order they appear. In both trimming modes the surviving exits are emitted in their original order. Any other value is rejected by `LoadConfig`. |
| `genesisPrefundETHWei` | `""` | Optional amount of native token (in Wei, as a decimal string) pre-funded at genesis. Those funds sit in accounts — and therefore in the certificate's bridge exits — without a matching agglayer deposit, so Step F subtracts this value from the native-token certificate sum before comparing it against the agglayer balance and the LBT (which only count genuinely bridged funds), logging the certificate total, the pre-fund and the difference. The cap budget stays `min(agglayer, lbt)`, and the Step 0 LBT and Step C SC-locked totals are untouched. The pre-funded amount has no agglayer collateral, so even when the checks match Step F emits `step-f-capped-certificate.json` trimming the native exits to that budget (see [Step F](#step-f--agglayer-token-balance-verification)). Validated by `LoadConfig` (non-negative base-10 integer). When set, Step B additionally verifies that the declared value equals the detected genesis ETH preload total and errors on mismatch — this error is **not** suppressed by `ignoreGenesisBalance` (a wrong declaration would make the Step F subtraction silently wrong). Example: `100000` ETH = `"100000000000000000000000"`. |
| `ignoreUnclaimed` | `false` | When `true`, Step E detects and logs unclaimed deposits but leaves the certificate unchanged. When `false` (default), any unclaimed asset deposit causes the pipeline to error. |
| `bridgeServiceURL` | `""` | Base URL of the bridge service REST API. When set, Step E cross-checks its unclaimed deposit set against the bridge service and returns an error on any discrepancy. |
| `bridgeServiceType` | `"aggkit"` | Bridge service API flavour. `"aggkit"` uses `GET /bridge/v1/bridges` (aggkit bridge service); `"zkevm"` uses `GET /pending-bridges` (zkevm-bridge-service). |
| `extraErc20Contracts` | `[]` | Optional list of ERC-20 contract addresses to decompose into individual holder balances in Step B3. For each address the tool calls `balanceOf` for every EOA collected in Step A. Example: `["0xAbc...123", "0xDef...456"]`. |
| `ignoreUnsupportedL2Events` | `false` | When `true`, the Step G lite syncer logs a warning and continues instead of aborting when it sees an L2 event that would invalidate a BridgeEvent-only reconstruction (`SetSovereignTokenAddress`, `MigrateLegacyToken`, `RemoveLegacySovereignTokenAddress`, `BackwardLET`, `ForwardLET`). The computed `NewLocalExitRoot` may then be incorrect — enable only to knowingly inspect such a chain. |
| `verifyNewLocalExitRootUsingShadowFork` | `true` | Selects the Step G2 mode. When `true` (default), Step G2 spins up an Anvil shadow-fork, replays every bridge exit against the real bridge contract, reorders the certificate to the on-chain deposit order, and verifies the computed `NewLocalExitRoot` against the contract's `getRoot()` (requires `anvil` in `$PATH`). When `false`, Step G2 computes the `NewLocalExitRoot` off-chain from the lite exit tree (no Anvil) — much faster, but it trusts the off-chain leaf encoding/metadata. See [Step G](#step-g--compute-newlocalexitroot) for details. |

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

#### Options to skip failing checks

Some options let you continue past conditions that would otherwise abort the pipeline. Use them with care:

| Option | Default | When to change |
| ------ | ------- | -------------- |
| `ignoreGenesisBalance` | `false` | Set to `true` only for Kurtosis or test environments where addresses are pre-funded at genesis. In production, a non-zero genesis balance indicates a misconfiguration, so leave it `false` to abort. |
| `ignoreUnclaimed` | `false` | Set to `true` to proceed even when unclaimed L1→L2 asset deposits are detected. The deposits are logged with a warning but the certificate is left unchanged. Only safe if you have independently verified the unclaimed deposits are negligible or already handled. |

## Commands

### Run full pipeline

```bash
./target/exit_certificate --config parameters.json
```

Runs all steps sequentially: CHECK → 0 → A → B → C → D → E → F → G → H → I → SIGN (if `signerConfig` is set).

This produces and signs the certificate but **does not submit it**. SUBMIT and WAIT are intentionally left out of the default pipeline — once you have reviewed the signed certificate, run them explicitly:

```bash
# Send the signed certificate to the agglayer
./target/exit_certificate --config parameters.json --step submit

# Wait for it to settle (on the agglayer and on L1)
./target/exit_certificate --config parameters.json --step wait
```

| Step | Name | What it does |
| :--: | ---- | ------------ |
| CHECK | Verify prerequisites | Checks Anvil, L1 RPC, network type (PP only), threshold = 1, no custom gas token. |
| 0 | Generate LBT | Resolves `targetBlock` to a concrete block number, then scans `NewWrappedToken` events and fetches `totalSupply` per wrapped token at that block. |
| A | Collect addresses | Discovers every value-holding address from the final state (`debug_accountRange` state dump) plus `Transfer` event logs per wrapped token. Strategy selected by `addressDiscovery` (`auto` falls back to receipt harvesting when `debug_accountRange` is unavailable). |
| B | EOA balances + ERC-20 detection | B1: classifies addresses and fetches ETH/token balances for EOAs. B2: probes contracts for the ERC-20 interface and checks if they hold tracked wrapped tokens. B3: fetches holder breakdowns for `extraErc20Contracts` (skips any already processed by B2). |
| C | SC-locked value | Computes value locked in contracts: `SC_locked = LBT_totalSupply − EOA_accumulated` per token. With `nativeSCLockedFromContracts=true` the native token's SC-locked value is measured from actual contract ETH balances instead. |
| D | Build certificate | Creates the `Certificate` with `BridgeExit` entries for every (EOA, token) pair, every decomposed ERC-20 holder (Step C holder bridges), and every token with SC-locked value. |
| E | Unclaimed deposits | Scans L1 for unclaimed `BridgeEvent` deposits targeting L2. Message deposits (`leaf_type=1`) are saved to `step-e-unclaimed-messages.json` and never added to the certificate. Asset deposits (`leaf_type=0`): if none are found the certificate is passed through unchanged; if any are found and `ignoreUnclaimed=true` they are logged but the certificate remains unchanged; if found and `ignoreUnclaimed=false` the pipeline errors (Merkle proof support not yet implemented). Optionally cross-checks against a bridge service. |
| F | Balance verification | Three-way comparison (LBT, agglayer, certificate) per token. Aborts on mismatch by default; with `ignoreBalanceMismatch=true` produces a capped certificate (allocation set by `capMode`; the default `"none"` forbids trimming and fails instead). Whenever `agglayerAdminURL` is set it also dumps the agglayer LBT to `step-f-agglayer-lbt.json`. With `useAgglayerAdminToStepFCheck=false` it skips the agglayer comparison and does an offline LBT-vs-certificate comparison instead. |
| G | NewLocalExitRoot | G1: syncs the L2 bridge history from genesis up to `targetBlock` into a lite DB and resolves the shadow-fork block. G2: computes the `NewLocalExitRoot` — by default shadow-forks L2 via Anvil, replays all bridge exits, and reads the resulting root from the forked bridge contract (or computes it off-chain when `verifyNewLocalExitRootUsingShadowFork=false`). |
| H | PreviousLocalExitRoot | Fetches `settled_ler` from the agglayer gRPC to obtain the previous LER and the next certificate height. |
| I | Assemble final cert | Applies `NewLocalExitRoot` (G), `PreviousLocalExitRoot` + height (H), bridge exit metadata, and `L1InfoTreeLeafCount` (from the latest `UpdateL1InfoTreeV2` event on L1). |
| SIGN | Sign certificate | Hashes the certificate and signs it with the configured keystore; wraps the signature in `AggchainDataMultisig`. |
| SUBMIT | Send to agglayer | Sends the signed certificate to the agglayer via gRPC. **Not part of the default pipeline.** |
| WAIT | Wait for settlement | Polls `GetCertificateHeader` every 5 s until the certificate is `Settled` or `InError`, then confirms the settlement on L1 (`VerifyBatchesTrustedAggregator` on the RollupManager + the accompanying `UpdateL1InfoTree`/`UpdateL1InfoTreeV2` events). **Not part of the default pipeline.** |

Steps SUBMIT and WAIT are **not** part of the default pipeline — they must be triggered explicitly.

### Run one or more steps

```bash
# Single step
./target/exit_certificate --config parameters.json --step h

# Multiple steps (comma-separated, run in the given order)
./target/exit_certificate --config parameters.json --step h,i,sign
./target/exit_certificate --config parameters.json --step "sign, submit"

# Ranges (inclusive)
./target/exit_certificate --config parameters.json --step a-c     # a, b, c
./target/exit_certificate --config parameters.json --step g-      # g, h, i, sign (open range stops at sign)
./target/exit_certificate --config parameters.json --step 0-wait  # every step, including submit and wait
```

Each step reads its dependencies from the output directory (files written by prior steps).
Spaces around commas are ignored. Execution stops at the first step that fails.

Ranges use `from-to` (inclusive). An open-ended `from-` runs through `sign`; `submit` and `wait` are left out of open ranges and must be named explicitly (e.g. `0-wait` to run the entire flow end to end).

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Step(s) to run. Accepts `all`; a single step name; a comma-separated list (e.g. `h,i,sign`); or a range `from-to` (inclusive, e.g. `a-c` → `a,b,c`). An **open-ended** range `from-` runs through `sign` (e.g. `g-` → `g,h,i,sign`); `submit`/`wait` are excluded from open ranges and must be named explicitly — use `0-wait` to run every step. Valid names: `check`, `0`, `a`, `b`/`b1`/`b2`/`b3`, `c`–`f`, `g`/`g1`/`g2`, `h`, `i`, `sign`, `submit`, `wait`. The aliases `b` and `g` expand to their sub-steps and also work as range bounds. |
| `--verbose` | — | `false` | Enable debug logging. Without this flag only `info`, `warn` and `error` messages are shown. |

## Pipeline steps

### Step CHECK — Verify prerequisites

Runs automatically as the first step of the full pipeline. Can also be run individually:

```bash
./target/exit_certificate --config parameters.json --step check
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

#### Step 0 — LBT generation

After resolution, Step 0 scans the L2 bridge contract for `NewWrappedToken` events and fetches the `totalSupply` of each wrapped token at the resolved block. It also applies any `SetSovereignTokenAddress` overrides (remapped wrapped addresses), computes the unlocked native token balance, and checks for a WETH entry if the chain has a custom gas token.

**Output:** `step-0-l2_target_block.json` (resolved block number), `step-0-lbt.json` (LBT entries)

### Step A — Collect addresses

Discovers every value-holding address at `targetBlock` from the **final state** plus **token logs**, instead of replaying the whole chain history with `debug_traceTransaction`. It combines two sources and merges them:

1. **State dump** — walks the entire account trie at `targetBlock` via paginated `debug_accountRange` calls: every account with non-zero balance/nonce/code (all native-ETH holders and every contract) in `O(#accounts)`. The node's `debug_accountRange` dialect (geth vs erigon/cdk-erigon) is auto-detected on the first page.
2. **`Transfer` event logs** — scans `eth_getLogs` for each wrapped token (list from the Step 0 LBT) across `[0, targetBlock]`, collecting the indexed `from`/`to` of every `Transfer`. This surfaces token-only EOAs that have no nonce/balance/code and therefore appear in neither a state dump nor a trace. The scan deliberately starts at block 0 (not `l2StartBlock`) so passive holders that received tokens early are not dropped.

The strategy is selected by `options.addressDiscovery`:

| Value | Behaviour |
| ----- | --------- |
| `auto` (default) | Probe `debug_accountRange`; if supported, use state dump + Transfer logs. Otherwise fall back to **receipt harvesting** (block bodies + `eth_getTransactionReceipt` from `l2StartBlock`, in windows of `options.stepAWindowSize`) + Transfer logs, with a warning — the fallback misses internal value transfers (a CALL with value to a fresh address leaves no receipt entry). |
| `stateDump` | State dump only. |
| `logs` | Transfer logs only. |
| `both` | State dump + Transfer logs; errors if `debug_accountRange` is unavailable. |

The state dump fails loudly (instead of returning a truncated or empty set) when the node keeps returning a non-empty pagination cursor past the page cap or returns 0 accounts (e.g. a geth archive node without address preimages) — in `auto` mode the latter triggers the receipt-harvesting fallback.

The **zero address** (`0x000…000`) is treated like any other account: a plain `transfer(0x0, amount)` is not a burn (the tokens remain in `totalSupply`) and native ETH can be sent there too, so its balances must be scanned and covered by the certificate for the totals to reconcile with the LBT.

**Output:** `step-a-addresses.json` (the file consumed by later steps)

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

Computes value locked in smart contracts using: `SC_locked = LBT_totalSupply - accumulated_EOA_balances`. Uses the LBT data (Step 0) for total supply per token. It also emits the per-holder bridge entries derived from the Step B3 ERC-20 decomposition, which Step D turns into exits back to each holder.

**Native token on premint chains (`nativeSCLockedFromContracts`):** on a chain where native ETH was minted at genesis directly into accounts, `LBT − EOA` underflows for the native token (the EOA balances include the premint but the LBT only measures bridge outflow), gets clamped to 0, and the ETH actually held by contracts silently disappears from the certificate. With `options.nativeSCLockedFromContracts: true`, Step C instead **measures** the native SC-locked value directly: it fetches `eth_getBalance` of every contract from Step B at `targetBlock` (excluding the L2 bridge, whose balance is the un-released reserve) and uses the sum as the native SC-locked value. Wrapped tokens keep the `LBT − EOA` formula. Note that with this option `--step c` is no longer a pure offline computation: it needs the L2 RPC, `step-0-l2_target_block.json` and `step-b-contract-addresses.json` (run Step B first).

**Output:** `step-c-sc-locked-values.json`, `step-c-holder-bridges.json`

### Step D — Build exit certificate

Creates the agglayer `Certificate` with `BridgeExit` entries for:

1. Every (EOA, token) pair with a non-zero balance → exits to the same address on the destination network
2. Every holder of a decomposed ERC-20 contract (from Step C's holder bridges, i.e. the `extraErc20Contracts` / detected-vault breakdowns) → exits to the holder's address on the destination network
3. Every token with SC-locked value → exits to `exitAddress` on the destination network

**Output:** `step-d-exit-certificate.json`

### Step E — Unclaimed L1→L2 bridge deposits

Scans L1 for `BridgeEvent` events targeting the L2 and checks each deposit against `isClaimed` on the L2 bridge. Deposits are split by leaf type:

- **Message deposits (`leaf_type=1`)** — never added to the certificate. Saved to `step-e-unclaimed-messages.json` for review.
- **Asset deposits (`leaf_type=0`)** — three outcomes depending on what is found:
  - **No unclaimed asset deposits** → step completes, certificate passed through unchanged.
  - **Unclaimed asset deposits found + `ignoreUnclaimed=true`** → deposits are detected, amounts logged with a warning, certificate left unchanged.
  - **Unclaimed asset deposits found + `ignoreUnclaimed=false`** → pipeline **errors**. Adding unclaimed deposits to the certificate requires Merkle proofs which are not yet implemented.

When `bridgeServiceURL` is set, Step E compares its detected unclaimed set against the bridge service's pending-bridges and errors if the sets differ. Supports both aggkit (`/bridge/v1/bridges`) and zkevm-bridge-service (`/pending-bridges`) via `bridgeServiceType`.

The scan ends at `options.l1EndBlock` when configured (deposits made on L1 after that cutoff are ignored, both in the scan and in the bridge-service comparison), otherwise at the current latest L1 block.

Requires `l1RpcUrl`.

**Output:** `step-e-unclaimed-bridges.json`, `step-e-unclaimed-messages.json` (both always written), `step-e-exit-certificate.json` *(only when the step produces a certificate — i.e. not on the `ignoreUnclaimed=false` abort path)*

### Step F — Agglayer token balance verification

Step F has two modes selected by `options.useAgglayerAdminToStepFCheck` (default `true`):

- **Agglayer mode (`true`):** queries the agglayer admin API (`admin_getTokenBalance`) and performs a **three-way comparison** per token (requires `agglayerAdminURL`).
- **Offline mode (`false`):** **no agglayer query** — performs a **two-way LBT (Step 0) vs certificate** comparison per token. No `agglayerAdminURL` needed; when no LBT data is available there is nothing to compare and the step is skipped. `step-f-token-balances.json` is not written in this mode.

The three-way comparison (agglayer mode):

| Source | What it represents |
| ------ | ------------------ |
| **LBT** (Step 0) | `totalSupply` of the wrapped token at `targetBlock` — what the L2 contract holds |
| **Agglayer** | What the agglayer believes is locked for this L2 network |
| **Certificate** | Sum of all `BridgeExit` amounts for that token |

All compared values must be equal. Each token is logged with ✅ or ❌:

```text
✅ (network=1 addr=0xabc...): lbt=1000  certificate=1000  agglayer=1000
❌ MISMATCH (network=1 addr=0xdef...): lbt=800  certificate=1000  agglayer=900
```

**If mismatches are found:**

- By default Step F **aborts the pipeline** with an error.
- Set `options.ignoreBalanceMismatch: true` to continue instead. In that case the step produces `step-f-capped-certificate.json`, where each mismatched token's bridge exits are trimmed so their per-token sum equals the budget `min(agglayer, lbt)`. Subsequent steps in the pipeline (G, H, I) automatically use this capped certificate.
  - `options.capMode` selects how each token's budget is allocated across its exits. `"none"` (the default) forbids capping: Step F fails if any exit would have to be trimmed, so combine `ignoreBalanceMismatch=true` with `"amount"` or `"appearance"`. `"amount"` serves the smallest-amount exits first, so the largest holders are the first to be capped/dropped once the budget runs out; `"appearance"` serves exits in the order they appear. In both trimming modes the surviving exits are emitted in their original order.

When running Step G individually it also prefers `step-f-capped-certificate.json` over `step-e-exit-certificate.json` if the capped file exists (logged with ⚠️).

LBT data comes from `step-0-lbt.json`. In agglayer mode, if it is not available the comparison falls back to two-way (certificate vs agglayer only); in offline mode, missing LBT means there is nothing to compare and the step is skipped.

In agglayer mode `agglayerAdminURL` must be set (errors otherwise); offline mode needs no admin endpoint.

**Genesis pre-fund adjustment:** when `options.genesisPrefundETHWei` is set, Step F subtracts that Wei amount from the native-token **certificate sum** (floored at zero) before running either comparison, logging the certificate total, the declared pre-fund and the resulting difference. Native tokens minted at genesis sit in accounts — and therefore in the certificate's bridge exits — without a matching agglayer deposit, so this discounts them and lets the comparison balance against the genuinely bridged amount (the agglayer balance and the Step 0 LBT only count bridged funds). The cap budget stays `min(agglayer, lbt)`, and the LBT written by Step 0 and the Step C SC-locked totals are left unchanged.

The pre-funded amount has **no agglayer collateral**, so it can never be bridged out: even when every check matches (thanks to the discount), Step F still produces `step-f-capped-certificate.json` trimming the native exits down to `min(agglayer, lbt)` — no `ignoreBalanceMismatch` needed. This trim requires a trimming `capMode`: with the default `"none"` Step F fails instead, so set `capMode` to `"amount"` (the large pre-funded holders absorb the trim) or `"appearance"`.

**Agglayer LBT dump:** whenever `agglayerAdminURL` is set, Step F queries `admin_getTokenBalance` once at the very start and writes the full raw response (the agglayer's local balance tree for `l2NetworkId`) to `step-f-agglayer-lbt.json`, regardless of the comparison mode. In agglayer mode this same response is reused for the comparison (no second RPC). The [`scripts/get-agglayer-lbt.sh`](scripts/get-agglayer-lbt.sh) helper fetches the same data manually.

**Reads:** `step-d-exit-certificate.json`, `step-0-lbt.json`

**Output:** `step-f-token-balances.json`, `step-f-checks.json`, `step-f-agglayer-lbt.json` *(only when `agglayerAdminURL` is set)*, `step-f-capped-certificate.json` *(when mismatches exist and `ignoreBalanceMismatch=true`, or when `genesisPrefundETHWei` trims the native exits)*

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
- `L1InfoTreeLeafCount` — scans L1 backwards from `options.l1EndBlock` (or the latest L1 block when unset) for the most recent `UpdateL1InfoTreeV2` event on the `l1GlobalExitRootAddress` contract. Requires `l1RpcUrl` and `l1GlobalExitRootAddress` in config.

**Reads:** `step-g-reordered-certificate.json` (run Step G first — there is no fallback to the Step E / Step F certificates, so the final certificate always matches the computed `NewLocalExitRoot`); plus `step-g-new-local-exit-root.json` and `step-h-previous-local-exit-root.json`.

**Output:** `exit-certificate-final.json`

### Step SIGN — Sign the certificate

Signs `exit-certificate-final.json` with the configured keystore and writes `exit-certificate-signed.json`. The signature is embedded in `AggchainData` as an `AggchainDataMultisig` ECDSA entry.

Requires `signerConfig` in config (same format as aggsender's `AggsenderPrivateKey`). Skipped automatically in `all` mode when `signerConfig` is not set.

**Reads:** `exit-certificate-final.json`

**Output:** `exit-certificate-signed.json`

### Step SUBMIT — Send certificate to agglayer

Sends `exit-certificate-signed.json` to the agglayer via gRPC and returns the certificate hash. **Not part of the default pipeline** — must be triggered with `--step submit`.

Before submitting, it:

1. Checks for a pending certificate on the network (`GetLatestPendingCertificateHeader`). If one exists and is **not closed**, the step **errors** — you must wait for it to settle before submitting a new one.
2. Captures the **latest L1 block right before submission** (`eth_blockNumber` on `l1RpcUrl`). This is recorded in the result and marks the L1 starting point from which Step WAIT looks for the certificate's L1 settlement.

Requires `agglayerClient.GRPC.URL` and `l1RpcUrl` in config.

**Reads:** `exit-certificate-signed.json`

**Output:** `step-submit-result.json` (`certificateHash` + `l1LatestBlockBeforeSubmittingCertificate`)

### Step WAIT — Wait for certificate settlement

Polls the agglayer until the submitted certificate reaches a final state, then confirms the settlement on L1. **Not part of the default pipeline** — must be triggered with `--step wait`.

Two phases:

1. **Agglayer settlement** — polls `GetCertificateHeader` by hash every 5 seconds until the submitted certificate is `Settled` (success) or `InError` (returns an error). Logs the settlement tx hash on success.
2. **L1 settlement confirmation** — scans the RollupManager contract on L1 from `l1LatestBlockBeforeSubmittingCertificate` (from the submit result) to the **finalized** block for the `VerifyBatchesTrustedAggregator` event matching the rollupID (`l2NetworkId`) and the certificate's `NewLocalExitRoot`. The RollupManager address is `rollupManagerAddress` if set, otherwise resolved on-chain from `sovereignRollupAddr.rollupManager()`. It re-resolves the finalized block and re-scans every 5 seconds until found. In that same L1 block it then reads the last `UpdateL1InfoTree` and `UpdateL1InfoTreeV2` events emitted by `l1GlobalExitRootAddress` (the global-exit-root update accompanying the settlement).

Requires `agglayerClient.GRPC.URL`, `l1RpcUrl`, and `l1GlobalExitRootAddress` in config, plus either `rollupManagerAddress` or `sovereignRollupAddr` to resolve the RollupManager.

**Reads:** `step-submit-result.json` (certificate hash + the captured pre-submission L1 block)

**Output:** `step-wait-result.json` (final status, settlement tx hash, the L1 `VerifyBatchesTrustedAggregator` block/tx, and the `UpdateL1InfoTree` / `UpdateL1InfoTreeV2` events in that block)

## Result

After the full flow completes (the certificate is built and signed, then SUBMIT and WAIT succeed):

- **The agglayer holds every bridge exit in the certificate.** Once the certificate settles, the agglayer accounts for all of the certificate's `bridge_exits` — the value has been bridged out of the L2 and is ready to be claimed on the destination network.
- **The files needed to claim those bridges have been generated.** Claiming each exit requires calling `claimAsset` on the bridge contract with Merkle proofs and the exit roots. The companion [`exit_certificate_claimer`](../exit_certificate_claimer/README.md) tool consumes the exit_certificate output and produces the parameters for each `claimAsset` call.

The output files the claimer needs are:

| File | Used for |
| ---- | -------- |
| `exit-certificate-signed.json` | The signed certificate — source of each exit's `originNetwork`, `originTokenAddress`, `destinationNetwork`, `destinationAddress`, `amount`, `metadata`. |
| `step-g-l2bridgesyncerlite.sqlite` | The L2 local exit tree — used to build the `smtProofLocalExitRoot` proof of each leaf against `new_local_exit_root`. |
| `step-wait-result.json` | The WAIT step's L1 settlement record (`VerifyBatchesTrustedAggregator` + the `UpdateL1InfoTree`/`UpdateL1InfoTreeV2` events) used to anchor the claim to the settled global exit root. |

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
