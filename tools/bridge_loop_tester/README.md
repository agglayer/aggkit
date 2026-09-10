# Bridge Loop Tester

A long-running soak-test tool that continuously moves value around **circular** bridge routes —
covering L1→L2, L2→L1 and L2→L2 in the same ring — using **ETH** and a **deployed ERC20**. Every
observation goes through the aggkit-proxy REST API (`/bridge/v1` + `/tracker/v1`); every
transaction goes through the JSON-RPC endpoint of the network it is submitted on. Nothing reads a
database or an aggkit component's internal storage directly.

This document is the operator's reference: the circular-route model, the claim-mode contract, the
full config schema, every CLI command, and — most importantly for a tool meant to run unattended
for days — how to tell a healthy ring from a stranded one and what to do about each failure class.

> **Validation status.** As of this writing the tool is unit/mock-tested only (see
> [`config.go`](config.go), [`network.go`](network.go), [`proxy.go`](proxy.go), [`hop.go`](hop.go),
> [`orchestrator.go`](orchestrator.go) and their `_test.go` files). It has **not** been exercised
> against a live chain, a live aggkit-proxy, or a real autoclaim service. That is the subject of the
> e2e test and live-env runs that follow this step in the plan. Nothing below should be read as a
> claim of field-proven behavior.

## Table of contents

- [What it does](#what-it-does)
- [The circular-route model](#the-circular-route-model)
- [The claim-mode contract: `auto` vs `manual`](#the-claim-mode-contract-auto-vs-manual)
- [How a claim is attributed](#how-a-claim-is-attributed)
- [Quick start](#quick-start)
- [Config reference](#config-reference)
- [Commands](#commands)
- [Operating it: diagnosing a healthy vs. a stranded ring](#operating-it-diagnosing-a-healthy-vs-a-stranded-ring)
- [Failure policy](#failure-policy)
- [The state file](#the-state-file)
- [Metrics](#metrics)
- [Gas drain, funding and `MinNativeReserve`](#gas-drain-funding-and-minnativereserve)
- [`DryRun` semantics](#dryrun-semantics)
- [Known limitations](#known-limitations)

## What it does

Per configured `Loop`, the tool repeatedly drives one asset (ETH or an ERC20 it deploys itself)
around a closed ring of bridge hops. Because the ring is closed, the value returns to its starting
network at the end of every cycle — a run can go on for days without ever needing to be refunded;
the only thing consumed is gas on each hop's source network.

Every hop goes through the same state machine (see [`hop_state.go`](hop_state.go) /
[`DESIGN.md`](DESIGN.md) §4): resolve the asset, approve if needed, submit `bridgeAsset`, wait for
the deposit to be indexed into the L1 info tree, wait for the corresponding leaf to be injected on
the destination, fetch the claim proof, wait for (or perform) the claim, and verify the destination
balance moved by exactly `Amount`. Every wait is a poll against the aggkit-proxy, never a raw chain
scan and never a fixed sleep.

## The circular-route model

A `Loop` is a list of `Hop`s where hop *i*'s `Destination` equals hop *i+1*'s `Source`, and the last
hop's `Destination` equals the first hop's `Source`. The minimal ring is 2 hops (there and back); a
3-network ring such as `0 → 1 → 2 → 0` exercises all three bridge directions in one loop:

```
        hop 0 (auto)           hop 1 (manual)          hop 2 (auto)
  L1 (0) ----------> L2A (1) ------------> L2B (2) ----------> L1 (0)
   ^                                                              |
   +--------------------------------------------------------------+
                          value is back home; next cycle starts
```

Between cycles, a healthy loop is **always at rest on its origin network** (`Hops[0].Source`). This
is the single fact the whole operational model rests on: if a loop's value is ever found somewhere
else, a cycle stopped mid-ring and left value behind. See
[Operating it](#operating-it-diagnosing-a-healthy-vs-a-stranded-ring).

## The claim-mode contract: `auto` vs `manual`

Every hop declares who is expected to claim the bridged value on its destination network:

- **`Claim = "auto"`** — an autoclaim service is expected to claim this hop. The tool only *waits*
  (`Global.ManualGracePeriod`) and *asserts* the claim happened; it never submits the claim itself.
- **`Claim = "manual"`** — the tool asserts that **nothing** claims the hop during
  `Global.ManualGracePeriod`, and only then submits `claimAsset`/`claimMessage` itself.

This is the tool's entire reason to exist: it is a live test of a per-network autoclaim policy, and
the two modes fail in ways that mean opposite things:

- **A failing `auto` hop** (nobody claimed it within the grace period) proves the autoclaim service
  that is supposed to serve that route **did not claim it** — the policy that should be enabled for
  this route is not working, missing, or misconfigured.
- **A failing `manual` hop** (something claimed it *before* the tool did) proves the opposite: **something
  is claiming a route that was configured to have no autoclaim at all** — an autoclaim policy is
  enabled somewhere it should not be, or another actor entirely is racing the tool's own claims.

Both are reported identically as `FailureClaimMode` / `*ClaimModeViolationError`, carrying the
expected mode, the observed `ClaimActor` (`none`, `tool`, `external`, or `unknown`), the deposit's
identity (`Source`, `Destination`, `DepositCount`, `GlobalIndex`), the grace period used, and both
transaction hashes when known. This
violation is **never retried** — see [Failure policy](#failure-policy) — because retrying would turn
a real autoclaim-policy defect into an invisible delay instead of a reported test failure.

## How a claim is attributed

Whether a hop passes or fails is decided by the destination bridge's own `isClaimed(depositCount,
sourceNetwork)` read — nothing else. *Who* claimed it is a separate question, answered afterwards,
and reported as `ClaimedBy` (a `ClaimActor`) plus `ClaimAttribution` (where that answer came from):

| `ClaimAttribution` | What named the claim transaction |
|---|---|
| `self` | The tool submitted the claim; the hash is from its own receipt. |
| `chain` | The destination bridge's own `ClaimEvent`/`DetailedClaimEvent` log, found by scanning the destination's blocks from the hop's start block. The claimant is then recovered from that transaction's signature (`eth_getTransactionByHash`). |
| `proxy` | The proxy's `GET /bridge/v1/claims` record, used only when the log could not be located — a hop resumed across a restart, whose claim predates the scanned window, or a node that refuses the log query. The claimant is still recovered from the transaction's signature: the record never populates `from_address`. |
| *(empty)* | Nothing named it. `ClaimedBy` is then `unknown`. |

The chain is preferred because the log is written in the very block that makes `isClaimed` true, so
it cannot lag the decision the hop has already observed. The proxy's record is served by a claim
syncer that trails the chain, and was measured never serving a record at all for 2 of 9 genuinely
claimed deposits on a healthy network (raising the lookup budget to five minutes did not help).

Attribution is a diagnostic, so all of it is best-effort: a node that will not serve the log **and**
a proxy that will not serve the record leaves `ClaimedBy = unknown` rather than failing a hop whose
on-chain outcome is already settled. What it never does is guess — `external` and `tool` are only
reported when a transaction was actually identified, or when the tool submitted the claim itself.

## Quick start

```bash
# From the repo root
make build-tools   # produces target/bridge_loop_tester

# Adapt an example config — see config-examples/README.md
cp tools/bridge_loop_tester/config-examples/example.toml my-config.toml
# edit my-config.toml: RPCURL, BridgeAddr, Signer, ProxyURL for your environment

# Preflight only — no transaction is sent
./target/bridge_loop_tester validate --cfg my-config.toml

# Deploy the ERC20 loop's token once (repeat only if you want a fresh token)
./target/bridge_loop_tester deploy-token --cfg my-config.toml --network 1 --loop erc20-ring \
  --mint 1000000000000000000000

# Run
./target/bridge_loop_tester run --cfg my-config.toml
```

## Config reference

The config is a standalone TOML file (or several, merged in order via repeated `--cfg`/`-c`, later
files overriding earlier ones), decoded by `LoadConfig` into the exported `Config` type
(`tools/bridge_loop_tester/config.go`). It is not derived from aggkit's own config
template/defaults pipeline. Wei-scale values always use the `WeiAmount` type, which decodes **only**
from a quoted decimal string in TOML (e.g. `Amount = "1000000000000000000"`), never from a bare
number — this avoids the silent precision loss a `float64` or `int64` would risk on wei-scale
amounts.

### `[Global]`

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `ProxyURL` | string | **yes** | — | Base URL of the aggkit-proxy REST API, must expose `/bridge/v1` and `/tracker/v1` (e.g. `"http://127.0.0.1:15601"`). |
| `LogLevel` | string | no | `"info"` | `debug`, `info`, `warn`, `error`, `dpanic`, `panic`, `fatal`. |
| `Iterations` | uint64 | no | `0` | Cycles to run per enabled loop; `0` = forever (the soak-test mode). Overridable with `run --iterations`. |
| `LoopDelay` | duration | yes (>0 after defaulting) | `5s` | Sleep between successive cycles of a loop. Also the spacing between hop-retry attempts within a cycle. |
| `HopTimeout` | duration | yes (>0) | `10m` | Total budget for one hop's whole state machine; every individual readiness gate's deadline is whatever remains of this budget. |
| `PollInterval` | duration | yes (>0) | `5s` | How often the `/bridge/v1/*` readiness endpoints are re-polled. |
| `ManualGracePeriod` | duration | yes (>0) | `2m` | For a `manual` hop: how long to wait and assert nothing else claims it before self-claiming. For an `auto` hop: how long to wait for the autoclaim service before declaring a violation. |
| `HopAttempts` | uint64 | yes (>0) | `3` | How many times a hop is attempted in total (the first attempt plus any retries after a transient failure; see [Failure policy](#failure-policy)) before its loop's cycle is abandoned at that hop without halting the loop. |
| `StatePath` | string | no | `""` (disabled) | File used to persist/resume hop state across restarts. See [The state file](#the-state-file). |
| `MetricsAddr` | string | no | `""` (disabled) | `host:port` to serve Prometheus metrics on. See [Metrics](#metrics). |
| `DryRun` | bool | no | `false` | See [`DryRun` semantics](#dryrun-semantics). Overridable with `run --dry-run`. |

### `[[Networks]]`

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `NetworkID` | uint32 | **yes**, unique | — | aggkit network ID (`0` for L1, `1..N` for rollups/L2s). |
| `Name` | string | **yes** | — | Human-readable label used in logs, errors and metrics labels. |
| `RPCURL` | string | **yes** | — | JSON-RPC endpoint. |
| `BridgeAddr` | address | **yes**, non-zero | — | `PolygonZkEVMBridgeV2` contract address on this network. |
| `ChainID` | uint64 | no | `0` | `0` means resolve live via `eth_chainId` at startup rather than trust a possibly-stale configured value. If set, it is cross-checked against the live value. |
| `MinNativeReserve` | `WeiAmount` (wei) | no | `0` | Floor native balance the signer must keep; the tool refuses to spend below it. See [Gas drain](#gas-drain-funding-and-minnativereserve). |
| `GasLimitOffset` | uint64 (gas units, **not wei**) | no | `0` | Added to every `eth_estimateGas` result before submitting, as a safety margin against a node whose estimate races the state the transaction will actually execute against. **Set it** (300000 is a good starting point) on any network that will see concurrent bridges: `bridgeAsset` is sent with `forceUpdateGlobalExitRoot = true`, so two bridges in the same block make the second one cost more than its own estimate predicted and revert `OutOfGas` inside `updateExitRoot`. |
| `[Networks.Signer]` | `signertypes.SignerConfig` | **yes** (`Method` non-empty) | — | Local keystore, AWS KMS, or GCP KMS — see `github.com/agglayer/go_signer/signer/types`. |

### `[[Loops]]`

| Field | Type | Required | Default | Notes |
|---|---|---|---|---|
| `Name` | string | **yes** | — | Human-readable label used in logs, metrics and the state file's map key. |
| `Asset` | `"eth"` \| `"erc20"` | **yes** | — | `"eth"` only works on a network whose bridge `gasTokenAddress()` is the zero address — verified **live** by `validate`/`run` preflight, not merely assumed (see Gap G3 in `DESIGN.md`). |
| `Amount` | `WeiAmount` | **yes**, `> 0` | — | Amount moved on **every** hop of **every** cycle. |
| `Enabled` | bool | no | **`false`** | **No implicit true-default.** A loop that omits `Enabled` decodes to `false` and is silently skipped by `run` — `Validate()` refuses a config where *no* loop ends up enabled, specifically so a typo'd/omitted `Enabled` cannot silently produce a no-op run. |
| `TokenOriginNetwork` | `*uint32` | required iff `Asset = "erc20"`; must be unset iff `Asset = "eth"` | `nil` | The `NetworkID` the ERC20 was deployed/minted on (via `deploy-token`); used to compute the wrapped-token address on the other networks. |
| `[[Loops.Hops]]` | `[]Hop` | **yes**, ≥ 2, contiguous, closed | — | See below. |

### `[[Loops.Hops]]`

| Field | Type | Required | Notes |
|---|---|---|---|
| `Source` | uint32 | **yes** | Must match a configured `Network.NetworkID`; must differ from `Destination`. |
| `Destination` | uint32 | **yes** | Must match a configured `Network.NetworkID`. |
| `Claim` | `"auto"` \| `"manual"` | **yes** | See [The claim-mode contract](#the-claim-mode-contract-auto-vs-manual). |

**Ring closure rule** (enforced by `Validate()`): `Hops` must have at least 2 entries; consecutive
hops must chain (`Hops[i].Destination == Hops[i+1].Source`); the last hop's `Destination` must equal
the first hop's `Source`.

**Non-fatal warning**: with more than two configured networks, `Validate()` (via `Warnings()`) flags
a config whose hops, taken together, do not cover all three bridge directions (L1→L2, L2→L1,
L2→L2) — worth checking, never a refusal.

Every field of `config.go`'s exported types is covered above (`Global`, `Network`, `Loop`, `Hop`,
and `WeiAmount`'s decoding rule). Two unexported details of `Config` — the `warnings` field (read
via `(*Config).Warnings()`) and `EnabledLoops()`/`NetworksByID()` (derived accessors, not config
fields) — are behavior, not schema, and are covered where they matter above and in
[Commands](#commands).

## Commands

Every command takes a repeatable `--cfg`/`-c CONFIG` flag (later files override earlier ones for
keys they both set).

### `run`

Drives every enabled loop until its `Iterations` cycles are done (or forever, if `0`), logging one
structured line per hop transition, exposing metrics, and flushing state on `SIGINT`/`SIGTERM`.

```
$ bridge-loop-tester run --cfg my-config.toml

Run loops=2 halted=0 cycles=2/2 hops=6 ok=6 failed=0 claim_mode_violations=0 stranded=0 duration=3m47s

Loop "eth-ring" (eth, 1000000000000000)
  route:   0->1 1->2 2->0
  cycles:  2 completed / 2 attempted
  hops:    6
  halted:  false 
  value:   at rest on the loop's origin network 0 (L1); the ring is closed

Loop "erc20-ring" (erc20, 500000000000000000)
  route:   1->2 2->0 0->1
  cycles:  2 completed / 2 attempted
  hops:    6
  halted:  false 
  value:   at rest on the loop's origin network 1 (L2A); the ring is closed
```

Flags: `--dry-run` (overrides `Global.DryRun`), `--iterations N` (overrides `Global.Iterations`),
`--resume-halted` (re-drives loops a previous run halted for a non-retryable reason — see
[Failure policy](#failure-policy)).

### `validate`

Strict preflight, **zero transactions sent**: static config checks plus every live, read-only check
the tool can make — RPC reachability, chain IDs, proxy health, bridge address agreement between the
config and what the proxy publishes, signer balances against `MinNativeReserve`, ring closure, gas
token liveness per network, and network-0 (L1) bridge-service availability. **Because it dials every
configured RPC and the proxy, it needs reachable endpoints — it is not offline like `status`.**

```
$ bridge-loop-tester validate --cfg my-config.toml

Proxy http://127.0.0.1:15601: healthy=true l1_bridge_service=true (network 0 in use: true)

Network 0 (L1)
  chain id:      271828
  signer:        0x1234...
  native:        4999998000000000000 wei (min reserve 1000000000000000000)
  gas token:     0x0000000000000000000000000000000000000000 (ether: true)
  WETH:          none
  bridge:        0x0000000000000000000000000000000000000B01 (bridge networkID() = 0)
  proxy bridge:  0x0000000000000000000000000000000000000B01 (reachable: true)

Network 1 (L2A)
  ...

Loop "eth-ring": asset=eth amount=1000000000000000 enabled=true route=0->1 1->2 2->0 claims=auto,manual,auto

Loop "erc20-ring": asset=erc20 amount=500000000000000000 enabled=true route=1->2 2->0 0->1 claims=manual,auto,manual

OK: 3 network(s), 2 enabled loop(s); no transaction was sent.
```

A refusal prints `REFUSED: <reason>` per problem found (aggregated, not stop-at-first) and the
command exits non-zero; a non-fatal observation prints `WARNING: <reason>` and does not block a run.
`--json` prints the full `PreflightReport` instead.

### `deploy-token`

Deploys (and optionally mints) the freely-mintable test ERC20 on one network, optionally recording
it as a loop's token in the state file so a later `run` reuses it instead of leaving a trail of
abandoned contracts behind.

```
$ bridge-loop-tester deploy-token --cfg my-config.toml --network 1 --loop erc20-ring \
  --mint 1000000000000000000000

Deployed 0xAbCd... (BridgeLoopTester L2A/BLT) on network 1 (L2A)
  owner:   0x1234...
  minted:  1000000000000000000000
  balance: 1000000000000000000000
  loop:    erc20-ring
```

Flags: `--network N` (required), `--name`, `--symbol` (default `"BridgeLoopTester <network name>"` /
`"BLT"`), `--mint AMOUNT` (wei-scale decimal string), `--loop LOOPNAME`.

### `claim`

One-off recovery claim for a deposit left unclaimed — a crashed run, a paused autoclaim service, or
an operator's own bridge transaction — identified by `(source, destination, deposit_count)`. Checks
`isClaimed` first, so it is always safe to run twice.

```
$ bridge-loop-tester claim --cfg my-config.toml --source 1 --destination 2 --deposit-count 57

Claimed deposit_count=57 (network 1 -> 2)
  global index:  4294967353
  leaf type:     0 (injected 904)
  amount:        1000000000000000
  destination:   0x1234...
  claim tx:      0xabcd... (block 129301, gas 142033)
  isClaimed:     true
```

Flags: `--source N`, `--destination N`, `--deposit-count N` (all required), `--gas-limit N`
(override the claim's gas estimate).

### `status`

Offline: reads the config and the state file only, touching **no RPC and no proxy** — the one
command guaranteed to work while the environment under test is down, which is exactly when it is
most needed. See [Operating it](#operating-it-diagnosing-a-healthy-vs-a-stranded-ring).

```
$ bridge-loop-tester status --cfg my-config.toml

State /var/lib/bridge-loop-tester/state.json, last written 2026-09-07T17:20:31Z

Loop "eth-ring" (eth, 1000000000000000, enabled=true)
  route:     0->1 1->2 2->0
  cycles:    41 completed / 42 attempted
  in flight: awaiting-claim (bridge tx 0x9f...)
  halted:    false 
  value:     STRANDED in flight: hop 1 (1->2) bridged in 0x9f... but was not claimed on network 2 (L2B)
```

`--json` prints the full `StatusReport` instead.

## Operating it: diagnosing a healthy vs. a stranded ring

Because every loop's route is circular, a healthy loop is **always at rest on its origin network**
between cycles. This single invariant is what makes an unattended, multi-day run diagnosable from
the outside:

- **Healthy**: `status`/`run`'s summary reports `value: at rest on the loop's origin network N (…);
  the ring is closed` and `Halted: false`.
- **Stranded**: anything else. The report's `ValueLocation` (`LoopReport.ValueLocation` in a
  `Report`, `LoopStatus.ValueLocation` in a `StatusReport`) names the network the value is sitting
  on, the hop index the ring stopped at, and one of two shapes:
  - **"STRANDED at rest on network N: hop *i* never bridged"** — the bridge transaction for that hop
    never happened (or never got a receipt); nothing has left the source network yet.
  - **"STRANDED in flight: hop *i* … bridged in `<tx>` but was not claimed on network N"** — the
    deposit exists on the bridge; it is waiting on a readiness gate or a claim.

A stranded loop that is not `halted` will **resume automatically** at the next cycle: the engine
picks the ring back up at the stranded hop rather than starting over at hop 0 (which would try to
spend value that is not there). A stranded loop that **is** `halted` will not — see the failure
policy below and `--resume-halted`.

`run`'s and `status`'s human output are one-line-per-loop by design so a quick glance across many
loops is enough; `--json` on either surfaces the same information machine-readably for scripting a
health check.

## Failure policy

Every hop failure is classified into exactly one `FailureClass` (`ClassifyHopFailure`, matching on
typed sentinels, never on message text), and each class has a single, fixed response:

| Class | What triggers it | Response |
|---|---|---|
| `transient` | A readiness gate's `*DeadlineExceededError`, an RPC/proxy error, a destination balance that did not reconcile, or anything unclassified. | **Attempted up to `Global.HopAttempts` times in total** (default `3`), `Global.LoopDelay` apart; each attempt after the first resumes from the checkpoint the previous attempt reached. If every attempt still fails, the cycle stops there and the loop's hop cursor **stays on that hop** — the next cycle resumes the ring at it. The loop itself is **never** halted for a transient. |
| `claim-mode-violation` | An `auto` hop nobody claimed, or a `manual` hop something else claimed (`*ClaimModeViolationError`). | **Fatal to that loop, never retried.** Logged at ERROR with the full evidence. Persisted as `halted`, so a plain restart does not paper over a real test failure — only `run --resume-halted` re-drives it, deliberately. |
| `ambiguous-resume` | A signed bridge transaction with no receipt, whose nonce a *different* mined transaction consumed (`*AmbiguousResumeError`). | **Fatal to that loop, halted loudly**, with the full decision evidence (bridge tx hash + nonce, account's mined/pending nonces, how long the receipt was waited for). Neither skipped (would abandon value mid-ring) nor re-driven (could double-bridge) — reconcile the bridge's indexed state by hand. |
| `insufficient-balance` | The signer cannot fund the hop without breaching `MinNativeReserve`, or holds too little of the asset (`*InsufficientBalanceError`). | **Fatal to that loop.** A soak run cannot top itself up; retrying for days would only repeat one log line. The report shows the value stranded, not in flight. |
| `configuration` | A malformed request or a network the engine does not know about. | **Fatal to that loop** — a bug or a bad config, not something a retry fixes. |
| `cancelled` | `SIGINT`/`SIGTERM`, or the caller's own context deadline. | **Not a failure.** The loop stops, state is flushed, `Report.Cancelled = true`, and `Report.Succeeded()` still reports true. |

`--resume-halted` clears every loop's `Halted` flag in the loaded state before the run starts, so a
loop halted for a non-retryable reason is deliberately re-driven. It is off by default: a halted
loop recorded a real test failure (or, for `ambiguous-resume`, an unresolved on-chain ambiguity), and
silently re-driving it on the next start would hide that instead of surfacing it — use it only after
you have looked at *why* the loop halted (`status`, or the run's log) and either fixed the underlying
problem or reconciled the ambiguous transaction by hand.

## The state file

When `Global.StatePath` is set, the tool persists one JSON document there after every checkpoint
change, so a crash or a deliberate restart resumes exactly where it left off rather than re-driving
already-bridged hops. When `Global.StatePath` is empty, state still exists in memory for the life of
the process (so a loop can resume within one run), it just is not durable across restarts.

**Format** (schema `version: 1`; a file from a newer version is refused rather than misread; a
missing or empty file means "nothing persisted yet"):

```json
{
  "version": 1,
  "updated_at": "2026-09-07T17:20:31.412Z",
  "loops": {
    "eth-ring": {
      "name": "eth-ring",
      "cycles_completed": 41,
      "cycles_attempted": 42,
      "hop_index": 1,
      "value_network": 1,
      "in_flight": {
        "state": "awaiting-claim",
        "bridge_tx_hash": "0x9f...",
        "bridge_tx_nonce": 128,
        "deposit_count": 57,
        "l1_info_tree_index": 903,
        "injected_leaf_index": 904,
        "destination_balance_before": "998877665544332211",
        "started_at": "2026-09-07T17:18:02.001Z"
      },
      "halted": false,
      "halt_class": "",
      "last_error": "",
      "updated_at": "2026-09-07T17:20:31.410Z"
    }
  },
  "tokens": {
    "erc20-ring": {
      "loop_name": "erc20-ring",
      "origin_network": 1,
      "address": "0xAbC...",
      "name": "BridgeLoopTester erc20-ring",
      "symbol": "BLT",
      "deployed_at": "2026-09-05T09:00:00Z",
      "minted": "50000000000000000000",
      "wrapped": {"0": "0x111...", "2": "0x222..."}
    }
  }
}
```

`hop_index` is the resume cursor: `0` means the loop is healthy and at rest on its origin; any other
value means the ring stopped part-way, and `value_network` names where the value is currently
believed to sit. `in_flight` is present only while a hop is mid-flight; its absence with a non-zero
`hop_index` means the bridge transaction for that hop never happened.

**Atomicity.** `Save` never writes into the live file: it marshals the whole document, writes it to
a temp file **in the same directory** (so the final rename cannot cross a filesystem boundary),
`fsync`s and closes it, `chmod 0600`s it, `os.Rename`s it over the target (atomic on POSIX), then
`fsync`s the parent directory so the rename itself is durable. This is not a nicety: the hop engine
treats "checkpoint state `bridging` with no `bridge_tx_hash`" as proof a bridge transaction was
never signed, and therefore safe to (re-)submit. A torn write that happened to leave exactly that
byte pattern for a hop whose transaction *had* already been signed and broadcast would be read as
permission to bridge the same value a second time — hence the whole-document, same-directory,
rename-then-fsync-the-directory sequence, rather than an in-place write.

The file itself is `0600`, its parent directory `0700` — it can carry deposit-identifying data and
bridge transaction hashes tied to the run's own funded keys, so it is created owner-only.

## Metrics

When `Global.MetricsAddr` is set, the tool serves Prometheus metrics on `http://<addr>/metrics`
(via `aggkit/prometheus`, so it shares that package's endpoint constant and default registry):

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `bridge_loop_tester_hops_total` | counter | `loop, route, claim_mode, outcome` | Hop attempts by outcome (`success`/`failed`). |
| `bridge_loop_tester_hop_duration_seconds` | histogram | `loop, route, claim_mode` | End-to-end duration of a hop attempt (bucketed 1s–3600s; the tail matters most — a 20-minute hop is the interesting one). |
| `bridge_loop_tester_cycles_total` | counter | `loop, outcome` | Ring passes by outcome (`completed`/`failed`). |
| `bridge_loop_tester_oldest_inflight_hop_age_seconds` | gauge | — | Age of the longest-running in-flight hop across all loops, `0` when idle. Refreshed every 5s so it reflects wall-clock time even while nothing else changes — this is the number to alert on for "a hop is stuck". |
| `bridge_loop_tester_inflight_hops` | gauge | — | Number of hops currently in flight across all loops. |
| `bridge_loop_tester_loops_halted` | gauge | — | Number of loops permanently halted by a non-retryable failure. Alert on `> 0`. |
| `bridge_loop_tester_stranded_loops` | gauge | — | Number of loops whose value is not resting on their origin network. A brief non-zero blip during a hop is normal; sustained non-zero for longer than a cycle's usual duration is the actionable signal. |
| `bridge_loop_tester_claim_mode_violations_total` | counter | `loop, route, claim_mode` | Claim-mode assertion failures — the tool's core diagnostic signal. Alert on any increase. |
| `bridge_loop_tester_gate_stalls_total` | counter | `loop, route, gate` | Hops that stalled on a readiness gate, by gate name (`l1-info-tree-index`, `injected-l1-info-leaf`, `claim-proof`, `claimed`) — tells you *which* stage is slow or stuck without reading logs. |
| `bridge_loop_tester_hop_retries_total` | counter | `loop, route` | Retries after a transient failure. A rising rate on one route without a corresponding rise in `..._hops_total{outcome="failed"}` means transients are being absorbed; if failures rise too, the retries are not fixing the underlying problem. |

Counters are cumulative across repeated in-process `Run` calls within one process, because metrics
are registered into the process-wide default registry (re-registering an already-registered name is
a no-op there) — this is the right behavior for a scrape target, and the reason a `Report`, not the
metrics, is what an automated test should assert on.

## Gas drain, funding and `MinNativeReserve`

A circular route returns the *bridged asset* to its origin every cycle — but gas is a separate,
one-way cost on every network a hop's transactions are submitted on (the bridge transaction always;
the claim transaction too, for a `manual` hop or a recovery `claim`). On a network where the bridged
asset **is** the gas asset (an `"eth"` loop on a network whose gas token is ether — the only case
this tool supports for ETH loops), every cycle very slightly shrinks the signer's spendable native
balance even though the *loop's own* accounting nets to zero: the loop moves `Amount` of ETH around
in a circle, but each hop it touches also burns some of that same account's ETH on gas, and that gas
never comes back.

Concretely: a hop's fundability check requires the source account to hold at least `Amount +
MinNativeReserve` before it will bridge ETH (see `operations.go`'s `planHop`/`InsufficientBalanceError`
in `hop.go`), and a native hop's destination-balance verification tolerates a small negative
`NativeGasSlack` (0.001 ETH by default, `DefaultNativeGasSlack` in `hop.go`) below the exact expected
delta — because the *same* destination account may pay gas for its own next hop, or for another
loop sharing that account, between the deposit landing and the balance being read. Over a long
enough run, that steady gas cost is what `MinNativeReserve` exists to protect against: set it high
enough, per network, that a long soak run hits `InsufficientBalanceError` (a clean, diagnosable
fatal stop) long before it would otherwise run the account low enough to fail to pay for gas at all.
An ERC20 loop is not exempt either — bridging *anything* still costs native-asset gas on the network
the transaction is submitted on, so `MinNativeReserve` matters for ERC20-only loops too, just without
the ETH loop's added complication of the reserve and the bridged asset being the same balance.

**Practical guidance for a multi-day run**: fund each network's signer well beyond
`Amount × (expected hops per day) × (days planned)` in native currency, and set `MinNativeReserve`
generously above your node's real minimum gas requirement — the cost of setting it too high is an
earlier, cleaner stop; the cost of setting it too low is a signer that cannot pay for gas at all,
which fails in less diagnosable ways than a deliberate `InsufficientBalanceError`.

## `DryRun` semantics

`DryRun` (`Global.DryRun`, or `run --dry-run`) runs **every read and every fundability assertion**
the tool would make, and submits **no transaction at all** — not even in an "everything looks fine"
path. Concretely, per hop it resolves the source account, reads its native and (for ERC20) token
balance, resolves the source/destination token addresses (for an ERC20 loop whose token has already
been deployed), and computes whether the hop is fundable, recording all of that as a `HopPlan`
(`LoopReport.Plan`) — **not** a simulated `HopResult`. There is deliberately no synthetic outcome for
the readiness gates or the claim-mode assertion: those need a real deposit on the bridge to observe
anything honest, so a dry run stops short of them rather than fabricating a result. What it *does*
prove is everything that can be wrong before the first transaction — a network whose gas token is
not ether, a signer that cannot fund a hop, an ERC20 loop with no token deployed yet, a proxy that
cannot route a network — the set of mistakes that would otherwise only surface minutes into a real
run.

```
$ bridge-loop-tester run --cfg my-config.toml --dry-run
...
Loop "eth-ring" (eth, 1000000000000000)
  ...
  DRY RUN hop 0 0->1 claim=auto amount=1000000000000000 fundable=true
  DRY RUN hop 1 1->2 claim=manual amount=1000000000000000 fundable=true
  DRY RUN hop 2 2->0 claim=auto amount=1000000000000000 fundable=true
```

## Known limitations

- **Native (ETH) bridging is only supported where the destination's/source's gas token is ether.**
  A network whose bridge reports a non-zero `gasTokenAddress()` needs the WETH path for its native
  asset, which this tool does not implement; `validate`/`run` refuse an `"eth"` loop that touches
  such a network rather than failing confusingly mid-run (`ErrNativeAssetUnsupported`).
- **The deferred dry-dock/bali work is out of scope here.** This tool has no bali-specific
  configuration, and none is documented — see the plan's "out of scope" section. The example configs
  below are deliberately generic, placeholder-only.
- Nothing in this README should be read as a claim of behavior verified against a live chain or
  proxy — see the validation-status note at the top.
