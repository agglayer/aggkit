# P7 Step Log

## Outcome

**ACCEPTED** — validation THUMBS_UP on **attempt 2**; change-request count: **1**.

Note: acceptance required a **takeover**. The first iteration-2 agent stalled ~50 minutes on a hung FEP enclave (the op-succinct contract-deployment step never returned), and a fresh takeover agent completed the fix by deterministically regenerating the compose from captured iteration-1 state rather than re-running the live enclave.

## Summary of work done

A real FEP / op-succinct enclave was brought up and a real snapshot generated (not a scaffold — genuine `summary.json` / `docker-compose.yml` / config bytes), then integrated into `test/e2e/envs/op-fep/` on the worktree branch `feat/e2e-envs-integration`.

- Final commits: worktree **`f333a89f`**, kurtosis-cdk **`b3e13ba9`** (snapshot-tool fixes, building on `05f04196` op-reth EL entrypoint and base `bd3308c9`).
- The op-fep env now **boots healthy** (`docker compose up -d` → RC 0 in ~74–75s; all core services healthy: agglayer, beacon, geth, op-geth-001 (op-reth) healthy, op-node-001, postgres-001, validator, aggkit-001 up).
- `LoadEnv` + `CheckEnv` **pass**; an **L1→L2 ERC20 bridge round-trip succeeds** (bridge mined, L1 Info Tree inclusion, L1InfoTreeLeaf injected on L2, claim proof fetched, L2 claim tx mined).
- `make build` RC 0; scoped `golangci-lint run ./test/e2e/...` 0 issues; `go vet ./test/e2e/` 0.
- `test/e2e/envs/README.md` provenance updated (kurtosis-cdk commit `b3e13ba9` pinned + honest boot/settlement status).

## Change-request history

**Attempt 1 → CHANGE_REQUEST.** The FEP L2 EL is **op-reth**, but the snapshot tool paired the op-reth image (`op-reth:v2.2.5`) with the **op-geth entrypoint** (`apk add jq; exec geth ...`) → container **exit 127** restart loop, so `docker compose up` / LoadEnv / CheckEnv could not complete. Classified as a **fixable-in-env defect** (the enclave and L1 chain demonstrably boot in this environment, so the infra-aware exemption did NOT apply). Build/lint/provenance/scope all already PASS.

**Attempt 2 → THUMBS_UP** after the EL-restore fix plus several follow-on compose fixes (below). The decisive iteration-1 defect was fixed and independently verified.

## Key decisions & deviations (non-obvious — CRITICAL for P8)

1. **op-reth EL restore fix** (kurtosis-cdk `05f04196` then `b3e13ba9`): `generate-compose.sh` now emits an **op-reth-correct entrypoint** when the restored EL image is op-reth; op-pp's op-geth path is unchanged and stays byte-identical. The `summary.json` logical service key remains `op-geth` (loader-compat) while the container actually runs op-reth.

2. **Regeneration path (takeover):** the takeover agent regenerated **only** the `docker-compose.yml`, deterministically, from the iteration-1 captured snapshot state. A live re-snapshot was impossible — agglayer never started in the hung enclave (re-running `discover-containers.sh` aborted at the Agglayer step), and the op-reth L2 EL restores fresh from genesis with no datadir capture. A reconstructed `discovery.json` (built verbatim from the committed, tool-emitted compose's image strings) fed the fixed `generate-compose.sh` into a scratch dir with byte-identical container names. The diff vs the broken compose was **EXACTLY the entrypoint/mount swap** (op-geth → op-reth) and nothing else — legitimate deterministic tool output, not a hand-edit.

3. **Additional minimal compose fixes needed once op-reth got past exit-127** (all on `b3e13ba9`, backward-compatible):
   - op-reth EL **healthcheck**: switched to JSON-RPC **POST `eth_chainId`** (op-reth 405s the op-geth GET probe, so it never went healthy and the gated op-node never started). Verified `eth_chainId` → `0x4ee9` (20201).
   - Baked-Teku (26.2.0) **FULU spec constants** added in `build-images.sh` (generated `spec.yaml` activated `FULU_FORK_EPOCH:3` but omitted Fulu/PeerDAS constants → Teku aborted); plus a **best-effort `genesis.ssz`** GenesisTimePatcher call so a patch failure under `set -e` no longer aborts (Teku boots from `checkpoint_state.ssz`).
   - op-succinct **proposer hostname rewrite** (original kurtosis hostnames → compose service names) + **writable `/app/configs`** (was read-only → "Read-only file system").
   - **TestMain LoadEnv timeout bump 1m → 5m** in `test/e2e/testmain_test.go` (op-reth boots in ~74–75s; op-pp unaffected).

4. **FEP enclave bring-up is flaky/heavy here:** the op-succinct contract-deployment step (`deploy-op-succinct-contracts.sh`) **HUNG for 50+ min** on the second bring-up. The live FEP enclave is **NOT reliably reproducible** in this environment.

## Remaining limitation (carry to P8 / P11 / P12)

**L2→L1 FEP settlement does NOT complete on a restored snapshot.** Root cause: op-succinct **v3.5.0** proposer enforces an on-chain L2OO **`rollupConfigHash` equality at startup with no skip/force flag**. Snapshot restore **re-anchors the L2 genesis timestamp** every boot (EL/CL/proposer patch genesis time to the restored L1 origin), which changes the recomputed rollup config hash — while the on-chain L2OO hash is fixed from the original live deployment baked into the L1 snapshot. Observed: `received=0x8cf7e86c… expected=0xa9f91de5… → Rollup config hash mismatch`. Consistent with the snapshot's documented **`settled: false`**.

Validation judged this **architectural** (beyond build+integrate scope) and explicitly **NOT** a fixable-in-env CHANGE_REQUEST basis → out of P7 scope, documented follow-up. Full remediation options (freeze/replay original L2 genesis time; snapshot after deterministic-restore contract deployment; redeploy/patch on-chain L2OO `rollupConfigHash`) are in the deliverable's Blockers section.

## Changed files

**aggkit worktree (`aggkit-envs`, committed `f333a89f`, builds on `2f7cdb77`):**
- M `test/e2e/envs/op-fep/docker-compose.yml` (op-reth entrypoint + POST `eth_chainId` healthcheck; proposer `/app/configs` rw)
- A `test/e2e/envs/op-fep/config/001/op-reth-entrypoint.sh`
- D `test/e2e/envs/op-fep/config/001/op-geth-entrypoint.sh`
- M `test/e2e/envs/op-fep/config/001/op-succinct/proposer.env` (rewired hostnames)
- M `test/e2e/testmain_test.go` (LoadEnv ctx 1m → 5m for op-reth boot time)
- M `test/e2e/envs/README.md` (provenance: cite `b3e13ba9` + boot status)
- (iteration-1 `2f7cdb77` added the full `test/e2e/envs/op-fep/` snapshot — 20 files — and the README `## op-fep` section.)

**kurtosis-cdk (`feat/aggkit-e2e-envs`, committed `b3e13ba9`):**
- M `snapshot/scripts/generate-compose.sh`
- M `snapshot/scripts/build-images.sh`
- (plus prior `05f04196` op-reth EL entrypoint script)

## Commands run (summary)

- `kurtosis run --enclave op-fep --args-file .github/tests/aggkit-e2e-envs/op-fep.yml .` (live bring-up, iter 1); `kurtosis enclave inspect op-fep`; `kurtosis enclave rm -f op-fep`.
- `./snapshot/snapshot.sh op-fep` (real snapshot generation, iter 1).
- Deterministic compose regeneration (takeover): reconstruct minimal `discovery.json` + `images/.tag` + `metadata/checkpoint.json` from captured state, re-run fixed `generate-compose.sh`, diff vs committed compose.
- `docker compose up -d` / `docker compose ps` / `docker compose down -v` (real boot + teardown).
- `E2E_ENV=op-fep go test ./test/e2e/... -run TestMain -count=1 -v` (LoadEnv + CheckEnv + L1→L2 bridge flow).
- `make build` (RC 0); `golangci-lint run ./test/e2e/...` (0 issues); `go vet ./test/e2e/`.
- git add/commit on worktree and kurtosis-cdk branches.

## Blockers

**None blocking P7 acceptance.** The op-reth EL boot defect from iteration 1 is fixed and proven.

- The **FEP L2→L1 settlement limitation** (rollupConfigHash / genesis-time re-anchoring tension) is documented **out-of-scope** — architectural, not fixable-in-env, consistent with `settled: false`.
- The **flaky live FEP enclave bring-up** (50+ min hang on contract deployment) is noted for P8 planning.

## Future-step updates

- **P8 (op-fep-committee)** builds on this and **WILL hit the same op-reth EL + op-succinct + FEP-settlement realities**. It should: reuse the op-fep env layout (including `config/001/op-succinct/`); reuse the fixed kurtosis-cdk tool (`b3e13ba9`); expect the same L2→L1 settlement limitation. The live committee+FEP enclave will likely be **even heavier/flakier** — budget for hangs, and **prefer regenerating the compose from captured state** over re-running the live enclave. Loader already has `EnvOpFEPCommittee` with `NativeGas:true`.
- **P11 (CI matrix):** do **NOT** wire op-fep into the CI matrix until/unless the boot is **CI-reproducible**. op-fep boots locally, but live FEP enclave generation is flaky. P11 should add the matrix entry only for the **BOOT/load/checks smoke** (settlement **excluded**). op-fep is a pure data addition; loader/checks/testmain already support it.
- **P12 (provenance/PR):** pin kurtosis-cdk **`b3e13ba9`** for op-fep provenance; surface the **FEP L2→L1 settlement limitation** in the PR description (and that op-fep / op-fep-committee need the settlement follow-up before they can be proven green for that leg in CI).
