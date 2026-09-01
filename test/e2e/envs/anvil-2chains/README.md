# anvil-2chains

`AGGKIT_E2E_ENV=anvil-2chains`

Two independent anvil-backed L2 sovereign chains (L2-001 chain 20201, L2-002 chain 20202)
settling PessimisticProof certificates against a single anvil L1 (chain 271828) through one
agglayer, each with its own aggkit instance (merged aggsender + aggoracle + bridge + autoclaim
-- no separate `-bridge` sidecar), fronted by a shared aggkit-proxy. Sourced from a
kurtosis-cdk anvil devnet snapshot rather than a live `kurtosis run` per test invocation,
matching the `op-pp`/`op-pp-2chains` pattern in this directory.

## Provenance

- **kurtosis-cdk commit:** `fc160450b55e64332436f11c091c61130c64030f`
  (`0xPolygon/kurtosis-cdk`, branch `feat/aggkit-bridge-ui-backend`, PR #929's head).
- **Params files** (two sequential `kurtosis run` invocations into the same enclave):
  - [`params-aggkit-anvil-l2l2-run1.yml`](https://github.com/0xPolygon/kurtosis-cdk/blob/fc160450b55e64332436f11c091c61130c64030f/params-aggkit-anvil-l2l2-run1.yml)
    -- deploys L1 (anvil-001) + agglayer + rollup 1 (network_id 1, `aggkit-001`).
  - [`params-aggkit-anvil-l2l2-run2.yml`](https://github.com/0xPolygon/kurtosis-cdk/blob/fc160450b55e64332436f11c091c61130c64030f/params-aggkit-anvil-l2l2-run2.yml)
    -- adds rollup 2 (network_id 2, `aggkit-002`) into the same enclave, plus the
    `aggkit-proxy-001` / dev-ui stack that this e2e env doesn't use.
- **Snapshot build:** `snapshot/snapshot.sh <enclave> --flavor anvil-aggkit --tag <tag>` (see
  `.github/workflows/snapshot-devui.yml`), which seeds fixtures, captures live state, builds
  self-contained images, and emits `docker-compose.yml` / `docker-compose.mounts.yml` /
  `summary.json` / `config/` under `snapshots/<enclave>-<tag>/`.
- **Publish run:** [GitHub Actions run 31787941750](https://github.com/0xPolygon/kurtosis-cdk/actions/runs/31787941750)
  (`workflow_dispatch`, `publish=true`, ref `feat/aggkit-bridge-ui-backend`, resolved base tag
  `fc160450b55e`). Independent full `test` run at the same HEAD (all 18 jobs green, zero
  skipped): [run 31787908220](https://github.com/0xPolygon/kurtosis-cdk/actions/runs/31787908220).
- **Config tree source:** this directory's `config/` was copied from the same commit's local
  re-run of the snapshot pipeline (`snapshots/k8-20260814-095314/config/`,
  `docker-compose.mounts.yml` variant -- bare upstream `agglayer`/`aggkit` images with
  bind-mounted config, not the fully-baked default variant), then renamed per the mapping
  documented in `snapshot/scripts/extract-state.sh` (kurtosis-cdk names each directory after
  its own service, e.g. `config/aggkit-001/config.toml`; this env instead keys per-L2
  directories by bare network prefix and calls the aggkit config file
  `aggkit-config.toml`, matching `op-pp-2chains`'s own layout):

  | kurtosis-cdk (emitted) | this env |
  |---|---|
  | `config/agglayer/config.toml` | `config/agglayer/config.toml` |
  | `config/agglayer/aggregator.keystore` | `config/agglayer/aggregator.keystore` |
  | `config/aggkit-001/config.toml` | `config/001/aggkit-config.toml` |
  | `config/aggkit-001/{sequencer,aggoracle,sovereignadmin}.keystore` | `config/001/{sequencer,aggoracle,sovereignadmin}.keystore` |
  | `config/aggkit-002/config.toml` | `config/002/aggkit-config.toml` |
  | `config/aggkit-002/{sequencer,aggoracle,sovereignadmin}.keystore` | `config/002/{sequencer,aggoracle,sovereignadmin}.keystore` |
  | `config/aggkit-proxy-001/config.toml` | `config/aggkit-proxy/aggkit-proxy.toml` |

  Only hostnames/internal service names needed to line up (they already did -- this env keeps
  the same compose service names the bundle uses: `anvil-001`, `l2-anvil-001`, `l2-anvil-002`,
  `agglayer`, `aggkit-001`, `aggkit-002`, `aggkit-proxy-001`) and only host ports changed (see
  below); no contract addresses or private keys needed to change, because the bundle's L1/L2
  mnemonics are byte-identical to `op-pp-2chains`'s own (`giant issue aisle ... athlete` / `test
  test ... junk`), so every deterministically-derived contract address matches address-for-address.

## Images (`name@digest`)

Independently re-verified as anonymously pullable from `ghcr.io/0xpolygon` (see the plan's
`K8-evidence/HANDOFF.md` and `06-anonymous-pull-proof.log` / `08-registry-digest-comparison.txt`
for the verification detail):

| Service | Image | Tag |
|---|---|---|
| `anvil-001` | `ghcr.io/0xpolygon/kurtosis-cdk-snapshot-anvil-001@sha256:006932fc49ce501c8d6f8c3f4ac3b5873ec14b59b101b4f4e9db02b169e6c0c9` | `v1.5.1-1786700247` |
| `l2-anvil-001` | `ghcr.io/0xpolygon/kurtosis-cdk-snapshot-l2-anvil-001@sha256:e9bbeb7f9a76a4ea725f194059c6b23d49c65e89a8a00241d6df3b687c8ccbb8` | `v1.5.1-1786700247` |
| `l2-anvil-002` | `ghcr.io/0xpolygon/kurtosis-cdk-snapshot-l2-anvil-002@sha256:3555d50518f6f72d5811edd759293ba205ac192c04192695afc046c2cb595ef0` | `v1.5.1-1786700247` |
| `agglayer` | `ghcr.io/0xpolygon/kurtosis-cdk-snapshot-agglayer@sha256:5a47d3778657ba618ff7dfc99dfd55a3863097d5fff4f960a0740d4d0ae80073` | `0.6.0-rc.8-1786700247` |
| `aggkit-001` / `aggkit-002` / `aggkit-proxy-001` | **not** the snapshot's baked aggkit image (`kurtosis-cdk-snapshot-aggkit-*`) -- this env runs `aggkit:local`, this repo's own build, so the binary under test is always the checkout in this worktree, not a pinned upstream aggkit release. |

`aggkit:local` is built by `make build-docker` (`docker build -t aggkit:local ... -f ./Dockerfile
.`, see the top-level Makefile) and is expected to already exist before running this env's
tests, exactly like `op-pp`/`op-pp-2chains`.

## Chain / network IDs

| | chain_id | network_id |
|---|---|---|
| L1 (`anvil-001`) | 271828 | 0 |
| L2-001 (`l2-anvil-001`) | 20201 | 1 |
| L2-002 (`l2-anvil-002`) | 20202 | 2 |

## Ports (host, collision-checked against `op-pp` and `op-pp-2chains`)

| Service | Container port(s) | Host port(s) |
|---|---|---|
| `anvil-001` | 8545 | 13545 |
| `agglayer` | 4443/4444/4446/9092 | 13443/13444/13446/13092 |
| `l2-anvil-001` | 8545 | 14545 |
| `aggkit-001` | 5576/5577/5579 | 14576/14577/14579 |
| `l2-anvil-002` | 8545 | 15545 |
| `aggkit-002` | 5576/5577/5579 | 15576/15577/15579 |
| `aggkit-proxy-001` | 8080 | 15601 |

## Known deviations from the design note

`config/agglayer/config.toml`'s `[full-node-rpcs]` / `[proof-signers]` here has entries for
**both** network 1 and network 2 (unlike `op-pp-2chains`'s own config, which only has a
network-1 entry). This is intentionally carried forward as emitted by the kurtosis-cdk bundle
rather than trimmed to match `op-pp-2chains`'s pattern: the bundle's own two-network config is
the one that was actually exercised end-to-end by kurtosis-cdk's test run (18 jobs green,
including PessimisticProof settlement on both rollups), so it is a stronger working precedent
here than `op-pp-2chains`'s incomplete (network-1-only) config for a topology that doesn't
happen to need network 2's entry for its own tests to pass.

## Regenerating this env

1. Check out kurtosis-cdk at `fc160450b55e64332436f11c091c61130c64030f` (or a descendant that
   hasn't changed the anvil-aggkit flavor's shape).
2. `kurtosis run --enclave=cdk --args-file=params-aggkit-anvil-l2l2-run1.yml .`
3. `kurtosis run --enclave=cdk --args-file=params-aggkit-anvil-l2l2-run2.yml .`
4. `snapshot/snapshot.sh cdk --flavor anvil-aggkit --tag <tag>` -- produces
   `snapshots/cdk-<tag>/{docker-compose.yml,docker-compose.mounts.yml,summary.json,config/}`.
5. Copy `docker-compose.mounts.yml` as the starting shape for this directory's
   `docker-compose.yml` (swap in digest-pinned image refs for `anvil-001`/`l2-anvil-001`/
   `l2-anvil-002`/`agglayer`, and `aggkit:local` for `aggkit-001`/`aggkit-002`/
   `aggkit-proxy-001`; keep the TCP-connect healthcheck on `agglayer` -- see the file header
   comment in `docker-compose.yml` for why).
6. Copy `config/` into this directory's `config/`, renaming per the mapping table above.
7. Rewrite `summary.json` into aggkit's own schema (see `test/e2e/envs/loader.go`'s
   `summaryJSON`/`summaryL2Network` structs for the exhaustive list of keys actually read) using
   this bundle's own `summary.json` (`chain_ids`, `network_ids`, `accounts.funded` filtered by
   `funded_on`, `networks.l1.contracts`, `networks.l2.*.contracts`) plus the host ports chosen
   in step 5.
8. Confirm `TriggerCertMode = "ASAP"` is still explicit in both `config/001/aggkit-config.toml`
   and `config/002/aggkit-config.toml` (`Auto` silently resolves to `EpochBased` for a
   PessimisticProof aggsender).
9. To publish new digests for step 5's image refs, re-run
   `.github/workflows/snapshot-devui.yml` with `publish=true` and copy the resulting
   `name@digest` triples from the run's summary/logs.
