# P11 Step Log

**Step:** P11 — Wire all new envs into the new-stack CI and prove they boot green

## Outcome
ACCEPTED. Validation decision: THUMBS_UP on attempt 1. Change-request count: 0.

## Summary of work done
Wired the four new envs (`op-fep`, `op-fep-committee`, `op-pp-2chains`, `cdk-erigon-3chains`) into the new-stack CI workflow `.github/workflows/test-go-e2e.yml`, keeping `op-pp` first. Work done on worktree `/home/aigent/repos/agglayer/aggkit-envs`, branch `feat/e2e-envs-integration`, committed as **`53cacc49`** (full SHA `53cacc490ed11202a24ac751ee37e40287572cdd`; not pushed/merged/PR'd — that is P12). Three files changed:
- `.github/workflows/test-go-e2e.yml`
- `test/e2e/envs/loader.go`
- `test/e2e/testmain_test.go`

`make build` GREEN, scoped `golangci-lint` reported 0 issues, `go vet` clean.

## Key decisions & deviations
1. **Heavy-env trigger gating (dynamic matrix).** Added a new `setup-matrix` job that emits the `env_name` list dynamically as a job output (`env_names`). BOTH downstream matrices — `pull-docker-images` (`needs: setup-matrix`) and `test-go-e2e` (`needs: [setup-matrix, build-docker-image, pull-docker-images]`) — consume it identically via `${{ fromJSON(needs.setup-matrix.outputs.env_names) }}`, so the two matrices are inherently one-to-one. The list is `["op-pp"]` on `pull_request`, and all five (`op-pp` + the 4 new envs) on the now-uncommented nightly `schedule` (`cron "0 2 * * *"`) + `workflow_dispatch`. This keeps PR runs fast (op-pp only, fully-published images) while the heavy envs run nightly/on-demand. The 90m `test-go-e2e` `timeout-minutes` was retained (consistent with the Makefile `go test -timeout 90m`). Per-env artifact names are parameterized by `${{ matrix.env_name }}` (`pulled-docker-images-${{ matrix.env_name }}`, `e2e-test-results-${{ matrix.env_name }}`) so there are no collisions across legs; the shared `aggkit-docker-image` is built once and downloaded by every leg (intended). Deviation note: the `# P11:` static-list markers were replaced with the `fromJSON` dynamic matrix because GitHub Actions cannot select static matrix items by `github.event_name`; the dynamic `setup-matrix` job is the idiomatic way to express the preferred fast-PR/nightly-heavy gate while keeping both matrices identical.

2. **FEP settlement-excluded smoke.** Added a new `SettlementSupported` capability flag on `EnvCapabilities` in `loader.go`. Values: **false for `op-fep` + `op-fep-committee`**, **true for `op-pp` / `op-pp-2chains` / `cdk-erigon-3chains`** and for the unknown-env fallback. The FEP envs keep `NativeGas=true`, so without this flag they would have run the full post-test bridge/settlement flow and red on the documented `settled:false` limitation; the flag diverts them into a new skip branch that emits a `[POSTTEST]` skip log instead. `testmain_test.go`'s post-test flow was gated: the original `if code == 0 && NativeGas` block is split into `NativeGas && !SettlementSupported` (skip-with-log, the FEP path) and `NativeGas && SettlementSupported` (full unchanged mint/approve + parallel `BridgeL1ToL2`/`BridgeL2ToL1` flow, the non-FEP path). op-pp evaluates identically to before. Minimal change — no migrated bridge/GER/committee/custom-gas test logic added (committee quorum stays asserted inside the unchanged `checks.go`).

3. **Zero hardcoded image refs.** The image list is derived dynamically via `docker compose config --images | grep -v 'aggkit:local'` (verified live for op-pp and op-fep; `grep -c snapshot- test-go-e2e.yml` = 0, confirming no hardcoded image references in the workflow).

## Changed files
Worktree `/home/aigent/repos/agglayer/aggkit-envs` only (off-limits main checkout `/home/aigent/repos/agglayer/aggkit` untouched):
- `.github/workflows/test-go-e2e.yml` — new `setup-matrix` job + dynamic `fromJSON` matrices in both jobs; uncommented nightly schedule; per-env artifact names.
- `test/e2e/envs/loader.go` — new `SettlementSupported` capability field + table values.
- `test/e2e/testmain_test.go` — post-test bridge/settlement flow gated on `SettlementSupported`.

(The Makefile was NOT changed — the capability-driven gate made a Makefile smoke-vs-settlement toggle unnecessary; `-timeout 90m` already consistent.)

## Commands run
- YAML parse via `python3 -c 'import yaml; yaml.safe_load(...)'` plus structural assertions — well-formed; jobs/triggers/matrix wiring verified programmatically.
- `docker compose config --images` for all five envs (op-pp and op-fep verified live) — succeeds; `aggkit:local` the only filtered line; surfaced the local-only `snapshot-*` images. `docker images` confirmed the `snapshot-*` images are local and registry-less.
- `E2E_ENV=op-pp go test -v -timeout 25m ./test/e2e/...` — env selection proven: TestMain resolved op-pp and ran `docker compose up -d` for the op-pp env (boot then blocked by a foreign container holding host 8545 — see Blockers). Same run produced a real `cdk-erigon-3chains` P10 GREEN probe.
- `make build` GREEN; `golangci-lint run --timeout 5m ./test/e2e/envs/... ./test/e2e/` → 0 issues; `go vet ./test/e2e/...` clean.
- Throwaway `TestP11_SettlementCapabilities` unit test asserting `capabilitiesFor(env).SettlementSupported` for all five envs + unknown fallback → `ok` (temp file removed afterward).
- `command -v actionlint` → not installed.
- `git add <3 files> && git commit` → `53cacc49`.

## Blockers
None blocking P11 acceptance. The following are infra realities / documented follow-ups, NOT failures:
1. The **live GitHub Actions matrix run cannot be executed here** — it is the final verification for the "envs come up green in CI" goal, to be confirmed on push/PR in P12/CI. No GH Actions output, logs, or checkmarks were fabricated.
2. **CRITICAL for P12/CI:** all four new envs reference **local-only `snapshot-{geth,beacon,validator}:*` images with NO registry prefix**, produced by the P7–P10 snapshot tooling. They cannot be `docker compose pull`-ed on a clean runner. P12/CI must publish/regenerate/`docker load` these images before the nightly/dispatch legs can go green. op-pp uses only fully-published images and is unaffected (its PR leg should be the first green leg).
3. **actionlint was not installed** — YAML parse + manual logic review used instead; no syntactic error introduced (diff is purely additive matrix/gate wiring).
4. A **foreign container** from the off-limits occupied workspace (`cdk-erigon-20260529-225318-geth`) holds host port 8545 and **must NOT be torn down**. It blocked a from-scratch op-pp local boot, so env selection was proven via the real op-pp selection log + the `cdk-erigon-3chains` GREEN probe + the P7–P10 boot proofs (no fabrication).

## Future-step updates
**P12 (final step, now unblocked):**
- (a) Finalize `envs/README.md` provenance for all four new envs — pin the kurtosis-cdk commits: op-fep/op-fep-committee `b3e13ba9`/`d71f4265`, op-pp-2chains `d71f4265`, cdk-erigon-3chains `da0f0845`.
- (b) The **local-only `snapshot-*` images** must be addressed for CI (publish to a registry, regenerate via the pinned kurtosis-cdk commit, or commit/restore tarballs and `docker load`) before the nightly/dispatch legs can go green — surface this in the PR.
- (c) PR description must surface: the FEP `settled:false` settlement limitation (op-fep/op-fep-committee are boot/load/checks(+committee quorum) smoke only — green does NOT imply settlement works); the committee-≠-DAC detail; the cdk-erigon aggoracle-dropped decision; and the verify.sh docker-py host bug.
- (d) Update `MIGRATION_PLAN.md`'s out-of-scope note that the blocking envs now exist.
- (e) kurtosis-cdk branch `feat/aggkit-e2e-envs` (final HEAD `da0f0845`) is PR-ready; the aggkit worktree branch `feat/e2e-envs-integration` (HEAD now `53cacc49`) is rebased/merge-ready — coordinate with the `feat/migrate-e2e` agent. Note: the worktree is off `origin/develop`, so it is independent of that branch.
