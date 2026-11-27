# exit-certificate

Generate exit certificates for a chain migration — scans L2 state, computes balances, and builds a certificate that bridges all value back to L1.

## Overview

**What it does:** The `exit-certificate` CLI scans an L2 chain from genesis to a target block, discovers all addresses with value, and produces an agglayer `Certificate` containing `BridgeExit` entries that transfer every balance (ETH + wrapped tokens) to the destination network. The certificate uses the native agglayer types directly — no conversion step is needed before submission.

**When to use it:** Use when an aggchain needs to exit the Agglayer ecosystem. The tool ensures all value on the L2 is accounted for and packaged into a single certificate.

## Requirements

The chain being deprecated must meet **all** of the following conditions for the tool to produce a valid certificate. The first two and the last one are verified automatically by [Step CHECK](#step-check--verify-prerequisites); the others are operational prerequisites you must ensure yourself.

- **The network must be Pessimistic Proof (PP).** FEP (Finality by Execution Proof) chains are not supported. Step CHECK queries `AGGCHAINTYPE()` and aborts if the network is FEP.
- **The committee threshold must be 1.** Exactly one committee member must be required to approve certificates. Step CHECK queries the multisig threshold and aborts if it is greater than 1.
- **The network must have settled at least one certificate.** Step H derives the `PreviousLocalExitRoot` from the agglayer's `settled_ler`, treating a missing value (no certificate ever settled) as zero. In practice a never-settled chain still cannot be exited: the AET-11 verification requires the L2 bridge LER at the target block to equal the settled LER, and the bridge's LER (the root of its deposit tree, non-zero even when empty) never matches that zero fallback.
- **The network's sequencer must be stopped.** Halt the sequencer before running the tool so that no new bridges (or other state changes) are produced while the certificate is being built. New activity after the target block would not be reflected in the certificate.
- **Every L2→L1 bridge exit up to the target block must be settled by the agglayer.** Halting the sequencer is not sufficient: an ordinary bridge withdrawal made *before* the halt advances the L2 local exit root, and if no certificate settling it has been finalized, the tool cannot produce a certificate from that snapshot (the L2 LER no longer matches the agglayer's `settled_ler`). In practice: keep the aggsender running after the halt until the last certificate settles, and only then run the tool. Step CHECK verifies this (check 9, AET-11) and Step 0 re-verifies it on its resolved target block, aborting with an actionable error before the expensive scan/replay phases; Step H re-checks the same condition at the end. In single-step mode past Step 0 (`--step g2`, …) Step H remains the final safety net.

- **Delta mode only:** the previous certificate must be settled and be the **latest** settled certificate of the network (no pending one), and its output directory (`options.previousRun`) must be intact — see [Delta mode](#delta-mode--chain-additional-exits-on-a-previous-certificate). The L2 does not need to be producing blocks; the RPC is only used to derive missing exit metadata and the gas-token info.

## Known limitations

- **No unclaimed L1→L2 bridges are allowed.** Every bridge towards L2 must be claimed before starting the process. Outstanding (unclaimed) deposits must be claimed first; otherwise the generated certificate will not reflect them correctly.
- **A full run covers one certificate.** Funds it leaves behind (e.g. `skipSCLockedValue`) cannot be added by re-running the pipeline: the new certificate would have to restart from the already settled LER and the L2 scan does not contain recovery exits. Use the [delta mode](#delta-mode--chain-additional-exits-on-a-previous-certificate).
- **Delta mode has no shadow-fork verification.** The L2 bridge never saw the previous exits, so its `getRoot()` cannot anchor the tree. The previous tree's root is instead checked against the previous certificate's `new_local_exit_root`, and the leaf encoding is the same code path as the off-chain `verifyNewLocalExitRootUsingShadowFork=false` mode.
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
| `l2RpcUrl` | Yes | L2 JSON-RPC endpoint. Step A requires `debug_accountRange` (an archive node exposing the `debug` namespace, to query state at `targetBlock`); without it Step A fails. |
| `l1RpcUrl` | Yes* | L1 JSON-RPC endpoint. Required by Step E (unclaimed deposit detection) and Step I (`L1InfoTreeLeafCount`). Without it Step E is silently skipped and Step I fails — the resulting certificate will be incomplete. |
| `l2BridgeAddress` | Yes | L2 bridge contract address. |
| `l1BridgeAddress` | No | L1 bridge contract address. Defaults to `l2BridgeAddress`. Verified on-chain by Step CHECK (`networkID()==0` plus the `aggchainbase`/`rollupManager` bridge-address cross-checks). |
| `l2NetworkId` | No | L2 network ID. Defaults to `1`. |
| `targetBlock` | No | Target block for state capture. Accepts a decimal number (`"21000000"`), hex (`"0x1406f40"`), or a finality keyword: `"LatestBlock"`, `"FinalizedBlock"`, `"SafeBlock"`, `"PendingBlock"`. An optional negative offset can be appended (e.g. `"LatestBlock/-10"` = ten blocks before latest). Omitting the field or setting it to `""` defaults to `"LatestBlock"`. The keyword is resolved to a concrete block number at the start of Step 0 and saved to `step-0-l2_target_block.json`. All subsequent steps use that fixed number. |
| `exitAddress` | Yes* | Address that receives SC-locked value exits on `destinationNetwork`. **Must be an address whose private key you control**, and **must not be the zero address** (`0x00…00`) — `LoadConfig` rejects both an empty value and the zero address, since these funds can only be recovered by signing from this address. **A multisig (e.g. a Gnosis Safe) is strongly recommended** over a single EOA, so that recovering these funds does not depend on a single private key. Not required with `options.skipSCLockedValue: true` (the SC-locked funds are then left behind and no exit targets this address); a non-empty value must still be valid hex. |
| `destinationNetwork` | No | Destination network for bridge exits. Defaults to `0` (L1). |
| `sovereignRollupAddr` | Yes* | Address of the `aggchainbase` contract on L1. Required by Step CHECK (network type and threshold verification). |
| `l1GlobalExitRootAddress` | Yes* | Address of `PolygonZkEVMGlobalExitRootV2` on L1. Required by Step I to fetch `L1InfoTreeLeafCount`. |
| `rollupManagerAddress` | No | Address of the `PolygonRollupManager` (AgglayerManager) contract on L1. Used by Step WAIT to confirm the certificate's L1 settlement (`VerifyBatchesTrustedAggregator`). When unset it is resolved on-chain from `sovereignRollupAddr.rollupManager()`. |
| `signerConfig` | No | Signer configuration object for Step SIGN. Same format as aggsender's `AggsenderPrivateKey`. Example: `{"Method": "local", "Path": "keystore.json", "Password": "pass"}`. |

> **\*Required for specific steps:** `l1RpcUrl` is required by Steps E and I; `sovereignRollupAddr` is required by Step CHECK; `l1GlobalExitRootAddress` is required by Step I. Without them those steps fail. `exitAddress` is required unless `options.skipSCLockedValue: true`.

### Options

| Field | Default | Description |
| :---: | :-----: | :---------: |
| `blockRange` | `5000` | Block range per `eth_getLogs` query (Steps 0, B, E). |
| `concurrencyLimit` | `20` | Max concurrent RPC requests. |
| `rpcBatchSize` | `200` | Max calls per JSON-RPC batch request. |
| `rpcDelayMs` | `0` | Delay between RPC batches (rate limiting). |
| `outputDir` | `./output` | Directory for intermediate and final output files. Relative paths resolve from the config file directory. |
| `l1StartBlock` | `0` | L1 block to start scanning from (Step E). |
| `l1EndBlock` | `0` | Optional L1 cutoff block. When set (> 0), Step E scans L1 for unclaimed deposits only up to this block (and filters the bridge-service cross-check accordingly) and Step I starts its backward `UpdateL1InfoTreeV2` scan from it. This prevents L1 deposits submitted after the L2 snapshot from blocking the pipeline (AET-03): pick a block at or after the moment the sequencer was stopped. `0` (default) means no cutoff — the current latest L1 block is used. A value below `l1StartBlock` is rejected at config load; a value beyond the current L1 head is rejected when the step runs (it is almost surely a misconfiguration, e.g. an L2 block number). |
| `l2StartBlock` | `0` | Sanity guard: Step A errors if `targetBlock` is below this value. The state dump reads the trie at `targetBlock` and the Transfer-log scan always starts at genesis. |
| `agglayerAdminURL` | `""` | Agglayer admin RPC endpoint. Required for Step F in agglayer mode (Step F errors if it runs without this set). Not needed when `useAgglayerAdminToStepFCheck: false` (offline LBT mode). |
| `agglayerAdminToken` | `""` | Optional bearer token for authenticating requests to `agglayerAdminURL`. Leave empty when the admin endpoint is unauthenticated; set it only when the endpoint is protected (e.g. behind Google Cloud IAP). |
| `agglayerClient` | `{}` | Agglayer gRPC client config (same as aggsender's `agglayer.ClientConfig`). Set at least `agglayerClient.GRPC.URL`. Required for Steps H, SUBMIT, and WAIT. |
| `useAgglayerAdminToStepFCheck` | `true` | Selects the Step F comparison source. When `true` (default), Step F queries the agglayer admin API (`admin_getTokenBalance`) and does a three-way check (LBT == agglayer == certificate; requires `agglayerAdminURL`). When `false`, it skips the agglayer query and instead compares the LBT (Step 0) totals against the certificate bridge-exit sums offline (no `agglayerAdminURL` needed; skipped only if no LBT data exists). |
| `ignoreGenesisBalance` | `false` | When `false` (default), Step B aborts if any address has a non-zero ETH balance at block 0 (genesis preload guard). Set `true` to downgrade it to a warning, only for Kurtosis or test environments. |
| `nativeSCLockedFromContracts` | `true` | When `true` (default), Step C computes the **native** token's SC-locked value from the actual ETH balances held by contract accounts (summed at `targetBlock`, excluding the L2 bridge reserve) instead of `LBT − EOA_accumulated`. That formula underflows on chains with a native genesis premint, clamping to 0 and silently dropping contract-held ETH from the certificate. Wrapped tokens are unaffected. Set to `false` to fall back to the `LBT − EOA` derivation. On premint chains combine with `genesisPrefundETHWei` so the Step F comparison also accounts for the premint. |
| `skipSCLockedValue` | `false` | When `true`, Step D omits the per-token SC-locked exits (the `PendingSCLockedBalance` → `exitAddress` entries) from the certificate: those funds are intentionally **left behind on the L2**, and `exitAddress` is no longer required. Step C still runs — `step-c-sc-locked-values.json` records what is left behind — and Step F discounts the omitted amounts from the LBT and agglayer balances before comparing (the check stays strict equality; each token's discount is recorded as `skippedSCLockedAmount` in `step-f-checks.json`, and the cap budget excludes the omitted funds). Holder-bridge exits (Step C's `HolderBridges`) are unaffected. In single-step mode `--step f` then requires `step-c-sc-locked-values.json` (run Step C first). |
| `ignoreBalanceMismatch` | `false` | When `true`, Step F does not abort the pipeline on token balance mismatches. Instead it produces a capped certificate (`step-f-capped-certificate.json`) where each token's bridge exits are trimmed so their per-token sum equals the budget `min(agglayer, lbt)`. The allocation order is controlled by `capMode` — the default `"none"` forbids trimming, so combine with `"amount"` or `"appearance"`. See [Step F](#step-f--agglayer-token-balance-verification) for details. |
| `capMode` | `"none"` | Selects how Step F allocates each token's cap budget when it needs to trim exits. `"none"` (default) forbids capping entirely: Step F fails if any exit would have to be trimmed — including the genesis pre-fund trim, so `genesisPrefundETHWei` requires a trimming mode. `"amount"` serves the smallest-amount exits first, so the largest holders are the first to be capped/dropped once the budget runs out; `"appearance"` serves exits in the order they appear. In both trimming modes the surviving exits are emitted in their original order. Any other value is rejected by `LoadConfig`. |
| `genesisPrefundETHWei` | `""` | Optional amount of native token (in Wei, as a decimal string) pre-funded at genesis. Those funds sit in accounts — and therefore in the certificate's bridge exits — without a matching agglayer deposit, so Step F subtracts this value from the native-token certificate sum before comparing it against the agglayer balance and the LBT (which only count genuinely bridged funds), logging the certificate total, the pre-fund and the difference. The cap budget stays `min(agglayer, lbt)`, and the Step 0 LBT and Step C SC-locked totals are untouched. The pre-funded amount has no agglayer collateral, so even when the checks match Step F emits `step-f-capped-certificate.json` trimming the native exits to that budget (see [Step F](#step-f--agglayer-token-balance-verification)). Validated by `LoadConfig` (non-negative base-10 integer). When set, Step B additionally verifies that the declared value equals the detected genesis ETH preload total and errors on mismatch — this error is **not** suppressed by `ignoreGenesisBalance` (a wrong declaration would make the Step F subtraction silently wrong). Example: `100000` ETH = `"100000000000000000000000"`. |
| `ignoreUnclaimed` | `false` | When `true`, Step E detects and logs unclaimed deposits but leaves the certificate unchanged. When `false` (default), any unclaimed asset deposit causes the pipeline to error. |
| `pendingClaimMessages` | `false` | When `true`, the [PENDING_CLAIMS step](#step-pending_claims--unclaimed-l2l1-bridges-emitted-before-the-certificate) also lists the unclaimed pre-certificate L2→L1 **messages** (`leaf_type=1`, claimed with `claimMessage`), along with the assets. Off by default: assets only, the output of the first mainnet run. The `--pending-claim-messages` CLI flag overrides it. |
| `bridgeServiceURL` | `""` | Base URL of the bridge service REST API. When set, Step E cross-checks its unclaimed deposit set against the bridge service and returns an error on any discrepancy. |
| `bridgeServiceType` | `"aggkit"` | Bridge service API flavour. `"aggkit"` uses `GET /bridge/v1/bridges` (aggkit bridge service); `"zkevm"` uses `GET /pending-bridges` (zkevm-bridge-service). |
| `extraErc20Contracts` | `[]` | Optional list of ERC-20 contract addresses to decompose into individual holder balances in Step B3. Step A includes these contracts in its `Transfer`-log scan so even passive holders (no ETH/nonce/code) are discovered; Step B3 then calls `balanceOf` for every EOA collected in Step A. Example: `["0xAbc...123", "0xDef...456"]`. |
| `ignoreUnsupportedL2Events` | `false` | When `true`, the Step G lite syncer logs a warning and continues instead of aborting when it sees an L2 event that would invalidate a BridgeEvent-only reconstruction (`SetSovereignTokenAddress`, `MigrateLegacyToken`, `RemoveLegacySovereignTokenAddress`, `BackwardLET`, `ForwardLET`). The computed `NewLocalExitRoot` may then be incorrect — enable only to knowingly inspect such a chain. |
| `ignoreLERMismatch` | `false` | When `true`, the AET-11 unsettled-bridge-exits verification (Step CHECK check 9, the Step 0 guard, and Step H's LER cross-check) logs a warning and continues instead of aborting when the L2 bridge's LER at the target block does not match the agglayer's `settled_ler`. The certificate then chains from a `PrevLocalExitRoot` that does not cover every emitted bridge exit, so the agglayer will most likely reject it — enable only to knowingly inspect such a snapshot. Step H's pending-certificate guard is unaffected. Rejected with `deltaMode`. |
| `deltaMode` | `false` | When `true`, builds a certificate that only carries the **new** bridge exits of `deltaExitsFile`, chained on the previous run (`previousRun`). See [Delta mode](#delta-mode--chain-additional-exits-on-a-previous-certificate). `exitAddress` is not required in this mode. |
| `previousRun` | `""` | Required with `deltaMode`: output directory of the previous (settled) run, its archive `gs://<bucket>/<base>/<networkID>/<height>`, or the archive base `gs://<bucket>/<base>` to use the run of the last settled certificate (see [Archiving runs](#archiving-runs-in-a-gcs-bucket)). Relative paths resolve from the config file directory. Must differ from `outputDir`. Setting it without `deltaMode` is rejected. |
| `deltaExitsFile` | `""` | Required with `deltaMode`: JSON file with the new exits (format in [Delta mode](#delta-mode--chain-additional-exits-on-a-previous-certificate)). Relative paths resolve from the config file directory. |
| `recoveriesPath` | `""` | Only with `deltaMode`, input of `--step recoveries`: a directory (searched recursively), a `.tgz` of the [zkevm-proof-of-ownership-kit](https://github.com/agglayer/zkevm-proof-of-ownership-kit) or a `recovery.json` with already verified recoveries. See [Recoveries step](#recoveries-step--from-verified-recoveries-to-delta-exits). Relative paths resolve from the config file directory. |
| `archiveURI` | `""` | Archive base for `--step upload`, `gs://<bucket>/<prefix>` (e.g. `gs://polygonlabs-zkevm-prod-let-archive/runs`): the run goes to `<archiveURI>/<networkID>/<height>`, height taken from the certificate. Only the upload step needs it. See [Archiving runs](#archiving-runs-in-a-gcs-bucket). |
| `archiveCacheDir` | `"archive-cache"` next to `outputDir` | Base directory archived runs are downloaded into (`<archiveCacheDir>/<bucket>/<prefix>/`): the `gs://` `previousRun` of a delta, and the claimer's `--run-source`. Relative paths resolve from the config file directory. |
| `archiveMetaFiles` | `[]` | Extra files that document the run (notes, the inputs it was built from...). `--step upload` copies each one, as is, to `<outputDir>/metafiles/<file name>` (files there no longer listed are removed) right before uploading, and archives them, compressed with zstd, with the rest of the run. File names must be unique; relative paths resolve from the config file directory. Once the output dir was uploaded they are not copied again. **Never list files holding secrets** (keystores, a config with passwords or tokens). See [Meta files](#meta-files--optionsarchivemetafiles). |
| `verifyNewLocalExitRootUsingShadowFork` | `true` | Selects the Step G2 mode. When `true` (default), Step G2 spins up an Anvil shadow-fork, replays every bridge exit against the real bridge contract, recovers the on-chain metadata, and verifies the off-chain leaf encoding against the contract's `getRoot()` (requires `anvil` in `$PATH`). When `false`, Step G2 skips Anvil — much faster, but it trusts the off-chain leaf encoding/metadata. In both modes the certificate keeps its deterministic exit order and the `NewLocalExitRoot` is the lite exit tree root in that order. See [Step G](#step-g--compute-newlocalexitroot) for details. |

### Important configuration notes

**`l1RpcUrl` — required in practice**

Although marked optional, `l1RpcUrl` is needed for Step E (unclaimed deposit detection) and Step I (`L1InfoTreeLeafCount`). In a real exit scenario you should always set it. Without it, Step E is silently skipped and the certificate may be missing unclaimed L1→L2 deposits.

**`exitAddress` — required, keep the private key**

SC-locked value (tokens held in smart contracts) is bridged to `exitAddress` on the destination network. The field is **mandatory**: `LoadConfig` errors if it is missing or set to the zero address (`0x00…00`). Use an address **whose private key you control** — once the certificate is settled, those funds can only be recovered by signing transactions from that address. If the key is lost, the value is permanently inaccessible.

The exception is `options.skipSCLockedValue: true`: the SC-locked funds are then intentionally left behind on the L2 and no exit targets `exitAddress`, so the presence and zero-address checks are skipped (a non-empty value must still be valid hex).

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

# Or every post-submission step in one go: submit, wait, upload (upload needs options.archiveURI)
./target/exit_certificate --config parameters.json --step submit-
```

| Step | Name | What it does |
| :--: | ---- | ------------ |
| CHECK | Verify prerequisites | Checks Anvil, L1 RPC, network type (PP only), threshold = 1, no custom gas token, and no unsettled L2 bridge exits at the target block (L2 bridge LER vs the agglayer's `settled_ler`, AET-11). |
| 0 | Generate LBT | Resolves `targetBlock` to a concrete block number, then scans `NewWrappedToken` events and fetches `totalSupply` per wrapped token at that block. |
| A | Collect addresses | Discovers every value-holding address from the final state (`debug_accountRange` state dump, for native-ETH holders and contracts) plus `Transfer` event logs per wrapped token (for token holders). Both sources always run and merge. |
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
| UPLOAD | Archive the run | Uploads the output directory of a settled run to `archiveURI` (write-once GCS bucket). **Not part of the default pipeline**; see [Archiving runs](#archiving-runs-in-a-gcs-bucket). |

Steps SUBMIT, WAIT and UPLOAD are **not** part of the default pipeline — they must be triggered explicitly.

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
./target/exit_certificate --config parameters.json --step submit- # submit, wait, upload (starts past sign: runs to the end)
./target/exit_certificate --config parameters.json --step 0-wait  # every step, including submit and wait
```

Each step reads its dependencies from the output directory (files written by prior steps).
Spaces around commas are ignored. Execution stops at the first step that fails.

Ranges use `from-to` (inclusive). An open-ended `from-` that starts at or before `sign` runs through `sign`: the post-submission steps (`submit`, `wait`, `upload`) are left out, so `f-` never submits by surprise, and must be named explicitly (e.g. `0-wait`, or `0-upload` to also archive the run). An open-ended range that starts at a post-submission step runs to the end: `submit-` = `submit,wait,upload`, `wait-` = `wait,upload`.

### CLI flags

| Flag | Short | Default | Description |
| :--: | :---: | :-----: | :---------: |
| `--config` | `-c` | `parameters.json` | Path to the config file. |
| `--step` | — | `all` | Step(s) to run. Accepts `all`; a single step name; a comma-separated list (e.g. `h,i,sign`); or a range `from-to` (inclusive, e.g. `a-c` → `a,b,c`). An **open-ended** range `from-` starting at or before `sign` runs through `sign` (e.g. `g-` → `g,h,i,sign`): `submit`/`wait`/`upload` are excluded and must be named explicitly (e.g. `0-wait`, `0-upload`); one starting at `submit`, `wait` or `upload` runs to the end (`submit-` → `submit,wait,upload`). Valid names: `check`, `0`, `a`, `b`/`b1`/`b2`/`b3`, `c`–`f`, `g`/`g1`/`g2`, `h`, `i`, `sign`, `submit`, `wait`, `upload` (the last step of the order); `pending_claims` (standalone, never part of a range); `recoveries` (delta mode only). The aliases `b` and `g` expand to their sub-steps and also work as range bounds. |
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
3. **`l1BridgeAddress` is the L1 bridge** — calls `networkID()` on `l1BridgeAddress` over the L1 RPC and requires 0 (the L1/mainnet network). A wrong address (a typo, a non-bridge contract, or the `l2BridgeAddress` default not existing on L1) would make Step E silently miss every unclaimed L1→L2 deposit. The outcome is recorded in the result as `l1BridgeAddressStatus`; if check 2 failed it is `unchecked` and counted as a failure.
4. **L2 network ID matches bridge** — calls `NetworkID()` on the L2 bridge contract and verifies it matches `l2NetworkId` in config.
5. **`sovereignRollupAddr` is set** — required; fails if zero address.
6. **Network type is PP** — queries `AGGCHAINTYPE()` on the `aggchainbase` contract at `sovereignRollupAddr` on L1. FEP is not supported. Only runs if checks 2 and 5 passed.
7. **Threshold is 1 + bridge addresses match** — queries the multisig threshold. Fails if > 1. Also verifies `l1BridgeAddress` matches both `aggchainbase.bridgeAddress()` and the canonical `rollupManager.bridgeAddress()` (the mismatch error shows the correct address to configure). Logs all committee signers and their URLs. Only runs if checks 2 and 5 passed.
8. **No custom gas token** — calls `gasTokenAddress()`/`gasTokenNetwork()` on the L2 bridge. Fails if a non-zero gas token is configured (not supported).
9. **No unsettled L2 bridge exits (AET-11)** — requires `agglayerClient.grpc.url` (the same requirement Step H enforces later). Reads the L2 bridge's local exit root (`getRoot()`) at the target block — the block Step 0 already resolved (`step-0-l2_target_block.json`) when present, otherwise resolved from the config on the spot — and compares it against the agglayer's `settled_ler` (refusing to proceed on a pending certificate, like Step H). A mismatch means the target block's bridge LER and the settled LER have diverged — the pipeline aborts here, before the expensive scan (Steps A/B) and replay (Step G) phases, with instructions to pick a target block whose bridge LER matches the settled LER (wait for settlement or move the target block back when the bridge is ahead; move it forward when a certificate settled past it). Step 0 re-runs this same verification on its own resolved block. The outcome is recorded in the result as `unsettledExitsStatus` (plus the two compared roots, `settledLER` / `l2BridgeLER`). With `ignoreLERMismatch=true` the failure is logged as a warning and not counted (the result still records the real outcome).

**Output:** `step-check-result.json`

### Step 0 — Generate LBT (Local Balance Tree)

#### Target block resolution

The `targetBlock` config field accepts a finality keyword, an optional offset, or a concrete block number. Step 0 resolves it to a `uint64` before doing any work. Right after the resolution — and before the LBT scan — Step 0 re-runs the unsettled-bridge-exits verification (AET-11, same as Step CHECK's check 9) on the resolved block and aborts on mismatch (a warning instead with `ignoreLERMismatch=true`); the check is skipped with a warning when `agglayerClient.grpc.url` is not configured (Step CHECK reports that as a failure).

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
2. **`Transfer` event logs** — scans `eth_getLogs` for each wrapped token (list from the Step 0 LBT) **and each extra ERC-20 contract** (`options.extraErc20Contracts`, deduplicated against the wrapped tokens) across `[0, targetBlock]`, collecting the indexed `from`/`to` of every `Transfer`. This surfaces token-only EOAs that have no nonce/balance/code and therefore appear in neither a state dump nor a trace. The scan deliberately starts at block 0 (not `l2StartBlock`) so passive holders that received tokens early are not dropped. The extra ERC-20s are scanned here (not in Step B3) because B3 only probes `balanceOf` against the Step A address set — a passive holder of an extra token would otherwise never be discovered and their collateral share would flow to `exitAddress` instead.

Both sources always run and their results are merged: they cover complementary blind spots (the state dump cannot see token-only EOAs, which do not exist in the account trie; the Transfer logs cannot see accounts that never touched a wrapped token, i.e. native-ETH holders and contracts), so neither is sufficient on its own.

The state dump fails loudly (instead of returning a truncated or empty set) when `debug_accountRange` is unavailable, when the node keeps returning a non-empty pagination cursor past the page cap, or when it returns 0 accounts (e.g. a geth archive node without address preimages) — there is no fallback, since a run without the dump would silently omit every native-ETH holder and contract.

The **zero address** (`0x000…000`) is treated like any other account: a plain `transfer(0x0, amount)` is not a burn (the tokens remain in `totalSupply`) and native ETH can be sent there too (including genesis allocs), so its balances must be scanned and covered by the certificate for the totals to reconcile with the LBT. It is **always added** to the address set rather than trusting discovery: the state dump can miss it (nodes often lack the preimage for the zero key) and Transfer logs only surface it when a mint/burn happened, which would make the genesis-preload detection depend on unrelated token activity.

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
3. Every token with SC-locked value → exits to `exitAddress` on the destination network. **Omitted entirely with `options.skipSCLockedValue: true`** — those funds are intentionally left behind on the L2 (Step C's output still records them, and Step F discounts them from the LBT/agglayer balances). The holder-bridge exits (2) are unaffected.

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

**Skipped SC-locked discount:** with `options.skipSCLockedValue: true` the certificate omits the SC-locked exits (Step D), so Step F subtracts each token's `PendingSCLockedBalance` (Step C) from the **LBT and agglayer amounts** before comparing — the check remains strict equality against the smaller certificate, and the cap budget excludes the left-behind funds. The applied discount is recorded per token as `skippedSCLockedAmount` in `step-f-checks.json` (the persisted LBT/agglayer amounts are the adjusted ones). In single-step mode `--step f` then also reads `step-c-sc-locked-values.json` (run Step C first).

**Agglayer LBT dump:** whenever `agglayerAdminURL` is set, Step F queries `admin_getTokenBalance` once at the very start and writes the full raw response (the agglayer's local balance tree for `l2NetworkId`) to `step-f-agglayer-lbt.json`, regardless of the comparison mode. In agglayer mode this same response is reused for the comparison (no second RPC). The [`scripts/get-agglayer-lbt.sh`](scripts/get-agglayer-lbt.sh) helper fetches the same data manually.

**Reads:** `step-d-exit-certificate.json`, `step-0-lbt.json`, `step-c-sc-locked-values.json` *(only with `skipSCLockedValue=true`)*

**Output:** `step-f-token-balances.json`, `step-f-checks.json`, `step-f-agglayer-lbt.json` *(only when `agglayerAdminURL` is set)*, `step-f-capped-certificate.json` *(when mismatches exist and `ignoreBalanceMismatch=true`, or when `genesisPrefundETHWei` trims the native exits)*

### Step G — Compute NewLocalExitRoot

Split into **G1** (sync the L2 bridge history from genesis up to the target block into a lite DB, resolving the shadow-fork block) and **G2** (compute the `new_local_exit_root`). In both modes the certificate's `bridge_exits` keep their deterministic incoming order and the `new_local_exit_root` is the lite exit tree root built from them in that order (the order agglayer rebuilds the LER from), so the same on-chain state always produces the same certificate. By default (`options.verifyNewLocalExitRootUsingShadowFork=true`) G2 additionally replays every `bridge_exit` against a shadow-fork of the L2 chain via [Anvil](https://getfoundry.sh), recovers the on-chain metadata, and verifies the off-chain leaf encoding against the forked contract's `getRoot()` (using a copy of the exits sorted by the replayed deposit counts — the replay's tx ordering is non-deterministic and never leaks into the certificate). Set the option to `false` to skip Anvil — faster, but it trusts the off-chain leaf encoding/metadata.

**Anvil is required in the default shadow-fork mode** (`anvil` binary in `$PATH`); the off-chain mode (`verifyNewLocalExitRootUsingShadowFork=false`) needs no Anvil. When the certificate has no bridge exits, the canonical empty LER is used.

**Reads:** `step-f-capped-certificate.json` if it exists (produced by Step F when `ignoreBalanceMismatch=true`), otherwise `step-e-exit-certificate.json`.

**Output:**

- **G1:** `step-g1-shadow-fork-block.json` (resolved shadow-fork block) and the lite syncer DB `output/step-g1-l2bridgesyncerlite.sqlite`.
- **G2:** `step-g-new-local-exit-root.json`, `step-g-reordered-certificate.json` (the certificate Step I consumes — same deterministic exit order, metadata hashes set; the file name is historical) and `step-g-l2bridgesyncerlite.sqlite` (working copy of the G1 DB with the tree built); in shadow-fork mode also `step-g-failed-exit.json` *(only on replay failure)*.

### Step H — Fetch PreviousLocalExitRoot

Calls `interop_getNetworkInfo` on the agglayer JSON-RPC and reads the `settled_ler` for the L2 network. If no certificate has been settled yet, `PreviousLocalExitRoot` is zero.

It also cross-checks Step G's `InitialLocalExitRoot` (the L2 bridge LER at the target block) against `settled_ler` and aborts on mismatch: the target block contains bridge exits no settled certificate covers (exits made before the sequencer halt that the agglayer never settled), or a new certificate settled while the certificate was being generated. Re-running with the same target block fails identically — pick a target block whose bridge LER matches the agglayer settled LER: when the bridge is ahead, wait until the agglayer settles every bridge exit up to the target block or move the target block back to the settled state; when the settled LER is ahead, move the target block forward. In the full pipeline Step CHECK (check 9) and the Step 0 guard catch this before the expensive work; in single-step mode this check is the final safety net. With `ignoreLERMismatch=true` the mismatch is logged as a warning and the step continues (the pending-certificate guard is unaffected).

Requires `agglayerClient.GRPC.URL` in options.

**Output:** `step-h-previous-local-exit-root.json`

### Step I — Assemble final certificate

Takes the certificate produced by Step G and applies:

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

### Step PENDING_CLAIMS — Unclaimed L2→L1 bridges emitted before the certificate

A **standalone** step: it is not part of `all` nor of any `--step` range, run it with `--step pending_claims`.

The certificate only carries the balances at the target block, so the L2→L1 bridges emitted earlier
(already part of a previously settled certificate) and never claimed on L1 are not in its
`bridge_exits`. Their leaves are, however, in the local exit tree and provable against the
certificate's `new_local_exit_root`. This step lists them so the claimer can serve them.

1. Reads every leaf of `step-g-l2bridgesyncerlite.sqlite` and drops the last `bridgeExitCount` ones
   (`step-g-new-local-exit-root.json`): those are the certificate's own exits.
2. Keeps the assets (`leaf_type=0`) destined to L1 (`destination_network=0`) and, **only with
   `options.pendingClaimMessages=true` or `--pending-claim-messages`**, the messages (`leaf_type=1`) too. A
   message is claimed with `claimMessage`: the claimer serves it through its API (`/claimer/v2`). The option
   is off by default so the step keeps the output of the first mainnet run (assets only); the log says which
   mode was used.
3. Calls `isClaimed(depositCount, l2NetworkId)` on `l1BridgeAddress` through `l1RpcUrl` and keeps the
   unclaimed ones (`claimAsset` and `claimMessage` share the same claimed bitmap).

Requires `l1RpcUrl` and `l1BridgeAddress` (it defaults to `l2BridgeAddress`), and the Step G output in the output directory.

```bash
exit_certificate --config parameters.toml --step pending_claims                           # assets only
exit_certificate --config parameters.toml --step pending_claims --pending-claim-messages  # assets + messages
```

**Output:** `step-pending-l2-bridges.json` (`[]PendingBridge`: `depositCount`, `blockNumber`, `leafType`, origin and destination, `amount`, raw `metadata`, `txHash`). The claimer adds them to `/bridges` and `/claim-params` when the file exists.

## Delta mode — chain additional exits on a previous certificate

A full run produces **one** certificate per chain. When more exits must be issued afterwards — typically funds locked in smart contracts that the first certificate left behind (`skipSCLockedValue`) and whose owners proved ownership (see [zkevm-proof-of-ownership-kit](https://github.com/agglayer/zkevm-proof-of-ownership-kit)) — the **delta mode** builds a certificate containing only those new exits, chained on the previous settled certificate. It can be repeated: each delta's output is a valid input for the next one.

```jsonc
// config (besides the usual l2RpcUrl, l1RpcUrl, agglayerClient, agglayerAdminURL, signerConfig, …)
"options": {
    "deltaMode": true,
    "previousRun": "./output-first",      // output dir of the previous run (or of the previous delta)
    "deltaExitsFile": "./delta-exits.json",
    "outputDir": "./output-delta-1"        // must differ from previousRun
}
```

```bash
./target/exit_certificate --config parameters.json                    # CHECK → F → G2 → H → I → SIGN
./target/exit_certificate --config parameters.json --step submit,wait # as in a regular run
./target/exit_certificate --config parameters.json --step submit-    # or submit, wait, upload in one go
```

In delta mode only `all` (the default), `recoveries`, `sign`, `submit`, `wait` and `upload` are available, also as lists and ranges (`submit-`, `sign-upload`); the L2 scan steps (0, A, B, C, D, E) do not exist.

### Delta exits file

The new exits, with the same `BridgeExit` shape as a certificate's `bridge_exits`:

```json
{
  "bridge_exits": [
    {
      "leaf_type": "Transfer",
      "token_info": { "origin_network": 0, "origin_token_address": "0x0000000000000000000000000000000000000000" },
      "dest_network": 0,
      "dest_address": "0x00000000000000000000000000000000000000d1",
      "amount": "1000000000000000000",
      "metadata": "<optional, hex without 0x>"
    }
  ]
}
```

- `Transfer` and `Message` exits are supported, with a `token_info` and a `dest_network` other than the exiting network. A `Transfer` (`bridgeAsset`) needs a positive `amount`. A `Message` (`bridgeMessage`; `--step recoveries` produces them from version-1 recoveries) has its own rules: `amount` may be `"0"` or the ETH it carries (charged to L1 ETH, as the agglayer does), `metadata` is mandatory and non-empty (it is the message), and `token_info` is not a token but the **sender**: `origin_network` must be the exiting network and `origin_token_address` a non-zero address. Example:

  ```json
  {
    "leaf_type": "Message",
    "token_info": { "origin_network": 1, "origin_token_address": "0xBDa0B27f93B2FD3f076725b89cf02e48609bC189" },
    "dest_network": 0,
    "dest_address": "0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB",
    "amount": "0",
    "metadata": "00000000000000000000000000000000b0e79c68cafc54802726c6f102cca30000000000000000000000000000000000000000000000000000000000000f4240"
  }
  ```

  A value-less `Message` moves no balance, so Step F leaves it out of the per-token check (a delta of such `Message` exits only has nothing to compare); one carrying ETH is checked with the ETH exits. It is claimed on the destination network with `claimMessage`, not `claimAsset`.
- Two delta exits identical to each other — same token (or sender), destination, amount and metadata — are rejected: they would be two leaves with the same hash with no reason to exist. Merge repeated exits into one (sum the amounts). An exit identical to a **leaf already in the exit tree** (an exit of a previous run or an older L2 bridge, read from the copied lite DB) is accepted, with a warning in the log: it is appended as a new leaf with its own deposit count and global index, so both can be claimed separately. This needs a claimer that matches certificate exits to leaves by position; one that indexes leaves by hash would serve only one of the two.
- `metadata`, when present, is the **raw** leaf metadata as plain hex (the tool hashes it): no `0x` prefix and even length — a malformed value is rejected instead of being silently dropped (which would regenerate it from the L2 and change the leaf). A certificate's `metadata` is the keccak hash: do not copy it here (a 32-byte value logs a warning). When absent the tool derives it from the L2 bridge the way `bridgeAsset` does: the ABI-encoded name/symbol/decimals of the L2 token for ERC-20 exits, the gas-token metadata for native exits. A `Message`'s metadata is the message itself: it is used as given, never derived, and an exit without it is rejected.
- **The L2 RPC (`l2RpcUrl`) must still answer in delta mode**: it is used for the L2 bridge's gas token and for that metadata derivation (the exits produced by `--step recoveries` from legacy-format cases carry none; version-1 cases carry the metadata of their `BridgeEvent`s). The delta run never reads balances from it (`--step recoveries` reads the version-1 contracts' balances at the frozen snapshot block).

### Pipeline

| Step | Delta behaviour |
| ---- | --------------- |
| CHECK | Same prerequisites (L1, network ID, PP, threshold, gas token) minus Anvil. **AET-11 is adapted:** the agglayer's `settled_ler` must equal the previous certificate's `new_local_exit_root` (and the next height must be the previous + 1) instead of the L2 bridge `getRoot()`. Fails if a certificate is pending or the previous one is not the latest settled. `ignoreLERMismatch` is **rejected** with `deltaMode` (a delta chains on `previousRun` by construction). |
| F | Per token, the sum of the new exits must be ≤ the remaining agglayer balance (`admin_getTokenBalance`, which already reflects the previous certificates). An excess **always fails**: `ignoreBalanceMismatch`/`capMode` do not apply in delta mode (capping would leave the recoveries ledger saying more was paid than the certificate carries). The check **cannot be skipped**: `useAgglayerAdminToStepFCheck=false` is an error in delta mode. |
| G2 | Copies the previous run's `step-g-l2bridgesyncerlite.sqlite`, appends the new exits as leaves **continuing the deposit counts** (old leaves keep their index, so their global index does not change) and takes the new root as `new_local_exit_root`. The previous tree's root is verified against the previous certificate first. |
| H | `prev_local_exit_root` = previous `new_local_exit_root`, `height` = previous height + 1, cross-checked against the agglayer. |
| I, SIGN, SUBMIT, WAIT | Unchanged. SIGN needs `signerConfig`: without it `--step all` still finishes but does not write `exit-certificate-signed.json` (it warns), and `--step submit` then fails. |

### Output (reusable as the input of the next delta)

| File | Content |
| ---- | ------- |
| `exit-certificate-signed.json` | The delta certificate: **only the new exits**. This is what is submitted to the agglayer. |
| `exit-certificate-full.json` | The same certificate shape carrying **every** exit (previous runs + this delta, in leaf order) with the latest `new_local_exit_root`. Not submittable; it is the input of the next delta and of the [claimer](../exit_certificate_claimer/README.md). |
| `step-g-l2bridgesyncerlite.sqlite` | The exit tree with all leaves (old + new). |
| `step-wait-result.json` | Settlement record of the delta certificate. |
| `delta-manifest.json` | Links previous run → this run: certificate heights/hashes, root before/after, leaf index range added, exit counts. When `previousRun` was an archive, also `previousRunArchive` (its URI) and `previousRunArchiveManifestSha256`. |

A delta's `previousRun` can be the output dir of a regular run (its signed certificate already holds every exit) or of a previous delta (its full certificate is used), or the [archive](#archiving-runs-in-a-gcs-bucket) of either.

## Archiving runs in a GCS bucket

Each run (full or delta) is the input of the next delta and of the claimer, so its output directory must be
kept intact. The tool can archive it in a **write-once** GCS bucket and read it back from there:

| Bucket | Environment |
| ------ | ----------- |
| `polygonlabs-zkevm-prod-let-archive` | zkEVM mainnet |
| `polygonlabs-zkevm-bali-let-archive` | Bali (to rehearse the process) |

Objects in these buckets can be created but never deleted or overwritten (retention policy). Writers need
`roles/storage.objectCreator` + `roles/storage.objectViewer`; readers (a delta's `previousRun`, the claimer)
only `roles/storage.objectViewer`.

**Credentials:** Application Default Credentials — `gcloud auth application-default login` on a laptop
(workforce identity via Okta), the workload identity / service account of a pod, or
`GOOGLE_APPLICATION_CREDENTIALS`. `STORAGE_EMULATOR_HOST` points the client at a GCS emulator instead.

### Layout

One prefix per run, named after the **certificate it settled**: `<archiveURI>/<networkID>/<height>/`, the
height zero-padded to 10 digits so runs sort by height. Each file of the output directory is stored
**compressed with zstd** as `<relative path>.zst`; only `archive-manifest.json` is plain JSON:

```text
gs://polygonlabs-zkevm-prod-let-archive/
└── runs/                                   ← options.archiveURI (the base)
    └── 1/                                  ← network ID
        ├── 0000000004/                     ← the full run (certificate height 4)
        │   ├── archive-manifest.json
        │   ├── exit-certificate-signed.json.zst
        │   ├── step-g-l2bridgesyncerlite.sqlite.zst
        │   ├── step-wait-result.json.zst
        │   └── ...                         (every other file of the output dir, .zst)
        └── 0000000005/                     ← delta 1 (height 5)
            ├── archive-manifest.json
            ├── exit-certificate-full.json.zst
            ├── delta-manifest.json.zst
            ├── recoveries/...zst
            └── ...
```

One height is one run: the bucket is write-once, so a second, different run for the same height cannot be
uploaded. After the sunset every certificate of the network is one of ours, so the heights are consecutive and
the run that follows height `h` is always at `h+1`.

`archive-manifest.json` has `"version": 1` (the manifest format), `"dataVersion"` (the format of the archived
files, see [Versions](#versions)), `"compression": "zstd"` and lists every file with the size and SHA-256 of the
**original** file plus the name, size and CRC32C of its compressed object. Its `info` records the run: tool
version, `full`/`delta`, network ID, `height`, `prevLocalExitRoot`, `newLocalExitRoot`, certificate hash,
`createdAt`, and for a delta the archive of its previous run. It is uploaded **last**: a prefix without it is
an incomplete upload and is never read.

### Upload — `--step upload`

```bash
# after --step submit,wait (and --step pending_claims, if you want it archived)
./target/exit_certificate --config parameters.toml --step upload
# or submit, wait and upload in one go (pending_claims is standalone: run it before if you want it archived)
./target/exit_certificate --config parameters.toml --step submit-
```

Set `options.archiveURI` to the archive **base** (e.g. `gs://polygonlabs-zkevm-prod-let-archive/runs`). The
step:

- archives only a **settled** run (`step-wait-result.json` with status `Settled`) that has its certificate
  (`exit-certificate-signed.json`, or `exit-certificate-full.json` in delta mode);
- takes the run prefix from that certificate's header (`network_id`, `height`), checked against `l2NetworkId`
  and against the height Step H wrote (`step-h-previous-local-exit-root.json`);
- uploads every file of `outputDir` except the claimer's L1 Info Tree DBs (`L1InfoTreeSync.sqlite`,
  `l1infotree_multidownloader.sqlite`), SQLite side files (`-wal`/`-shm`/`-journal`) and `archiveCacheDir` if
  it is inside `outputDir`. Each SQLite DB is checkpointed first, so its main file is complete (it fails if a
  process — e.g. a running claimer — still holds it busy);
- compresses each file into `<archiveCacheDir>/.upload-tmp/` before uploading it (the store checks the
  compressed CRC32C, which must be known up front), so that directory needs room for the largest compressed
  file. As a reference, the Cardona exit-tree DB goes from 734 MB to 229 MB;
- never overwrites: each object is created with a "does not exist" precondition and carries the original
  file's SHA-256 as metadata (`source-sha256`). An object already there for the same original file is kept,
  so **re-running after a failed upload resumes it**; one made from another file is an error. If the prefix
  already has a manifest, the step succeeds without uploading when it lists the same files, and fails
  otherwise (another run already settled that height);
- writes a local copy of the manifest (`archive-manifest.json`) in `outputDir`.

Upload is the last step of a run: the archive cannot be changed, so files written afterwards (e.g. a late
`--step pending_claims`, which warns about it) are not archived.

### Meta files — `options.archiveMetaFiles`

Files that are not produced by the tool but document the run — operator notes, the delta exits file, the
recoveries kit, a sanitized copy of the config — can be kept with it by listing them in
`options.archiveMetaFiles`:

```toml
[options]
archiveMetaFiles = ["./RUN-NOTES.md", "./delta-exits.json", "./recoveries.tgz"]
```

Only `--step upload` uses them: right before collecting the files to archive, it copies them, as they are,
into `outputDir/metafiles/`, and they are uploaded like any other file of the run, so compressed with zstd
once (a file that was already compressed, like the `.tgz`, is compressed again all the same):

```text
output/metafiles/RUN-NOTES.md   →   gs://<bucket>/runs/1/0000000005/metafiles/RUN-NOTES.md.zst
output/metafiles/recoveries.tgz →   gs://<bucket>/runs/1/0000000005/metafiles/recoveries.tgz.zst
```

A file removed from the list is removed from `metafiles/` too. A missing file, a directory or two entries with
the same file name are errors. A re-run of a failed upload copies them again (an unchanged file resumes as
usual); once the output dir has been uploaded (`archive-manifest.json` present) they are not copied again: the
archive is write-once. **Do not list files holding secrets** — the archive is readable by everyone with
`roles/storage.objectViewer`.

### Versions

The manifest carries two independent versions:

| Field | What it versions | Today |
| ----- | ---------------- | ----- |
| `version` | The manifest itself (its fields, how objects are named and compressed). | `1` |
| `dataVersion` | The format of the archived files as their readers see it: the certificate JSON, the lite syncer DB schema (`step-g-l2bridgesyncerlite.sqlite`), `step-wait-result.json`, `step-pending-l2-bridges.json` and the `recoveries/` state. | `1` |

- `--step upload` records the `dataVersion` of the tool that produced the run (`ArchiveDataVersion` in
  `archive.go`). **Bump it whenever a change to one of those files would break an older reader**, and extend
  the range each reader supports (`supportedArchiveDataVersions` for a delta, `SupportedDataVersions` in the
  claimer) once it can read the new format.
- A manifest without `dataVersion` (the first archived runs) is read as `dataVersion` 1.
- A reader that finds a `version` or `dataVersion` it does not support fails with a distinct error
  (`ErrUnsupportedVersion`: "use a newer version of the tool"), told apart from a corrupted manifest, and
  downloads nothing. A **delta** stops there: chaining on an older run is never valid. The **claimer** refuses
  such a run at startup, and in follow mode keeps serving the run it has (see its README).

### Delta from an archived run

Set the delta's `options.previousRun` to the archive **base** and the tool finds the latest run on its own:

```toml
[options]
deltaMode = true
previousRun = "gs://polygonlabs-zkevm-prod-let-archive/runs"   # the base: use the run of the last settled certificate
deltaExitsFile = "./delta-exits.json"
outputDir = "./output-delta-1"
archiveURI = "gs://polygonlabs-zkevm-prod-let-archive/runs"   # --step upload puts this delta at runs/1/0000000005
```

- The tool asks the agglayer (`agglayerClient.grpc.url`) for the network's last settled height `H` and reads
  `<base>/<l2NetworkId>/<H>/` directly: the prefix is computed, the bucket is never listed (read access to
  objects is enough).
- It fails with a clear error when that prefix has no manifest (the last settled run was not archived: run
  `--step upload` on its output dir, or use its local directory), when the manifest says another
  `newLocalExitRoot` than the agglayer's `settled_ler`, when the network has a pending certificate, or when it
  has no settled certificate at all. It never falls back to an older run.
- Step CHECK and Step H still check the agglayer's `settled_ler` and height against the downloaded
  certificate, as with any `previousRun`.
- `delta-manifest.json` records the run that was chosen (`previousRunArchive`, e.g.
  `gs://…/runs/1/0000000004`) and the SHA-256 of its manifest.

`previousRun` is taken as a **run** when it ends with `/<networkID>/<height as 10 digits>` and as the **base**
otherwise. To pin the run yourself, give its prefix:

```toml
[options]
deltaMode = true
previousRun = "gs://polygonlabs-zkevm-prod-let-archive/runs/1/0000000004"   # the run of height 4
deltaExitsFile = "./delta-exits.json"
outputDir = "./output-delta-1"
archiveURI = "gs://polygonlabs-zkevm-prod-let-archive/runs"   # --step upload puts this delta at runs/1/0000000005
```

`--step all` and `--step recoveries` download what a delta reads — the certificate (full, else signed),
`step-wait-result.json`, `step-g-l2bridgesyncerlite.sqlite`, `delta-manifest.json`, the balance state
(`step-0-lbt.json`, `step-c-sc-locked-values.json`, `step-f-checks.json`) and `recoveries/` — into
`<archiveCacheDir>/<bucket>/<prefix>/`, decompress them while checking each object's size and CRC32C and each
file's size and SHA-256 against the manifest, and then run exactly as with a local directory. Files already in the cache with the right content are not
downloaded again. The cache directory belongs to that one archived run: a different manifest or a file the
manifest does not list there is an error (remove the directory). SQLite side files (`-wal`/`-shm`/`-journal`)
left next to a cached database, e.g. by a process that crashed, are removed before it is reused or replaced,
because SQLite would replay them onto it; if another process still has that database open, the download fails
instead (`ErrFileInUse`). The delta records the archive URI and its
manifest SHA-256 in `delta-manifest.json`. Read access to the bucket is enough.

The claimer can serve an archived run too: `runSource` in its config, or `--run-source` — see the
[claimer README](../exit_certificate_claimer/README.md#serving-an-archived-run).

## Recoveries step — from verified recoveries to delta exits

`--step recoveries` (delta mode only, standalone) turns the recoveries that the
[zkevm-proof-of-ownership-kit](https://github.com/agglayer/zkevm-proof-of-ownership-kit) already verified into the
`deltaExitsFile` of a delta run, and keeps the balance files of the first run up to date so the same funds are
never released twice. Whether each case is legitimate is a **manual prerequisite** (the kit's verification); the
step only checks that the money can be taken from the balance state we have (the L2 RPC no longer reflects
reality and the agglayer offers no per-contract view).

```bash
./target/exit_certificate --config parameters.json --step recoveries
```

Config (besides `deltaMode`, `previousRun`, `outputDir`): `options.recoveriesPath`.

Two `recovery.json` formats are accepted, and can be mixed in the same run:

- **Legacy** (no `version` field): `locked_contract`, `destination`, `funds[]` with `token` = `native` or the L2
  token address, and `amount` in base units. The L2 token is resolved to its origin through `step-0-lbt.json`.
- **Version 1** (`"version": 1`): the bridge exits themselves — one `outputs[]` entry per `BridgeEvent` that the
  case's `move.sh` emitted on the kit's shadow-fork (built by the kit's `build-recovery.sh`) — and the contracts
  that paid for them (`origin_SC_funds`):

  ```json
  {
    "version": 1,
    "outputs": [
      {
        "leaf_type": "Transfer",
        "token_info": { "origin_network": 0, "origin_token_address": "0x0000000000000000000000000000000000000000" },
        "destination_network": 0,
        "destination_address": "0x4512b32d20424960b4c7f11cecb02063e31d85ad",
        "amount": "14204531250000",
        "metadata": "0x",
        "deposit_count": 81234
      }
    ],
    "origin_SC_funds": [
      {
        "address": "0x5c8bdb3e8a8bba8ba3a4a6da6b26c6c3bd1c0e2d",
        "tokens": [
          {
            "token_info": { "origin_network": 0, "origin_token_address": "0x0000000000000000000000000000000000000000" },
            "amount": "14204531250000"
          }
        ]
      }
    ]
  }
  ```

  The field names and rules are the kit's (`recovery-tx/validate-recovery.sh`): `destination_network` /
  `destination_address` (not the `dest_*` names of a certificate), a labelled `leaf_type`, `amount` as a base-10
  string, `metadata` as `0x` hex, every field required and no other field. `leaf_type` is `Transfer` (bridgeAsset:
  `token_info` is the token, ETH is `0`/`0x0…0`; it must go to network 0, with a positive amount, and bridge ETH or
  a token in the LBT — not one native to the L2) or `Message` (bridgeMessage: `token_info` is the sender — the L2
  network and a non-zero address —, `metadata` is the message and cannot be empty, and the destination network
  cannot be the L2). A `Message` may carry ETH (`bridgeMessage` is payable): its `amount` is charged to ETH, as the
  agglayer does. `origin_SC_funds` lists each contract once (`address`), each token once per contract, with the
  total it bridged out as a positive base-10 string; per token, the amounts over all contracts must add up to what
  the outputs bridge (a Transfer its `token_info`, a Message ETH; outputs of amount `"0"` pay for nothing).

  **`deposit_count`** (uint32, required) is the deposit count the `BridgeEvent` got on the kit's fork. The kit
  verifies a case on a fork that first replays every exit certified so far, so the fork's exit tree is the one
  after the last certificate and the deposit counts of `move.sh` are the ones the exits get in the next delta.
  *Pending in the kit*: its current `build-recovery.sh` (PR #6) does not write `deposit_count` yet; this tool
  already requires it.

**Anti-drain checks of version-1 cases** (they need `l2RpcUrl` and the previous run's exit tree):

- **per-contract budget**: for every (contract, token) of `origin_SC_funds`, the contract's balance at the frozen L2
  snapshot block — the first run's target block (`step-0-l2_target_block.json`, carried in the recoveries state; the
  L2 is halted) — read with `eth_getBalance` for ETH or `balanceOf(contract)` on the L2 token (resolved through the
  LBT) must cover what earlier deltas already recovered from it plus what this batch takes (several cases of one
  batch add up). The cumulative amounts are kept in `recoveries-sc-funds.json`, delta to delta.
- **deposit counts (anti-replay)**: each output's `deposit_count` must be the deposit count its exit gets in this
  delta: the previous run's exit tree size (its lite DB) + the exit's position in `delta-exits.json` (the merged
  legacy exits first, then the version-1 outputs in case and output order). A stale case — built on an older tree,
  or an old claim replayed — carries deposit counts already taken and is refused, naming the case, the output and
  both values. Several version-1 cases in one batch therefore must have been simulated by the kit in that same
  order (each on top of the previous). The recorded deposit count goes to the ledger (`depositCount`), and the
  delta run's Step G2 checks again that the leaf it appends at that deposit count is that exit (a
  `delta-exits.json` edited or reordered after `--step recoveries` is refused).

The exits of legacy cases come first: one Transfer exit to L1 per (destination, origin token), summing the cases
that coincide, sorted (as before version 1 existed). The outputs of version-1 cases follow **one exit per output,
field by field and in the same order** (the order of the kit's `BridgeEvent`s; cases in path order): they are never
merged, sorted or re-derived, and each keeps its `metadata`. Two exits of the batch that would be the same leaf
— two identical outputs of one case or of two cases, or a version-1 Transfer identical to the exit of a legacy
(destination, token) — are refused here, naming the case and output, because the delta run would reject them
after the ledger recorded them; a Transfer with empty `metadata` counts as identical to one with any metadata, since
the delta run fills it in with the token's. An output identical to a leaf already in the exit tree is accepted (see
[Delta exits file](#delta-exits-file)). Checks, all or nothing (on any
error nothing is written):

- same validation as the kit's `validate-recovery.sh` of each format (no unknown fields, every field present; legacy:
  no placeholder addresses, positive amounts, no repeated token; version 1: labelled `leaf_type`, `0x` hex metadata,
  amounts as strings, `origin_SC_funds` totals, plus no placeholder `destination_address`, a positive amount for a
  Transfer, the Message rules above and a `deposit_count` per output); an unsupported `version` is an error;
- **idempotence**: a case whose case hash (the kit's: sha256 of `recovery.json` and `move.sh`) is already in the
  ledger is skipped, not an error; a `(locked_contract, token, destination)` already recovered by a *different* case is an error
  — one recovery per contract+token+destination, ever (the ledger cannot tell a re-packaged copy of an applied case
  from a new one); a contract holding funds of several owners is recovered once per owner (another destination is
  accepted), always bounded by the token's pending balance; a `recovery.json` already in the ledger under another
  case hash (the same recovery re-packaged with another `move.sh`) is an error. Ledgers written before version 1
  existed are read as they are: their cases keep their case hash and are still skipped;
- **one case per archive, each case once per batch**: a `.tgz` with more than one `recovery.json`/`move.sh` is an
  error, and so is a directory holding the same `recovery.json` twice (e.g. a kit `.tgz` and its extracted files; a
  loose `recovery.json` without `move.sh` hashes only itself, so it would not look like the `.tgz`'s case);
- per token, the sum of the new recoveries ≤ `pendingSCLockedBalance` still left (Step C), and ≤ the
  `skippedSCLockedAmount` of Step F (the ETH a Message carries counts as ETH). What a Message triggers on the
  destination network (e.g. a contract paying out in `onMessageReceived`) is **not** checked here — that is part of
  the manual review of the case;
- the version-1 per-contract budget and deposit counts above.

Input state: `<previousRun>/recoveries/` if a previous recoveries run left it there, else the files of
`previousRun` when it is a first (non-delta) run. A previous delta run **without** `recoveries/` state is refused
(its balances would be stale). The previous run is never modified. Output, in `<outputDir>/recoveries/`:

| File | Content |
| ---- | ------- |
| `step-c-sc-locked-values.json` | `pendingSCLockedBalance` −= recovered; `recoveredSCLockedBalance` += recovered. |
| `step-f-checks.json` | recovered amount moves from `skippedSCLockedAmount` to `certificateAmount` / `lbtAmount` (Step F reports `lbtAmount` net of the skipped SC-locked value). |
| `step-0-lbt.json` | unchanged copy, so the directory is a complete state for the next run. |
| `step-0-l2_target_block.json` | copy of the L2 snapshot block (when the input state has it), for the next run's per-contract budget. |
| `recoveries-sc-funds.json` | per (contract, token) of the version-1 `origin_SC_funds`: balance at the snapshot block and cumulative amount recovered. |
| `recoveries-applied.json` | ledger of every applied (case, token) — legacy — or (case, output) — version 1, which adds `recoveryHash`, `version`, `leafType`, `destinationNetwork`, `metadata` and `depositCount` and has no `lockedContract`. |
| `delta-exits.json` | **only the exits of this run**: set it as `options.deltaExitsFile`. |
| `recoveries-report.md` | Transfer / Message exit counts, per token: pending before / recovered / pending after, and per version-1 contract: snapshot / recovered before / this run / left. |

The state lives in a `recoveries/` subdirectory because the delta pipeline itself writes `step-f-checks.json` in
`outputDir`. Run the step on every delta (with `previousRun` = the last settled run) so the chain keeps its state.

The step **refuses to run again into an `outputDir` that already has `recoveries/` output** (the state is always read
from `previousRun`, so a second run would overwrite the output and drop the earlier cases). Put every case of the
delta in `recoveriesPath` and remove `<outputDir>/recoveries/` to regenerate it.

## Result

After the full flow completes (the certificate is built and signed, then SUBMIT and WAIT succeed):

- **The agglayer holds every bridge exit in the certificate.** Once the certificate settles, the agglayer accounts for all of the certificate's `bridge_exits` — the value has been bridged out of the L2 and is ready to be claimed on the destination network.
- **The files needed to claim those bridges have been generated.** Claiming each exit requires calling `claimAsset` (or `claimMessage`, for a `Message` exit) on the bridge contract with Merkle proofs and the exit roots. The companion [`exit_certificate_claimer`](../exit_certificate_claimer/README.md) tool consumes the exit_certificate output and produces the parameters for each `claimAsset` / `claimMessage` call.

The output files the claimer needs are:

| File | Used for |
| ---- | -------- |
| `exit-certificate-signed.json` | The signed certificate — source of each exit's `originNetwork`, `originTokenAddress`, `destinationNetwork`, `destinationAddress`, `amount`, `metadata`. With [delta mode](#delta-mode--chain-additional-exits-on-a-previous-certificate) the claimer uses `exit-certificate-full.json` (every exit, latest root) instead. |
| `step-g-l2bridgesyncerlite.sqlite` | The L2 local exit tree — used to build the `smtProofLocalExitRoot` proof of each leaf against `new_local_exit_root`. |
| `step-pending-l2-bridges.json` | *Optional* (Step PENDING_CLAIMS). Unclaimed L2→L1 bridges emitted before the certificate; the claimer serves them along with the certificate's exits. |
| `step-wait-result.json` | The WAIT step's L1 settlement record (`VerifyBatchesTrustedAggregator` + the `UpdateL1InfoTree`/`UpdateL1InfoTreeV2` events) used to anchor the claim to the settled global exit root. |

Archive the output directory with [`--step upload`](#archiving-runs-in-a-gcs-bucket) once the run is complete:
the next delta and the claimer can then read these files from the bucket.

## Testing

From the repository root:

```bash
go test ./tools/exit_certificate/...
```
