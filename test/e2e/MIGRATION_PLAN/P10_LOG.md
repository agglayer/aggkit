# P10 Log — Migrate `aggsender-committee-updates.bats` → `TestCommitteeUpdates`

**Step:** P10 — Migrate `aggsender-committee-updates.bats` → add/remove committee validator (requires env change)

**Final outcome:** completed (validator THUMBS_UP, attempt 1; feasibility = FEASIBLE). Live verification deferred to P10b full-suite gate.

## Work done

Ported the two bats `@test`s ("Add single validator to committee", "Remove single validator from committee") to a single pure-Go test `TestCommitteeUpdates` with two sequential subtests:

- add-signer + raise-threshold → start validator → assert SETTLED cert height advances → remove signer + restore threshold → assert height advances again.
- Pure Go throughout: `aggchainbase` bindings + the P2 read-RPC helpers (no `cast`/`jq`).

Additive env plumbing for an on-demand committee validator container:

- Profile-gated (`profiles: ["committee"]`) `aggsender-validator-004` service added to the op-pp docker-compose (existing services untouched).
- New `envs/op-pp/config/validator-004/` directory: `config.toml` (op-pp hostnames) + `aggsendervalidator-4.keystore` (copied verbatim from the upstream `attach-new-committee-members` scenario).
- `Start/StopAggsenderValidator` loader helpers + service/profile name constants added to `envs/loader.go`. No change to `waitForServices`, `ensureDockerComposeRunning` default `up -d`, or any existing service logic.

## Signing-identity coherence (key risk, resolved)

The added on-chain signer address = `0x77A21F79994876973BeF5bbcbbd617a5B32B2f57` = the validator container's keystore signing key = `[Validator].Signer`. The member URL = the container hostname (`http://aggkit-001-aggsender-validator-004:5578`). The update tx is authorized by `env.Keys.SovereignAdmin` (== on-chain `aggchainManager`, OnlyAggchainManager). Therefore threshold=2 is satisfiable and certs won't stall — `remote_validator.go` recovers the returned signature's address and requires it to equal the on-chain member `Addr`, which holds because added-signer == container-signer.

Note: the legacy bats had a latent mismatch — it added the **sovereign-admin** address while running a container signing with the **validator-4** key. The Go port fixes this so added-signer == validator-signer (the sanctioned resolution).

## Additive/optional proof

- `docker compose config --services` with NO profile omits the validator (only 7 default services: `aggkit-001, agglayer, beacon, geth, op-geth-001, op-node-001, validator`); with `--profile committee` the `aggsender-validator-004` service appears.
- `waitForServices` / `ensureDockerComposeRunning` / `up -d` / `summary.json` / `aggkit-001` all untouched → the other 9 tests + post-suite health check are unaffected.
- Test `t.Cleanup` always stops + removes the container and restores the original `(signers, threshold)` snapshot (threshold=1 / single signer `0x5b06...`).

## Validation

- Decision: **THUMBS_UP** (attempt 1, 0 change requests).
- `go build ./test/e2e/...` → exit 0; `go vet ./test/e2e/...` → exit 0; `golangci-lint run ./test/e2e/...` → `0 issues.`
- `docker compose -f test/e2e/envs/op-pp/docker-compose.yml config -q` → exit 0; profile-gating proven via `config --services` with/without `--profile committee`.
- Keystore decrypted (password `pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m`) → confirmed derived address `0x77A2...` matches the added on-chain address and the container Signer key; keystore byte-identical to upstream scenario.
- Both flows verified faithful to the bats; write scope respected (only the 4 permitted P10 artifacts).

## Change-request count

0.

## Changed files

- `test/e2e/committee_updates_test.go` (new)
- `test/e2e/envs/op-pp/docker-compose.yml` (additive profiled service, one added block)
- `test/e2e/envs/op-pp/config/validator-004/config.toml` (new)
- `test/e2e/envs/op-pp/config/validator-004/aggsendervalidator-4.keystore` (new)
- `test/e2e/envs/loader.go` (`Start/StopAggsenderValidator` + constants)

## Commands run

`go build`, `go vet`, scoped `golangci-lint run`, and `docker compose config -q` — all clean (exit 0), run by both executor and validator. Read-only live probes confirmed current committee state (threshold=1, single signer, `aggchainManager` == SovereignAdmin). The long live `go test -run TestCommitteeUpdates` was NOT run (deferred to P10b).

## Blockers / notes for P10b / P11

- **P11 (CI image pull):** unchanged — the validator uses the same `aggkit:local` image as `aggkit-001`. No new image to add to any pull list.
- **P10b (live gate):** `TestCommitteeUpdates` will start/stop the profiled `aggsender-validator-004` container on-demand. Ensure the full-suite run context has the `committee` compose profile available so the container can be started, and that teardown removes profiled containers (the test's `rm -sf` cleanup already does this). P10b must run the live test and re-prove the post-suite health check stays green (committee restored to threshold=1 / single signer `0x5b06...`, container removed).
- **Loader API added:** `(*envs.Env).StartAggsenderValidator(ctx)` / `StopAggsenderValidator(ctx)`.
