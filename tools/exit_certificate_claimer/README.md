# exit_certificate_claimer

Backend (and, later, frontend) companion to the [`exit_certificate`](../exit_certificate) tool.
Given a destination address it returns the bridge exits available for that address and the full set
of parameters needed to call
[`AgglayerBridge.claimAsset`](https://github.com/agglayer/agglayer-contracts/blob/110bda5a03e70ee7331bc06407a8e79226d3e520/contracts/AgglayerBridge.sol#L537)
on L1.

```
tools/exit_certificate_claimer/
├── service/      Go HTTP service (this document)
└── frontend/     (not implemented yet)
```

## What it does

`claimAsset` requires a local-exit-tree proof, a rollup-exit-tree proof, the L1 exit roots, the
global index, and the bridge-leaf fields. This service assembles all of them from three sources:

| `claimAsset` argument        | Source |
| ---------------------------- | ------ |
| `smtProofLocalExitRoot`      | `step-g-l2bridgesyncerlite.sqlite` (the L2 local exit tree) — proof of the leaf at its deposit count against `new_local_exit_root` |
| `smtProofRollupExitRoot`     | L1 Info Tree DB — `GetRollupExitTreeMerkleProof(networkID, rollupExitRoot)` |
| `globalIndex`                | `GenerateGlobalIndexForNetworkID(networkID, depositCount)` |
| `mainnetExitRoot` / `rollupExitRoot` | the selected L1 Info Tree leaf |
| `originNetwork`, `originTokenAddress`, `destinationNetwork`, `destinationAddress`, `amount`, `metadata` | `exit-certificate-signed.json` (`bridge_exits[]`) |

The bridge-exit list is taken from the signed certificate; each exit is matched to its deposit count
(the exit-tree leaf index) by recomputing its canonical leaf hash and looking it up in the local
exit tree database.

> **Settlement requirement.** The certificate's `new_local_exit_root` must already be settled on L1
> — i.e. present in the rollup exit tree of some L1 Info Tree leaf. `/claim-params` verifies this
> against the selected leaf (latest by default) and returns HTTP `409` if it is not yet settled.

## Configuration

JSON or TOML, selected by file extension. See [config.toml.example](service/config.toml.example).
Relative paths resolve against the directory containing the config file.

| Field | Required | Description |
| ----- | -------- | ----------- |
| `address` | no (default `0.0.0.0`) | HTTP bind host/IP (without port) |
| `port` | no (default `8080`) | HTTP bind port |
| `signedCertificatePath` | yes | path to `exit-certificate-signed.json` |
| `localExitTreeDBPath` | yes | path to `step-g-l2bridgesyncerlite.sqlite` |
| `l1InfoTreeDBPath` | yes | path to the l1infotreesync SQLite DB |
| `stepWaitResultPath` | yes | path to `step-wait-result.json` (the WAIT step's L1 settlement record) |
| `networkId` | no | source network; defaults to the certificate's `network_id` |
| `l1Sync.enabled` | no | when `false` the L1 Info Tree DB is opened read-only; when `true` it is kept in sync from L1 |
| `l1Sync.rpcUrl`, `l1Sync.globalExitRootAddr`, `l1Sync.rollupManagerAddr`, … | when `l1Sync.enabled` | L1 sync parameters |

> **Settlement GER check on startup.** From the WAIT step's `updateL1InfoTree` event the claimer
> derives the certificate's settlement Global Exit Root (`keccak256(mainnetExitRoot, rollupExitRoot)`)
> and checks whether it is already indexed in `l1InfoTreeDBPath`. If it is, the DB is caught up to
> settlement and no L1 sync is started (regardless of `l1Sync.enabled`). If it is **not** indexed it
> must be synced from L1: with `l1Sync.enabled=true` the claimer syncs from L1 **only until the
> settlement GER is indexed**, then stops the sync and serves from that state; with sync disabled it
> **fails fast** with an error pointing at `l1Sync`. The HTTP server is started **only after** this
> sync completes — it does not bind until the L1 Info Tree is caught up to the settlement GER, so any
> reachable endpoint is already ready to serve claim requests (which is why `/health` always returns
> `ok`).

### Deriving the config from the exit_certificate tool

Instead of maintaining a separate claimer config you can derive it directly from an
[`exit_certificate`](../exit_certificate) config file with `--exit-certificate-config`
(mutually exclusive with `--config`). The claimer reuses the exit_certificate's output directory,
L1 RPC, contracts and tuning, and enables L1 sync so it keeps its own L1 Info Tree DB up to date.

| Derived claimer field | Source in the exit_certificate config |
| --------------------- | ------------------------------------- |
| `signedCertificatePath` | `options.outputDir` + `/exit-certificate-signed.json` |
| `localExitTreeDBPath` | `options.outputDir` + `/step-g-l2bridgesyncerlite.sqlite` |
| `l1InfoTreeDBPath` | `options.outputDir` + `/L1InfoTreeSync.sqlite` |
| `stepWaitResultPath` | `options.outputDir` + `/step-wait-result.json` |
| `networkId` | `l2NetworkId` |
| `l1Sync.enabled` | always `true` |
| `l1Sync.rpcUrl` | `l1RpcUrl` |
| `l1Sync.globalExitRootAddr` | `l1GlobalExitRootAddress` |
| `l1Sync.rollupManagerAddr` | `RollupManager()` read on-chain from the `aggchainbase` contract at `sovereignRollupAddr` |
| `l1Sync.initialBlock` | `options.l1StartBlock` |
| `l1Sync.syncBlockChunkSize` | `options.blockRange` |
| `l1Sync.blockFinality` | fixed `FinalizedBlock` |
| `address`, timeouts | claimer defaults |

The L1 sync uses the multidownloader-based l1infotreesync implementation, which keeps its own
storage and reorg processor (`l1infotree_multidownloader.sqlite`) next to the L1 Info Tree DB.

> Because `rollupManagerAddr` is not part of the exit_certificate config, deriving always makes an
> L1 RPC call to resolve it; `l1RpcUrl` and `sovereignRollupAddr` must be set and reachable.

## HTTP API

The HTTP API (endpoints, query parameters, response schemas, and error model) is fully specified in
[SPEC.md](SPEC.md#http-api). Base path: `/claimer/v1`.

## Build & run

```bash
make -C ../../.. $(go env GOPATH 2>/dev/null)/dev/null  # (or use the repo Makefile)
# from the repo root:
make build-tools                       # builds all tools, including exit_certificate_claimer
# or directly:
CGO_ENABLED=1 go build -o exit-certificate-claimer ./tools/exit_certificate_claimer/service/cmd

./exit-certificate-claimer --config tools/exit_certificate_claimer/service/config.toml

# derive the config from an exit_certificate config instead:
./exit-certificate-claimer --exit-certificate-config tools/exit_certificate/parameters.toml

# override the bind host/port from the command line (works in both modes):
./exit-certificate-claimer --config config.toml --address 127.0.0.1 --port 9090
```

`CGO_ENABLED=1` is required (SQLite via `mattn/go-sqlite3`).

## Tests

```bash
go test ./tools/exit_certificate_claimer/...
```
