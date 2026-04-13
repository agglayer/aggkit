# Using a Full Node instead of an Archive Node

## Description

Historically, aggkit required **archive nodes** for both L1 and L2 RPC endpoints because certain
startup and sync operations needed to query historical blockchain state. This document explains
the changes that remove those requirements.

**Reference:** [Ethereum Archive Nodes](https://ethereum.org/developers/docs/nodes-and-clients/archive-nodes)

---

## Background: Why archive nodes were required

An archive node retains the full historical state of the blockchain (e.g. contract storage at every
block). A full node only retains a recent window of state (typically the last ~128 blocks on
Ethereum). Archive nodes are significantly more expensive to run and harder to access.

aggkit had three hard dependencies on archive-node capabilities:

1. **Initial Local Exit Root (LER) query** — on startup, aggkit calls the `RollupManager` contract
   at the rollup creation block to fetch the initial LER. Since the rollup creation block is far in
   the past, this call fails on a full node.

2. **`debug_traceTransaction` for bridge `FromAddress`** — the bridge syncer uses
   `debug_traceTransaction` to extract the `FromAddress` field for bridge asset events. This call
   is typically only available on archive nodes. It is only triggered when the `bridge` component
   is active (controlled automatically by `SyncFromInBridges = "auto"`).

3. **`debug_traceTransaction` for claim calldata** — the `ClaimEvent` emitted by the bridge
   contract only contains basic fields (`globalIndex`, `originNetwork`, `originAddress`,
   `destinationAddress`, `amount`). To obtain the full claim data needed by the aggsender
   (Merkle proofs, `destinationNetwork`, `GlobalExitRoot`, `MainnetExitRoot`, `RollupExitRoot`,
   `metadata`…), aggkit had to call `debug_traceTransaction` on the claim transaction and decode
   the calldata of `claimAsset` / `claimMessage`. This call requires an archive node.
   The new [`AgglayerBridgeL2`](https://github.com/agglayer/agglayer-contracts/blob/v12.2.3/contracts/sovereignChains/AgglayerBridgeL2.sol) contract (sovereign chains, `cdk-contracts-tooling`) emits a richer
   `DetailedClaimEvent` that includes all of those fields directly in the log, making the
   `debug_traceTransaction` call unnecessary. When aggkit detects that the L2 bridge is a
   sovereign deployment (`AgglayerBridgeL2`), it handles `DetailedClaimEvent` logs directly and
   skips the calldata extraction step entirely.

---

## Component compatibility

Not all components are compatible with a full node. The table below summarises which components
can run against a full node and which require an archive node:

| Component | Full node compatible | Notes |
|---|---|---|
| `aggsender` | Yes | |
| `aggsender-validator` | Yes | |
| `aggoracle` | Yes | |
| `bridge` | **No** | Requires `debug_traceTransaction`  |

The `SyncFromInBridges` option defaults to `"auto"`, which means aggkit automatically decides
whether to use `debug_traceTransaction` based on the active components: it resolves to `true`
when the `bridge` component is present, and to `false` otherwise. **Do not override this value
manually** — just control it by choosing which components to start.

Because of this, **`bridge` must not be run in the same aggkit instance as the other
components** when using a full node. Doing so would force `SyncFromInBridges` to resolve to
`true` and fail against a non-archive node.

> **Important:** Run `bridge` in a separate instance with its own archive-node RPC endpoint, and
> run `aggsender` / `aggsender-validator` / `aggoracle` in a separate instance pointing to the
> full node.

---

## Usage of a full node for L1 RPC


The only configuration change needed for L1 is overriding the Initial Local Exit Root to avoid
a historical contract call at startup.

Add the `[L2NetworkConfig]` section to your config file with the `InitialLER` field set to the
known initial LER for your rollup:

```toml
[L2NetworkConfig]
# InitialLER overrides the on-chain query for the initial Local Exit Root.
# Required when using a full node (non-archive) as the L1 RPC endpoint.
# 0x000...000 is a valid value. Omit this field entirely to query the contract (requires archive node).
InitialLER = "0x0000000000000000000000000000000000000000000000000000000000000000"
```

Without this override, aggkit calls `GetRollupData` on the `RollupManager` contract at block
`AggSender.RollupCreationBlockL1`, which a full node cannot serve.

> **How to get this value:** Query the `RollupManager` contract with an archive node (or a block
> explorer) at the rollup creation block. For a newly created rollup whose bridge has never been
> used, the value is the zero hash (`0x000...000`).


> **Note:** The L1 claim synchronization (`ClaimEvent` calldata extraction via
> `debug_traceTransaction`) still requires an archive node. Unlike L2, there is no L1 contract
> that emits a `DetailedClaimEvent`, so calldata extraction cannot be avoided on L1. However,
> this synchronization is only performed when the `bridge` component is active. The compatible
> components (`aggsender`, `aggsender-validator`, `aggoracle`) do not run claim sync and
> therefore are not affected by this limitation.


---

## Usage of a full node for L2 RPC

For L2, no extra configuration is needed beyond running without the `bridge` component (see
[Component compatibility](#component-compatibility) above). As long as `bridge` is not in the
active component list, `SyncFromInBridges` resolves to `false` automatically and no
`debug_traceTransaction` calls are made. On sovereign chains (`AgglayerBridgeL2`), the
`DetailedClaimEvent` is used instead of calldata extraction, also without any extra configuration.

---

## Summary: minimal config diff for full node usage

```toml
[L2NetworkConfig]
InitialLER = "<initial-ler-hash-for-your-rollup>"
```

Make sure the instance does **not** include `bridge` in its component list.

---

## Trade-offs

| | Archive node instance | Full node instance |
|---|---|---|
| Components | `bridge` (+ others) | `aggsender`, `aggsender-validator`, `aggoracle` |
| `FromAddress` in bridge events | Populated | NULL (resolved automatically) |
| Initial LER | Queried from contract | Must be provided via `InitialLER` config |
| Node cost | High | Low |
