# exit_certificate_claimer

Backend (and, later, frontend) companion to the [`exit_certificate`](../exit_certificate) tool.
Given a destination address it returns the bridge exits available for that address and the full set
of parameters needed to call
[`AgglayerBridge.claimAsset`](https://github.com/agglayer/agglayer-contracts/blob/110bda5a03e70ee7331bc06407a8e79226d3e520/contracts/AgglayerBridge.sol#L537)
(for a `Transfer` exit) or `AgglayerBridge.claimMessage` (for a `Message` exit, API v2) on L1.

```
tools/exit_certificate_claimer/
├── service/      Go HTTP service (this document)
└── frontend/     (not implemented yet)
```

## What it does

`claimAsset` and `claimMessage` take the same 11 arguments. They require a local-exit-tree proof, a rollup-exit-tree proof, the L1 exit roots, the
global index, and the bridge-leaf fields. This service assembles all of them from three sources:

| `claimAsset` / `claimMessage` argument | Source |
| ---------------------------- | ------ |
| `smtProofLocalExitRoot`      | `step-g-l2bridgesyncerlite.sqlite` (the L2 local exit tree) — proof of the leaf at its deposit count against `new_local_exit_root` |
| `smtProofRollupExitRoot`     | L1 Info Tree DB — `GetRollupExitTreeMerkleProof(networkID, rollupExitRoot)` |
| `globalIndex`                | `GenerateGlobalIndexForNetworkID(networkID, depositCount)` |
| `mainnetExitRoot` / `rollupExitRoot` | the selected L1 Info Tree leaf |
| `originNetwork`, `originTokenAddress`, `destinationNetwork`, `destinationAddress`, `amount` | `exit-certificate-signed.json` (`bridge_exits[]`) |
| `metadata` | the raw bridge metadata of the leaf in `step-g-l2bridgesyncerlite.sqlite` (the certificate only carries its hash) |

The bridge-exit list is taken from the signed certificate. Each exit is matched to its deposit count
(the exit-tree leaf index) **by position**: the certificate's exits are the last leaves of the local
exit tree, in the certificate's order, so exit `i` is the leaf at
`(leaves in the tree − exits in the certificate) + i`. The leaf hash is only a cross-check (the hash of
the exit must be the hash of the leaf at that position; checked for every exit on startup and again
for each exit served). Matching by hash would be ambiguous: two bridges with the same content — two
exits of the certificate, or an exit and an older leaf — have the same leaf hash but each has its own
deposit count and global index.

> **Message exits and message routes (API v2).** A `Message` exit (`bridgeMessage`) is addressed to a
> contract, and the holder it pays is only inside its `metadata` (e.g. the Circle USDC recovery:
> `ZkMinterBurner` on the L2 → `L1Escrow` on L1, `metadata = abi.encode(holder, amount)`, claimed with
> `claimMessage`, after which `L1Escrow` pays the holder in L1 USDC). With a
> [message route](#message-routes) configured for that pair of contracts, `/claimer/v2/bridges` and
> `/claimer/v2/claim-params` find the exit by the holder's address and return `claim_method`,
> `beneficiary`, `decoded_amount` and `display_token`.

> **Pending pre-certificate bridges.** The certificate only lists the exits made at the target block.
> L2→L1 bridges (assets and, when that step ran with `pendingClaimMessages`, messages) emitted earlier and never claimed on L1 are not in it, but their leaves are in
> the local exit tree. When `pendingBridgesPath` points to the exit_certificate
> [PENDING_CLAIMS step](../exit_certificate/README.md#step-pending_claims--unclaimed-l2l1-bridges-emitted-before-the-certificate)
> output (`step-pending-l2-bridges.json`), `/bridges` and `/claim-params` also return them, after the
> certificate's exits. Each is matched by the deposit count recorded in that file (not by leaf hash, which
> is ambiguous for identical exits) and its proof is verified against `new_local_exit_root` before being
> returned. Their `metadata` is the raw bridge metadata, with `metadata_hash` next to it. The claimer does not check whether they were claimed afterwards: the claim reverts with
> `AlreadyClaimed`, so re-run the PENDING_CLAIMS step to refresh the file.

> **After a delta.** When the exit_certificate tool ran in [delta mode](../exit_certificate/README.md#delta-mode--chain-additional-exits-on-a-previous-certificate),
> point `signedCertificatePath` at the latest delta's **`exit-certificate-full.json`** (every exit, previous
> + new, with the latest `new_local_exit_root`), `localExitTreeDBPath` at the latest delta's sqlite (it
> holds every leaf) and `stepWaitResultPath` at its `step-wait-result.json`. No other change: old exits
> keep the deposit count (and therefore the `globalIndex`) they had before the delta, and old and new
> exits alike are proven against the latest root. With `--exit-certificate-config` and a delta config
> (`options.deltaMode=true`) this is derived automatically.

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
| `signedCertificatePath` | yes (unless `runSource`) | path to `exit-certificate-signed.json` |
| `localExitTreeDBPath` | yes (unless `runSource`) | path to `step-g-l2bridgesyncerlite.sqlite` |
| `l1InfoTreeDBPath` | yes | path to the l1infotreesync SQLite DB |
| `stepWaitResultPath` | yes (unless `runSource`) | path to `step-wait-result.json` (the WAIT step's L1 settlement record) |
| `pendingBridgesPath` | no | path to `step-pending-l2-bridges.json` (PENDING_CLAIMS step output); when set, those bridges are also served by `/bridges` and `/claim-params` |
| `runSource` | no | archive of the run to serve, `gs://<bucket>/<base>/<networkID>/<height>`, or the archive base `gs://<bucket>/<base>` together with `runSourceHeight` (see [Serving an archived run](#serving-an-archived-run)); the four run data paths above must then be left empty |
| `runSourceHeight` | no | makes `runSource` the archive base: the run served is `<runSource>/<networkId>/<runSourceHeight>` (`networkId` is then required). `0` is a valid height |
| `followIntervalSeconds` | no (default `0`: disabled) | [follow mode](#follow-mode): look every that many seconds for the run of the next certificate height and switch to it without a restart. Needs `runSource` |
| `followCleanupOldRuns` | no (default `true`) | in follow mode, remove the local copy of a run once the next one is being served |
| `runDataDir` | no (default `archive-cache` next to the config) | base directory `runSource` is downloaded into |
| `networkId` | no | source network; defaults to the certificate's `network_id` |
| `messageRoutes` | no | list of [message routes](#message-routes): how API v2 decodes the beneficiary of `Message` exits |
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

### Message routes

A message route describes the `Message` exits one contract sends to another, so API v2 can find them
by the holder they pay:

```toml
[[messageRoutes]]
originNetwork = 1
originAddress = "0xBDa0B27f93B2FD3f076725b89cf02e48609bC189"        # ZkMinterBurner (zkEVM)
destinationNetwork = 0
destinationAddress = "0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB"   # L1Escrow
metadataLayout = "address,uint256"
displayToken = { address = "0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48", symbol = "USDC", decimals = 6 }
```

| Field | Description |
| ----- | ----------- |
| `originNetwork`, `originAddress` | the contract that called `bridgeMessage` (the leaf's origin network and address) |
| `destinationNetwork`, `destinationAddress` | the contract that receives the message (the leaf's destination) |
| `metadataLayout` | ABI layout of the metadata. Only `"address,uint256"` is supported: `abi.encode(address beneficiary, uint256 amount)` |
| `displayToken` | `address`, `symbol` and `decimals` (0–77) of the token the destination contract pays the beneficiary with. Only returned to the API client (`display_token`) to render `decoded_amount`; the claimer does not read the token on-chain |

Rules:

- Routes are validated on startup: `0x` non-zero addresses, a supported layout, a complete
  `displayToken`, and no two routes with the same origin and destination.
- A message is decoded only when the four route fields match its leaf **and** its metadata is exactly
  two 32-byte words whose address word has zero padding (what Solidity's `abi.decode` accepts).
  Otherwise — unknown route, or metadata of another shape — it is served undecoded and found by its
  destination address only.
- Where to put them: `messageRoutes` in the native config (in TOML, after every top-level key), or a
  separate JSON/TOML file with the same `messageRoutes` list passed with `--message-routes <file>`
  (env `CLAIMER_MESSAGE_ROUTES`). The flag works with `--config` (it **replaces** the routes of the
  file) and with `--exit-certificate-config`, where it is the only way to set them (the
  exit_certificate config has no routes).

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
| `pendingBridgesPath` | `options.outputDir` + `/step-pending-l2-bridges.json`, only when the file exists |
| `networkId` | `l2NetworkId` |
| `l1Sync.enabled` | always `true` |
| `l1Sync.rpcUrl` | `l1RpcUrl` |
| `l1Sync.globalExitRootAddr` | `l1GlobalExitRootAddress` |
| `l1Sync.rollupManagerAddr` | `RollupManager()` read on-chain from the `aggchainbase` contract at `sovereignRollupAddr` |
| `l1Sync.initialBlock` | `options.l1StartBlock` |
| `l1Sync.syncBlockChunkSize` | `options.blockRange` |
| `l1Sync.blockFinality` | fixed `FinalizedBlock` |
| `address`, timeouts | claimer defaults |
| `messageRoutes` | not derived: pass `--message-routes <file>` |

The L1 sync uses the multidownloader-based l1infotreesync implementation, which keeps its own
storage and reorg processor (`l1infotree_multidownloader.sqlite`) next to the L1 Info Tree DB.

> Because `rollupManagerAddr` is not part of the exit_certificate config, deriving always makes an
> L1 RPC call to resolve it; `l1RpcUrl` and `sovereignRollupAddr` must be set and reachable.

## Serving an archived run

The exit_certificate tool archives each settled run in a write-once GCS bucket
([`--step upload`](../exit_certificate/README.md#archiving-runs-in-a-gcs-bucket)). Instead of a local copy of
the output directory, the claimer can load the run data from there:

- **Native config:** set `runSource = "gs://<bucket>/<prefix>"` (and optionally `runDataDir`), and leave
  `signedCertificatePath`, `localExitTreeDBPath`, `stepWaitResultPath` and `pendingBridgesPath` empty.
- **Derived config:** `--exit-certificate-config parameters.toml --run-source gs://<bucket>/<prefix>` (or the
  `CLAIMER_RUN_SOURCE` environment variable). The run data goes to the exit_certificate's
  `options.archiveCacheDir` (by default `archive-cache` next to `options.outputDir`); the L1 Info Tree DB
  stays in `options.outputDir`.

The run is named in one of two ways:

- **The run itself:** `runSource = "gs://<bucket>/runs/1/0000000005"` — a prefix that ends with
  `/<networkID>/<height as 10 digits>`, the layout exit_certificate archives runs in.
- **The archive base plus a height:** `runSource = "gs://<bucket>/runs"`, `runSourceHeight = 5` and
  `networkId = 1` (`--run-source gs://<bucket>/runs --run-source-height 5` with a derived config, which takes
  the network from `l2NetworkId`). The claimer serves `<base>/<networkId>/<height>`.

(A prefix of any other shape is still accepted as a run, but it cannot be followed.)

On startup, before anything else, the claimer downloads into `<runDataDir>/<bucket>/<prefix>/` only what it
serves — the certificate (`exit-certificate-full.json` for a delta run, else `exit-certificate-signed.json`),
`step-g-l2bridgesyncerlite.sqlite`, `step-wait-result.json` and, when archived,
`step-pending-l2-bridges.json` — decompresses them (they are stored as `.zst`) and checks each object's size
and CRC32C and each file's size and SHA-256 against the run's
`archive-manifest.json`. A prefix without a manifest (incomplete upload) is refused. On a restart the files
already there are re-verified and only missing or different ones are downloaded again.

**Versions.** The manifest of an archived run has a `version` (the manifest format) and a `dataVersion` (the
format of the run files; a manifest without it is `dataVersion` 1). This claimer supports manifest version 1
and data version 1 (`SupportedDataVersions` in `service/archive.go`; extend it when the claimer learns to read
a new format). A run of any other version was written by a newer exit_certificate: at **startup the claimer
refuses it and exits** with a clear error (nothing is downloaded, and it never falls back to an older run);
in follow mode it keeps serving the run it has (below).

Read access to the bucket (`roles/storage.objectViewer`) is enough. Credentials are the Application Default
Credentials: `gcloud auth application-default login` locally, the workload identity / service account of the
pod in a cluster (that identity must be granted `objectViewer` on the bucket), or
`GOOGLE_APPLICATION_CREDENTIALS`.

```bash
./exit-certificate-claimer --exit-certificate-config parameters.toml \
  --run-source gs://polygonlabs-zkevm-prod-let-archive/runs/1/0000000005   # network 1, certificate height 5
```

### Follow mode

A running claimer serves one run. With follow mode it moves on its own to the run of each new certificate
(each delta) as soon as it is archived, with no restart and no downtime:

```toml
runSource             = "gs://<bucket>/runs"   # or the run itself: gs://<bucket>/runs/1/0000000005
runSourceHeight       = 5                      # the run to start from (only with the base)
networkId             = 1
followIntervalSeconds = 60
#followCleanupOldRuns = false                  # keep the local copy of the runs left behind
```

```bash
# derived config
./exit-certificate-claimer --exit-certificate-config parameters.toml \
  --run-source gs://<bucket>/runs --run-source-height 5 --follow-interval-seconds 60
```

Flags (derived config only; each has an environment variable): `--run-source-height`
(`CLAIMER_RUN_SOURCE_HEIGHT`), `--follow-interval-seconds` (`CLAIMER_FOLLOW_INTERVAL_SECONDS`),
`--follow-keep-old-runs` (`CLAIMER_FOLLOW_KEEP_OLD_RUNS`, the opposite of `followCleanupOldRuns`).

How it works, serving the run of height `h`:

1. Every `followIntervalSeconds` it reads `<base>/<networkID>/<h+1>/archive-manifest.json` — **one request**,
   no listing of the bucket (read access to objects is enough). No manifest there means nothing new: either
   nothing was archived at `h+1` yet, or its upload did not complete (the manifest is uploaded last).
2. When the manifest is there, it downloads and verifies the run into its own directory
   (`<runDataDir>/<bucket>/<base>/<networkID>/<h+1>/`), loads it, and syncs the L1 Info Tree until that run's
   settlement GER is indexed (so `l1Sync.enabled` is needed, unless the DB already has it).
3. Only then it swaps the run it serves, in memory. Requests in flight finish on the run they started with;
   the old run's database is closed once none uses it.
4. It checks `h+2` right away, so several runs archived while it was down or busy are applied one after
   another, in height order.

**If anything fails** (the archive cannot be read, a file does not match the manifest, L1 is unreachable, the
run's certificate is not the one expected at that height, …) the claimer **keeps serving the run it has**,
logs a warning and tries again at the next check. What was already downloaded or synced is kept. One attempt
to prepare a run is bounded to 1 hour.

**A next run of a version the claimer does not support is never switched to.** The claimer keeps serving the
current run, logs a warning on every check and sets `follow.blocked_by_unsupported_version` in `/health`:
upgrade the claimer. It never stops serving because of a newer run.

`/claimer/v2/health` (and the kept `/claimer/v1/health`) reports the run being served (`run`: `source`,
`height`, `certificate_hash`, `data_version`) and the follow state (`follow`: next prefix it looks at, time and result of the last check,
the unsupported-version flag); see [SPEC.md](SPEC.md#run-and-follow-fields-of-health).

Things to know:

- **Runs are applied strictly in order.** If the run of `h+1` was never archived the claimer stays at `h`,
  even if `h+2` is there. It does not detect that gap by itself (see the open points in SPEC.md): compare
  `run.height` in `/health` with the network's settled height.
- **Disk.** Each run is downloaded whole (its exit-tree DB is ~3 GB compressed and ~10 GB on disk on mainnet).
  The directory of the run left behind is removed after the switch unless `followCleanupOldRuns = false`.
- **Restart.** The claimer starts from the run the config names, then catches up through the following ones.
  Updating `runSourceHeight` (or `runSource`) to the latest height saves downloading the ones in between.
- A claim built from an older run stays valid on L1: it is anchored to that run's own settlement.

## HTTP API

The HTTP API (endpoints, query parameters, response schemas, and error model) is fully specified in
[SPEC.md](SPEC.md#http-api). The claimer serves **`/claimer/v2`**: `Transfer` and `Message` exits, looked up
by `address` (destination address, or beneficiary of a decoded message); every entry says its
`claim_method`. `/claimer/v2/health` returns `api_versions` (`[2]`).

API v1 has been removed: the claimer and the claims UI are deployed together, so there is no
side-by-side period. `/claimer/v1/health` still answers (with `api_versions: [2]`) so existing probes keep
working; any other `/claimer/v1/*` path answers `410 Gone`. See [SPEC.md](SPEC.md#api-versioning).

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

# ...serving the run archived in a GCS bucket instead of the files of its output dir:
./exit-certificate-claimer --exit-certificate-config tools/exit_certificate/parameters.toml \
  --run-source gs://<bucket>/runs/<networkID>/<height, 10 digits>

# message routes from a file (works in both modes; the only way with --exit-certificate-config):
./exit-certificate-claimer --exit-certificate-config tools/exit_certificate/parameters.toml \
  --message-routes message-routes.toml

# override the bind host/port from the command line (works in both modes):
./exit-certificate-claimer --config config.toml --address 127.0.0.1 --port 9090

# emit logs in JSON format (default is the human-readable console format):
./exit-certificate-claimer --config config.toml --log-json

# print the full build/version info and exit (same fields as GET /health):
./exit-certificate-claimer --version

# probe a running claimer and exit 0/1 (backs the docker-compose healthcheck; the production
# image has no shell or curl, so the binary performs the HTTP probe itself):
./exit-certificate-claimer healthcheck --address 127.0.0.1 --port 8080
```

`CGO_ENABLED=1` is required (SQLite via `mattn/go-sqlite3`).

`--version` (alias `-v`) prints the build traceability info — version, git revision/branch, build
date, Go version and OS/arch — the same data served by `GET /claimer/v2/health`. The values are
injected at build time via ldflags (use the Makefile target so they are populated).

## Tests

```bash
go test ./tools/exit_certificate_claimer/...
```

`TestAPIGolden` pins the HTTP contract: it compares every response of the API (health, bridges,
claim-params for `claimAsset` and `claimMessage`, error bodies) with the golden files in
[`service/testdata/api/`](service/testdata/api) (`requests.json` maps each file to its request and
status). They are built from a fixture that mirrors the Circle USDC route and are meant to be reused
as fixtures by API clients. After an intended change of the API, regenerate them with:

```bash
go test ./tools/exit_certificate_claimer/service/ -run TestAPIGolden -update
```

A diff in an existing file is a change of the public contract: see the
[versioning policy](SPEC.md#api-versioning).
