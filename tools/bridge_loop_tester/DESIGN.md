# bridge_loop_tester wire contract and hop state machine

Design-only document (no code in this step). Confirms, by reading the actual handlers/clients
(not just their doc comments), the exact wire contract `tools/bridge_loop_tester` depends on, and
specifies the hop state machine later steps (S6 in particular) implement against.

Target validation env: `test/e2e/envs/anvil-2chains` — L1 network_id=0 (chain 271828,
`http://127.0.0.1:13545`), L2A network_id=1 (chain 20201, `:14545`), L2B network_id=2 (chain 20202,
`:15545`), aggkit-proxy REST at `http://127.0.0.1:15601` (proxy+tracker components).

## 1. Proxy routing and `network_id`

The aggkit-proxy binary (`proxy/cmd`) runs two independently-registered components on one shared
HTTP server (`proxy/restserver.go`):

- **`proxy`** (`proxy/service.go`): forwards `ANY /bridge/v1/*any` to the per-network bridge
  service resolved by `bridgeservicefinder.Finder.GetURL(network_id)`. `ForwardHandler` reads
  `network_id` from the **query string** and answers `400 {"error":"missing mandatory query
  parameter: network_id"}` if absent, or `404` if the finder has no URL cached for that network.
  **Every `/bridge/v1/*` call the tool makes must carry `?network_id=<id>` in the query string,
  with no exception.**
- **`tracker`** (`bridgetracker/api/api.go`): registers `/tracker/v1/*` directly (not proxied by
  `network_id`-keyed forwarding). Its network selector is a **URL path segment**
  (`/tracker/v1/network/:network_id/tx/:tx_hash`), mandatory by routing (no route matches without
  it), parsed by `parseBridgeRequest`. The tracker is one process serving every network in this
  env; it resolves per-network JSON-RPC/bridge sources itself via the same finder.

  So (a) holds for both prefixes, just with `network_id` carried differently: query param on
  `/bridge/v1/*`, path segment on `/tracker/v1/*`.

- **`GetClaimCandidates` (`bridgeservice/client.Client.GetClaimCandidates`, `/bridge/v1/claim-candidates`)
  does not set `network_id` in its query** (only `destination_network_ids`, `to_ler`, `from_ler`,
  paging — confirmed by reading `client.go`: no `NetworkID` field on `GetClaimCandidatesParams`).
  Called through the proxy this always 400s. **The tool does not need this endpoint**: it always
  knows its own hop's origin network and deposit count directly from the `BridgeEvent` log (see
  §5), so it never needs to *discover* candidate bridges — only to fetch data for one it already
  identified. Confirmed unusable through the proxy, confirmed unneeded.

- **`network_id=0` (L1) resolves through an L2's bridge service.** `bridgeservicefinder` has *no
  independent* concept of an L1 bridge service — `network 0` is not a rollup and is never
  enumerated on-chain; `GetURL(0)` only succeeds if `Config.BridgeURLs[0]` is set (see
  `bridgeservicefinder/doc.go`). In this env's `config/aggkit-proxy/aggkit-proxy.toml`:
  `[BridgeServiceFinder.BridgeURLs] 0 = "http://aggkit-001:5577"` — i.e. **L1 requests are served
  by aggkit-001's (L2A's) bridge-service instance**, which additionally runs an `L1InfoTreeSync`/
  `BridgeL1Sync` component and switches on `networkID == mainnetNetworkID(0)` inside its own
  handlers (`bridgeservice/bridge.go`, e.g. `L1InfoTreeIndexForBridgeHandler`,
  `ClaimProofHandler`). This is transparent to a proxy caller — just note it so a "why does L1
  answer through an L2's compose service" surprise during debugging isn't mysterious.

## 2. Readiness / claim sequence (per hop)

All three calls below are `GET`, all go through the proxy, all use `doRequestAllowNotFound`
semantics in `bridgeservice/client.Client`: **HTTP 404 → `client.ErrNotFound`, meaning "not
indexed yet, retry later"**; any other non-200 is a hard error string (see Gap G1).

| # | Call | Query params | Success (200) | 404 means | Notes |
|---|---|---|---|---|---|
| 1 | `GET /bridge/v1/l1-info-tree-index` | `network_id=<origin>`, `deposit_count=<N>` | `uint32` L1 info tree index `I` | origin's own bridge/L1-info syncer hasn't indexed deposit `N` yet | `origin` = the network the bridge tx was sent on (source network), not the token's origin network |
| 2 | `GET /bridge/v1/injected-l1-info-leaf` | `network_id=<dst>`, `leaf_index=<I>` | `types.L1InfoTreeLeafResponse` (may have a **different, larger** `l1_info_tree_index` than `I` — see below) | no GER covering leaf `I` has been injected on `dst` yet | **skip this call entirely when `dst == 0`** (L1 destination needs no injection: settlement onto L1 already is the L1 GER update) |
| 3 | `GET /bridge/v1/claim-proof` | `network_id=<origin>`, `leaf_index=<I'>`, `deposit_count=<N>` | `types.ClaimProof` (local+rollup exit proofs + `L1InfoTreeLeaf`) | origin's bridgesync/l1infotreesync hasn't caught up to `I'`/`N` yet | `I'` must be the **actual** index returned by call 2 (`resp.L1InfoTreeIndex`), not the `I` requested — the aggoracle may have injected a later-covering leaf first (races with a concurrent bridge elsewhere); using the actual index keeps `mainnetExitRoot`/`rollupExitRoot` consistent with what's really injected on `dst`. When `dst==0` (step 2 skipped), `I' = I` from step 1. |

Confirmed against the handlers directly (`bridgeservice/bridge.go`):
`L1InfoTreeIndexForBridgeHandler`, `InjectedL1InfoLeafHandler`, `ClaimProofHandler` all validate
`network_id` against exactly two values — `mainnetNetworkID` (0) or `b.networkID` (this instance's
own L2) — anything else is `400`. This is invisible to the tool since it always calls through the
proxy with the right instance already resolved for it.

Also confirmed against a proven, working implementation of exactly this sequence:
`test/e2e/bridge_utils.go`'s `BridgeL1ToL2`/`BridgeL2ToL1(NoClaim)`/`BridgeL2ToL2(NoClaim)` all
poll call 1 then (L2/L2 dest only) call 2 then call 3, in this order, with a sleep-based retry
loop on `ErrNotFound`/zero-index. **Deliberate difference from that helper**: it talks to
`bridgeservice/client.Client` pointed straight at one aggkit instance's own bridge-service port
(bypassing the proxy and its per-network routing); `bridge_loop_tester` must always go through the
proxy's single `network_id`-routed base URL instead, and must retry on `client.ErrNotFound`
(404) rather than assume same-process wiring.

## 3. `GET /tracker/v1/network/{network_id}/tx/{tx_hash}`

`network_id` and `tx_hash` are **path** segments (`network_id` = the network the bridge tx was
*sent* on — the origin, not the destination). Always **200 OK** (registers the tx for tracking on
first call — see Gap G2) with body `TrackingData` (`bridgetracker/api/tracking_data.go`):

| Field | Type | Useful for |
|---|---|---|
| `tracking_status` | string: `"Registered"` / `"Running"` / `"Error"` | coarse liveness; `"Error"` means the tracker gave up resolving the tx entirely (bad hash / not a bridge tx / permanent failure) |
| `claim_status` | string: `"pending"` / `"readyToClaim"` / `"claimed"` / `"error"` | **primary diagnostic signal** — `"readyToClaim"` == tracker's own `StepWaitingClaim`; `"claimed"` == `StepClaimed`. This is an independent cross-check against the tool's own state machine (§4), useful for detecting an autoclaim-policy bug (auto-claim hop stuck at `readyToClaim` past its grace period) or the tool's own bug (manual hop already `claimed` by something else) |
| `bridge_status` | `*BridgeStatus`, null until resolved | `bridge_status.event.deposit_count` cross-checks the deposit count taken from the tx receipt log (§5); `bridge_status.bridge_type` cross-checks the configured hop direction |
| `step_index` / `all_steps` | full step path | diagnosis only: on a stuck hop, `all_steps[step_index]` names the exact stuck step (`WaitingGERUpdate`, `WaitingLERUpdate`, `CertificatePending`, `WaitL1SettledGER`, `WaitingGERInjection`, `WaitingL1InfoLeafAvailable`, `WaitingClaim`, `Claimed`) and, if `status=="error"`, `error.error_type` (`transient`/`permanent`/`exhausted`) |
| `error` | `*ErrorStep`, nil unless something failed | terminal tx-level failure (bad hash, not a bridge tx) |

Recommendation: the tool should call this once right after submitting the bridge tx (to register
it for tracking — cheap, side-effect-free to call again) and then poll it periodically alongside
its own proxy-driven state machine purely for **diagnostics/logging** when a hop stalls past an
expected duration — never as the source of truth for hop-state transitions (§4 only transitions
on `/bridge/v1/*` responses and on-chain reads, per the plan's constraint that observations go
through `/bridge/v1` + `/tracker/v1` + JSON-RPC, and the tracker's own step list already documents
that step timing/exact semantics can shift between aggkit versions — the bridge/v1 sequence in §2
is the stable contract to drive the state machine on).

## 4. Hop state machine

One instance per configured hop (origin network, destination network, token, amount, claim mode).
Every state must be re-derivable after a process restart purely from: (a) the bridge tx hash +
origin network (to re-read the `BridgeEvent` log, §5), (b) `/bridge/v1/*` responses, (c) an
on-chain `isClaimed` read on the destination — nothing is trusted to survive only in the tool's own
memory.

```
S0 SubmittingBridge
      -> call bridgeAsset/bridgeMessage/bridgeMessageWETH on origin (JSON-RPC), get tx hash
      -> on receipt mined: parse BridgeEvent from receipt logs (§5) -> depositCount, leafType
      -> register tx with tracker (fire-and-forget, diagnostics only)
      => S1

S1 WaitingOriginIndex
      -> poll GET /bridge/v1/l1-info-tree-index?network_id=<origin>&deposit_count=<depositCount>
      -> 404 (ErrNotFound): stay in S1
      -> 200 -> I
      => S2 (dst == 0)         [skip S2's call, go straight to S3 with I'=I]
      => S2 (dst != 0)

S2 WaitingGERInjection            [skipped when dst == 0]
      -> poll GET /bridge/v1/injected-l1-info-leaf?network_id=<dst>&leaf_index=<I>
      -> 404 (ErrNotFound): stay in S2
      -> 200 -> resp; I' = resp.l1_info_tree_index (may exceed I, see §2 note)
      => S3

S3 FetchingClaimProof
      -> poll GET /bridge/v1/claim-proof?network_id=<origin>&leaf_index=<I'>&deposit_count=<depositCount>
      -> 404 (ErrNotFound): stay in S3
      -> 200 -> ClaimProof (proof_local_exit_root, proof_rollup_exit_root, l1_info_tree_leaf)
      => S4

S4 AwaitingClaimDecision
      -> read destination bridge contract IsClaimed(depositCount, originNetwork) via JSON-RPC
         (agglayerbridgel2 binding; originNetwork = the hop's origin, i.e. where the BridgeEvent
         fired -- NOT the token's origin network encoded in bridge.OriginAddress/OriginNetwork)
      -> already claimed => S6 (claimed by something else -- fine, still idempotent success)
      -> not claimed, Claim == "auto":
           start/continue a grace-period timer; poll IsClaimed each tick
           -> claimed before timeout => S6
           -> timeout elapsed, still unclaimed => S5err (policy violation: autoclaim did not claim in time)
      -> not claimed, Claim == "manual":
           wait a short grace period (assert nothing else claims it -- if it DOES get
           claimed during this window, that's also a hard failure: a "manual" hop must not
           observe an autoclaim policy claiming it)
           -> claimed during grace window => S5err (policy violation: unexpected auto-claim)
           -> still unclaimed after grace window => S5submit

S5submit SubmittingClaim           [manual hops only]
      -> pack calldata: autoclaim/claimtx.PackClaim (or call agglayerbridgel2 binding's
         ClaimAsset/ClaimMessage directly with the fields below) and submit via JSON-RPC
      -> tx mined, status success => S6
      -> tx reverts with AlreadyClaimed selector (0x646cf558, see
         test/e2e/bridge_utils.go's alreadyClaimedErrorSelector) => re-check IsClaimed;
         true => S6 (race lost to something else, still success); false => hard error
      -> tx reverts otherwise / not mined => S5err

S5err TerminalHopError
      -> logged/reported; does not advance the cycle. Whether the loop aborts the whole
         cycle or just this hop is a later-step (S6 engine) policy decision, not this doc's.

S6 Claimed
      -> hop complete. If this is the last hop of the circular route, the value is back at
         its origin address on the origin network -- cycle done, loop back to S0 for the next
         cycle (same route, same amount, gas already accounted for).
```

Resumability: every state transition above is derived purely from (bridge tx hash, origin
network) plus proxy/RPC reads that are themselves idempotent and side-effect-free except S5submit
(the one on-chain write beyond the original bridge tx). A restart mid-hop re-derives
`depositCount`/`leafType` from the known tx hash (re-fetch receipt, re-parse `BridgeEvent`), then
re-enters at whichever of S1/S2/S3/S4 its polls land on; S4's `IsClaimed` read means a restart
mid-grace-period or even mid-S5submit (tx sent but process died before observing the receipt) is
safe: re-check `IsClaimed` first, do not blindly resubmit.

The one window that is *not* covered by "the known tx hash" is the bridge submission itself -- a
crash between signing and learning the hash would leave nothing to re-derive from. A signed
transaction's hash is fixed by its signature, so the network layer hands it (and the nonce it
consumes) to the caller before broadcasting (`TxRequest.OnSigned`), and the hop engine checkpoints
both there. A restart in that window then decides from three reads: a receipt for the hash means
the deposit is real; no receipt with the nonce neither mined nor queued means the node never saw it
and it is safe to re-submit; no receipt with the nonce already consumed means a deposit may exist
under a hash the tool never learned, which is the only case still refused (`AmbiguousResumeError`)
rather than guessed.

## 5. `BridgeEvent` -> `depositCount` (no polling needed to locate the bridge)

The bridge tx's own receipt contains a `BridgeEvent` log emitted by the bridge contract itself.
Confirmed pattern from `test/e2e/bridge_utils.go` (`BridgeL2ToL1NoClaim`, ERC20 case — note the
comment "ERC20 bridging also emits a Transfer log, so scan all logs for the BridgeEvent"):

```go
for _, lg := range receipt.Logs {
    bridgeEvent, err := bridgeContract.ParseBridgeEvent(*lg)  // agglayerbridgel2 binding
    if err == nil {
        depositCount = bridgeEvent.DepositCount
        break
    }
}
```

This avoids the `GetBridges`/`GetBridgeByDepositCount` polling loop the *L1->L2* helper uses (it
predates this pattern and instead lists+scans `/bridge/v1/bridges` until the tx hash shows up).
**`bridge_loop_tester` should always parse `BridgeEvent` from the receipt directly** (works
identically for L1, L2A, L2B origins — same binding, same event) rather than poll any `/bridge/v1`
list endpoint to *locate* the deposit. It still needs `/bridge/v1/l1-info-tree-index` etc.
afterward — those aren't locating the bridge, they're waiting on cross-network indexing that has
no on-chain-only substitute.

## 6. Native-asset (ETH) semantics per network

Bridge contract getters (agglayerbridgel2 binding, confirmed present):
`GasTokenAddress() (common.Address, error)`, `GasTokenNetwork() (uint32, error)`,
`WETHToken() (common.Address, error)`.

Rule:

- **`gasTokenAddress() == 0x0` (this network's native/gas currency IS ether):** bridge ETH with
  `bridgeAsset(destNetwork, destAddr, amount, token=common.Address{}, forceUpdateGlobalExitRoot,
  permitData=nil)` and `msg.value = amount`. This is the only path the tool needs to implement for
  the anvil-2chains validation env for ETH hops — see below.
- **`gasTokenAddress() != 0x0` (this network's gas token is some other ERC20, i.e. an
  L2 whose gas currency isn't ether):** there is no raw `msg.value` ETH here. Represent bridged-in
  ETH via the network's own `WETHToken()` ERC20 and either (a) `bridgeAsset(token=WETHToken(),
  amount, ...)` after `Approve`, ordinary ERC20 semantics, or (b) `bridgeMessageWETH(destNetwork,
  destAddr, amountWETH, forceUpdateGlobalExitRoot, metadata)` — a value-bearing message that burns
  local WETH and unwraps to real ETH on claim at the destination, no `Approve` needed. **The tool
  should refuse up front (fail `CmdValidate`) any hop configured as "ETH" whose origin network has
  `gasTokenAddress() != 0x0`**, i.e. do not implement the WETH/`bridgeMessageWETH` path in the
  first version — flag it as an explicit non-goal/TODO rather than silently mishandling it.

**Confirmed for anvil-2chains: `gasTokenAddress()` is the zero address on both L2A and L2B** (i.e.
ether is the native/gas currency on both, same as L1). This is not a live on-chain probe (no env
was running during this research step) but strong static/behavioral evidence:
`test/e2e/bridge_utils.go`'s `BridgeL1ToL2` bridges with `token=common.Address{}` + `msg.value` from
L1 and then asserts the increase lands in `env.Clients.L2.BalanceAt` (L2's **native** balance, not
an ERC20 balance) — this only works if the L2's gas token is ether. `BridgeL2ToL1`/`BridgeL2ToL2`
do the same in the other direction (`if token == zeroAddr { l2Opts.Value = bridgeAmount }`), and
both are part of this repo's own passing e2e suite against this exact env. **Recommendation for
later steps:** `CmdValidate` (or an early step of `CmdRun`) should still call `gasTokenAddress()`
live at startup for every configured network rather than hardcode this assumption — cheap, and
turns a wrong assumption into a clear refusal instead of a confusing mid-run failure.

For the **ERC20** leg of the circular route: deploy via `test/contracts/mintableerc20`'s
`DeployMintableerc20` on one network (say L2A, matching the e2e suite's own pattern of deploying
the test ERC20 on an L2 rather than L1), then bridge it around the loop with `token =
<deployed address>` — ordinary `bridgeAsset` + `Approve`/`Mint` as needed, no WETH complications
since it's a real ERC20 the whole way (wrapped representations on the other two networks resolved
via `ComputeTokenProxyAddress`, confirmed on the `agglayerbridgel2` binding and used exactly this
way in `test/e2e/bridge_utils.go`'s `BridgeL2ToL2`).

## 7. `globalIndex`

Use `bridgesync.GenerateGlobalIndexForNetworkID(originNetworkID uint32, depositCount uint32)
*big.Int` (`bridgesync/processor.go`) directly — this is exactly what
`autoclaim/types.DeriveGlobalIndexForSource` (used by `autoclaim/claimtx.PackClaim`) calls under
the hood. `originNetworkID` here is the **hop's origin network** (where the `BridgeEvent` fired —
same value used in `/bridge/v1/l1-info-tree-index`'s `network_id`), not the token's origin network.
Do not hand-roll the mainnet-flag/rollup-index encoding — this helper is the single source of
truth and is already reused by `autoclaim`.

(`BridgeResponse.GlobalIndex` from `/bridge/v1/bridges` etc. also already carries this
precomputed — fine to cross-check against if the tool ever lists bridges for debugging, but §5
means the tool has `depositCount` before any such call, so it should compute `globalIndex` itself
rather than wait on a list endpoint.)

## 8. Idempotent "already claimed" detection

Call `IsClaimed(opts *bind.CallOpts, leafIndex uint32, sourceBridgeNetwork uint32) (bool, error)`
on the **destination** network's bridge contract directly via JSON-RPC (agglayerbridgel2 binding).
`leafIndex` = `depositCount` from §5, `sourceBridgeNetwork` = the hop's origin network (exactly
`test/e2e/bridge_utils.go`'s own `IsClaimed(callOpts, depositCount, bridge.OriginNetwork)` call,
modulo that helper naming it `bridge.OriginNetwork` off a `/bridge/v1/bridges` response — the tool
gets the same value from the `BridgeEvent` log's origin network directly).

This is preferred over polling `/bridge/v1/claims?global_index=...` (also possible, per
`bridgeservice/client.GetClaimsParams.GlobalIndex`) because it's a direct view call against the
authoritative contract state — no dependency on claim-sync catching up — and it's exactly the
check the tracker itself and `autoclaim`'s own sender use before submitting
(`docs/autoclaim.md`'s sequence diagrams: "SND->>L2: Already claimed (isClaimed)?" both before and
after send). Call it: before the grace-period wait (S4 entry), on every grace-period tick, and
again immediately after a submitted claim tx reverts with the `AlreadyClaimed` selector
(`0x646cf558`) to distinguish "someone else won the race" (success) from a genuine revert
(failure) — see S5submit in §4.

## 9. `test/contracts/mintableerc20` reuse

**Directly importable and usable from `tools/`, no change needed.** Confirmed:

- Single Go module (`github.com/agglayer/aggkit`, one `go.mod` at repo root) — `test/` and
  `tools/` are ordinary sibling packages under the same module, there is no `internal/` boundary
  anywhere in the repo blocking cross-import.
- Existing precedent: `tools/force_ger_update/integration_test.go` already imports
  `github.com/agglayer/aggkit/test/helpers` from under `tools/`.
- `test/contracts/mintableerc20/mintableerc20.go` exports exactly what's needed:
  `DeployMintableerc20(auth *bind.TransactOpts, backend bind.ContractBackend, name, symbol string)
  (common.Address, *types.Transaction, *Mintableerc20, error)`, and on the returned `*Mintableerc20`:
  `Mint(opts *bind.TransactOpts, to common.Address, amount *big.Int)`,
  `Approve(opts *bind.TransactOpts, spender common.Address, amount *big.Int)`, plus `BalanceOf`,
  `NewMintableerc20(address, backend)` to bind an already-deployed instance (needed for the
  wrapped-token side on the other two networks, via `ComputeTokenProxyAddress` +
  `NewMintableerc20`, exactly as `test/e2e/bridge_utils.go`'s `BridgeL2ToL2` does).

No re-generation or move is warranted; `tools/bridge_loop_tester`'s `CmdDeployToken` should import
`github.com/agglayer/aggkit/test/contracts/mintableerc20` directly.

## Gaps found

**G1 — `bridgeservice/client.Client` conflates 503 into the same generic error as 400/500.**
`doRequestAllowNotFound` only special-cases HTTP 404 (`ErrNotFound`); a 503
(`httpStatusForSyncerError`'s "syncer is resolving a reorg, retry later" case, per
`bridgeservice/bridge.go`'s `respondSyncerError`) comes back as an opaque
`fmt.Errorf("unexpected status code %d: %s", ...)` indistinguishable from a genuine 400/500. The
hop state machine in §4 currently only retries on `ErrNotFound`; a transient reorg-recovery 503
would incorrectly read as a hard/terminal error. **Recommended resolution:** add an
`ErrServiceUnavailable` sentinel to `bridgeservice/client` (mirroring `ErrNotFound`) mapped from
HTTP 503, and have every polling call-site in the state machine retry on either sentinel. **This
needs a shared-package change** to `bridgeservice/client/client.go` (not `bridgeservice` itself) —
must be its own commit, separate from this tool, ideally landed before or alongside the S6 hop
engine step so the engine can rely on it from day one instead of string-matching status codes
client-side as a workaround.

**G2 — `GET /tracker/v1/network/{id}/tx/{hash}` is a side-effecting GET (registers the tx).**
Not a bug, just worth flagging so a later step doesn't call it more often than intended or treat
it as free: every call that isn't already a lookup of a registered tx also occupies a slot in the
tracker's bounded `MaxTrackedBridges` registry (`bridgetracker/config.go`) — with hundreds of hops
over a multi-day soak run and `RetentionPeriod`/`IdleTimeout` both `30m` in this env's config, the
tool should call it once per hop right after the bridge tx is mined (to register) and rely on its
own §4 state machine for the actual polling loop, not re-register-poll the tracker in a tight loop.
No shared-package change needed — this is a usage-pattern note for the S6 engine.

**G3 — No live on-chain confirmation that `gasTokenAddress()==0x0` on L2A/L2B in this env.**
This step had no running `anvil-2chains` instance to query. §6's conclusion rests on strong
static/behavioral evidence (the e2e suite's own passing assertions against native L2 balances),
not a direct read. **Recommended resolution:** no shared-package change; the implementing step
should call `gasTokenAddress()` live during `CmdValidate` (already recommended in §6) which
serves as the actual confirmation the first time the tool runs against a live env — treat §6's
conclusion as "expected, to be asserted at runtime," not "guaranteed."

**G4 — `bridgeservicefinder`'s `network_id=0` routing depends on env-specific config, not a
protocol guarantee.** `GetURL(0)` only works because this env's `aggkit-proxy.toml` happens to set
`BridgeURLs[0]`. This is fine for `anvil-2chains` (confirmed in §1) but means the tool must not
assume L1 requests always succeed through *any* aggkit-proxy deployment — a differently-configured
proxy without `BridgeURLs[0]` set would 404 every `network_id=0` call. Not a gap in this tool's
design (nothing to change), just a documented environmental assumption: `bridge_loop_tester`
targets proxies that expose network 0, and should fail fast and clearly (not retry forever as if
it were a 404-retry-later case) if `network_id=0` calls consistently 404 at every retry with no
progress — worth a distinct error path in the S6 engine, not lumped in with `ErrNotFound`'s
retry-later handling for every other network.
