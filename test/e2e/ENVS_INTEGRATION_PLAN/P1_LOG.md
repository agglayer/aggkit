# P1 Step Log

Step: **P1 — Discovery, decisions, and env spec lock-in**
Plan: `/home/aigent/repos/agglayer/aggkit/test/e2e/ENVS_INTEGRATION_PLAN.md`
Run: `run-20260529-150038`

## Outcome

- **ACCEPTED.**
- Validation: **THUMBS_UP** on attempt 1.
- Change-request count: **0**.
- Validation evidence: `/tmp/follow-plan/run-20260529-150038/P1/validation_result_1.md` — all acceptance criteria (a)–(f) PASS; claimed JSON fields/values independently re-verified against the real files, loader hardcodes confirmed in `loader.go`, and neither repo has any new P1-attributable change.

## Summary of work done

Produced a read-only discovery spec (`/tmp/follow-plan/run-20260529-150038/P1/execution_deliverable.md`) that locks in the integration scope for the four target envs and grounds every claim in real files across the **aggkit** (`/home/aigent/repos/agglayer/aggkit`) and **kurtosis-cdk** (`/home/aigent/repos/0xPolygon/kurtosis-cdk`) repos. The spec contains:

1. **Final env set (locked):** `op-fep` (FEP/op-succinct, mock prover, native ETH, single net `001`), `op-fep-committee` (separate env — FEP + aggoracle committee, quorum 2/3), `op-pp-2chains` (2 OP-PP L2s `001`+`002` sharing one L1/agglayer), `cdk-erigon-3chains` (3 cdk-erigon L2s `001`/`002`/`003`, custom gas inside the env). Matches the plan's *Target env set* table.
2. **Per-env kurtosis-args composition:** which aggkit `.github/test_e2e_*.json` presets merge for each env, mirroring the legacy CI deep right-biased merge (`jq -s 'reduce .[] as $item ({}; . * $item)'` in `.github/workflows/test-e2e.yml`), with quoted evidence and kurtosis-cdk preset correspondence. Arg names cross-checked against `src/package_io/input_parser.star` and `src/chain/shared/aggkit.star` (no field-name discrepancies).
3. **Worktree base-branch decision + P12 coordination/merge story.**
4. **Snapshot-tool gap list per env (feeds P3)**, grounded in `snapshot/snapshot.sh`, `snapshot/scripts/discover-containers.sh`, `generate-summary.sh`, `docs/docs/advanced/snapshot.md`.
5. **Loader gap list per env (feeds P5)**, grounded in real hardcodes in `test/e2e/envs/loader.go`, plus a `checks.go` side-note for P6.

## Key decisions & deviations

Items below were NOT explicit in the P1 step instructions and were decided/surfaced during discovery:

1. **Worktree base-branch = `origin/develop` (not `feat/migrate-e2e`).** The current aggkit checkout is on `feat/migrate-e2e`, which is **dirty and actively owned by another agent** (modified `test/e2e/README.md`; untracked `ENVS_INTEGRATION_PLAN.md`, `MIGRATION_PLAN.md`, `MIGRATION_PLAN/`, `cert_settlement_test.go`, `helpers_test.go`). Default branch confirmed `develop` via `symbolic-ref refs/remotes/origin/HEAD` and `remote show origin`. Chosen `origin/develop` because it is clean, structurally independent of the migration work, and the eventual merge target — keeps the diff reviewable/rebase-friendly and avoids racing on shared `test/e2e/...` files.
   - **P2 fallback note:** the op-pp env, `loader.go`, and `checks.go` that P5/P6 generalize must already exist on `origin/develop`. If they live **only** on `feat/migrate-e2e` (not yet on `develop`), P2 must fall back to basing the worktree on `feat/migrate-e2e` (or cherry-pick the loader/env baseline) and record it. This is the single concrete risk in the develop-base choice (deliverable §3 caveat).

2. **cdk-erigon custom-gas placement deviation (feeds P4/P10).** The plan's *Target env set* one-liner says cdk-erigon has "one custom-gas" chain. The actual legacy CI (`test-e2e.yml` lines ~139–147) applies `test_e2e_cdk_erigon_custom_gas_token.json` to **chains 001 AND 002** (`kurtosis-cdk-args-3` and `-4`), leaving **chain 003 native**. The spec documents the real composition. This refines what P4/P10 must reproduce; it does **not** change the env count (still one `cdk-erigon-3chains` env). Recorded as a deviation, not a re-litigation.

3. **`bridge_spammer` confirmation (feeds P4).** Confirmed it is an array entry, not a boolean flag: `aggkit/.github/test_e2e_op_succinct_args_base.json` contains `"args": { ..., "additional_services": [ "bridge_spammer" ] }`. This base feeds **both** `op-fep` and `op-fep-committee`. It must be **dropped** (set `additional_services: []` / excluded) before snapshotting per the "no settlement/bridge activity before snapshot" constraint in `snapshot.md`. The OP base and cdk-erigon base already set `"additional_services": []` (snapshot-clean), so only the two op-succinct envs carry the spammer.

4. **Snapshot `summary.json` service-key mismatch (flagged for P3).** `generate-summary.sh` emits the L2 execution RPC under JSON key `"op-reth"`, but the consumed `test/e2e/envs/op-pp/summary.json` and `loader.go` key on `"op-geth"`. Committed snapshots (e.g. `snapshot/snapshots/cdk-20260217-185547/summary.json`) contain `"op-geth"`, so the emitted summary is currently hand-massaged. P3 must reconcile the emitted key (and per-network ports) with what the loader parses.

## Changed files

**None.** P1 changed no repo files in either aggkit or kurtosis-cdk. All inspection was read-only; the sole output is the temp-dir spec `/tmp/follow-plan/run-20260529-150038/P1/execution_deliverable.md` (outside both repos). Validation confirmed `git status` on aggkit shows only pre-existing other-agent files and kurtosis-cdk is clean — no P1-attributable change.

## Commands run

Read-only inspection only:

- **Git state inspection (aggkit):** `git status`, `git branch`, `git log --oneline -5`, `git symbolic-ref refs/remotes/origin/HEAD` (→ `refs/remotes/origin/develop`), `git remote show origin` (HEAD branch: develop).
- **Git state inspection (kurtosis-cdk):** `git branch` (on `main`), `git log --oneline -3`.
- **Directory listings:** `snapshot/`, `snapshot/scripts/`, `.github/tests/op-succinct/`, `.github/tests/cdk-erigon/`, `.github/tests/other/gas-token/`, `snapshot/snapshots/`.
- **File reads / greps (read-only):** all ten aggkit `.github/test_e2e_*.json` presets, `.github/workflows/test-e2e.yml`, `test/e2e/envs/{loader.go,checks.go,README.md}`, `test/e2e/envs/op-pp/summary.json`; kurtosis-cdk `snapshot/snapshot.sh`, `snapshot/scripts/{discover-containers.sh,generate-summary.sh,...}`, `docs/docs/advanced/snapshot.md`, `main.star`, `src/package_io/input_parser.star`, `src/chain/shared/aggkit.star`, the `.github/tests/...` presets, and a spot-check of a committed `summary.json`.

## Blockers

**None.** All required files existed and were readable. The one downstream item (whether `op-pp/`, `loader.go`, `checks.go` are on `origin/develop` vs only `feat/migrate-e2e`) is a P2 verification step with a documented fallback, not a P1 blocker.

## Future-step updates

Concrete items each downstream step should carry forward (file/line references as the deliverable provided them):

- **P2 (worktree base + fallback):** Base the worktree on `origin/develop` (deliverable §3). Before generalizing, run `git -C <worktree> ls-files test/e2e/envs/` to confirm the op-pp env + `loader.go`/`checks.go` baseline exist on `develop`. If they exist only on `feat/migrate-e2e`, fall back to basing on that branch (or cherry-pick the loader/env baseline) and record the deviation.

- **P3 (snapshot tool — incl. key mismatch):** Biggest lifts are **cdk-erigon** (zero support today — `grep erigon` over snapshot scripts found only a doc comment in `generate-compose.sh:678`) and **op-succinct prover/proposer + committee keystore** capture (no `op-succinct`/`proposer` patterns in `discover-containers.sh`). Reconcile the `"op-reth"`-emitted (`generate-summary.sh` ~455–469) vs `"op-geth"`-consumed (`op-pp/summary.json`, `loader.go`) summary key, and ensure per-network distinct ports for multi-L2. Capture committee containers + the 3 member keystores `aggoracle-{0..2}.keystore` (`src/chain/shared/aggkit.star` ~573+; quorum 2/3). Surface the gas-token contract address in `summary.json` (not extracted today). Validators remain mandatory — keep that working for op-pp backward compat. Constraint: no settlement/bridge activity before snapshot (`snapshot.md`).

- **P4 / P10 (presets):** Mirror the CI merges exactly (deliverable §2). Apply custom gas on cdk-erigon **chains 001 AND 002**, native on 003 (`test-e2e.yml` ~139–147). **Drop** `additional_services: ["bridge_spammer"]` from **both** op-succinct envs (`op-fep`, `op-fep-committee`) before snapshotting; OP base and cdk-erigon base are already `[]` (snapshot-clean). Author a committee preset for `op-fep-committee` (no kurtosis-cdk committee `tests/` file exists today; committee flags come from the aggkit JSON and are valid per `input_parser.star`).

- **P5 (loader gaps):** Generalize the single-network hardcodes in `test/e2e/envs/loader.go`: `summary.Networks.L2Networks["001"]` (~239–243) and single `L2 L2Config` field (line 46) → expose all L2s with a backward-compatible single-network accessor; unconditional native-ETH `DeployMintableerc20(...)` (~293–319, confirmed line 312) → make conditional/per-network for `gas_token_enabled` chains; `const aggkitServiceName = "aggkit-001"` (line 527) and `aggkit001DataDir` (~632–634) and `config/001/...` paths (~426, 437, 585) → parameterize per network/aggkit; add an erigon RPC path (loader reads `l2Network.Services.OpGeth.HTTPRpc.External` ~252 and assumes op-stack shape); add committee-keystore loading (`loadAggOracleKey` loads exactly one `config/001/aggoracle.keystore` ~426). `waitForServices` (~751–761) breaks after the first network — must iterate all.

- **P6 (checks/CI side-note):** `checks.go` `checkConfiguration` hardcodes L1 ChainID `271828` and L2 ChainID `2151908` (op-pp values). New env chain IDs: FEP `20201`; op-pp-2chains `20201`+`20202`; cdk-erigon-3chains `20201`/`20202`/`20203`. Generalize to per-env/per-network expected values and iterate over all L2 networks.

- **P12 (coordination/merge):** Other agent's `feat/migrate-e2e` likely lands first (further along, owns those docs). This branch then rebases onto post-merge `develop`, resolving `test/e2e/README.md` (and `MIGRATION_PLAN.md`) additively. The loader/checks/CI generalization here is additive and should not conflict with migration test logic.
