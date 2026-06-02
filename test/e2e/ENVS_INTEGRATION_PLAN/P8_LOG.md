# P8 Step Log

## Outcome

ACCEPTED. Validation returned THUMBS_UP on attempt 1; change-request count: 0.
This was a long, heavy run, but the live committee + FEP enclave was brought up
successfully this time and the committee-quorum probe ran against it on-chain.

## Summary of work done

Built and integrated a new `op-fep-committee` E2E env at
`test/e2e/envs/op-fep-committee/` — the `op-fep` FEP topology (op-reth EL,
op-node, agglayer, op-succinct mock proposer) PLUS an AggOracle committee
governed by an on-chain `AggOracleCommittee` proxy enforcing a 2-of-3 quorum.

Final commits:
- kurtosis-cdk `feat/aggkit-e2e-envs` HEAD = **`d71f4265`** ("snapshot: capture
  the AggOracle committee + flush L1 geth cleanly"), on top of `b3e13ba9`.
- aggkit worktree `feat/e2e-envs-integration` HEAD = **`e7c12277`** ("test(e2e):
  add op-fep-committee env + committee-quorum loader surface & probe").

The env boots healthy (all core L1/L2/committee services up; op-reth EL healthy,
NOT exit-127). LoadEnv binds the committee contract and CheckEnv passes; the
2-of-3 committee quorum probe passes (`quorum=2`, 4 members, true M-of-N).
`make build` is green and scoped `golangci-lint` reports 0 issues. Provenance is
recorded in `test/e2e/envs/README.md`, pinning kurtosis-cdk `d71f4265`.

## Key decisions & deviations

These are the non-obvious points — CRITICAL for P11/P12 and any future committee
work:

1. **The AggOracle committee is NOT cdk-data-availability / DAC.** The P3 DAC
   path (`cdk-data-availability`, `dac.keystore`) is a different/legacy topology
   that this committee env does not instantiate. The committee here is
   implemented as **extra aggkit services** `aggkit-001-aggoracle-committee-00N`
   (each launched with `--components=aggoracle`, each with its own
   `aggoracle-N.keystore` under `config/001/committee/00N/etc/`) PLUS an on-chain
   **`AggOracleCommittee` proxy** (2-of-3 quorum; address surfaced in
   `summary.json` as `networks.l2_networks.001.contracts.aggoracle_committee` =
   `0x49C53709d86653e9a68463D2dda2D545051899Cc`). The snapshot tool was extended
   to discover/capture these committee member services + keystores, record the
   committee proxy address, and adapt the captured members' Kurtosis hostnames so
   they don't crash-loop on L1 DNS after restore.

2. **L1-geth archive-flush bug fix (the real flakiness).** geth running with
   `--gcmode=archive` keeps most of the chain in memory and only persists state
   on a CLEAN graceful SIGTERM. A short `docker stop --time 5` (and even a forced
   `docker kill`) left the restored L1 at block 7 — hundreds of blocks behind its
   rollup L1-origin — so restored op-reth boot-looped "waiting for L1 block N"
   and never came up. The fix (in `extract-state.sh`) stops geth with a long
   graceful SIGTERM (`--time 180`) and never force-kills it, keeping the
   wall-clock-timeout + docker-kill fallback only for beacon/validator. After the
   fix the final snapshot's geth restored at block 156 (checkpoint 136, rolled
   back cleanly), rollup L1-origin 85 → bootable. This is a snapshot-tool fix on
   kurtosis-cdk that benefits ALL restored envs.

3. **Inherited from P7 and confirmed.** The EL is op-reth (logical key
   `op-geth`); the `b3e13ba9` op-reth/healthcheck/Teku/proposer bootability fixes
   carried through. The committee work adds only a minimal loader binding for the
   `AggOracleCommittee` contract, a ~91-line load/read quorum probe (a probe, NOT
   a migrated test), and 5 bounded snapshot-script edits.

## Remaining limitation (inherited, out-of-scope)

**L2→L1 FEP settlement `settled:false`.** The op-succinct proposer crash-loops
and TestMain's post-test L2→L1 bridge health-check FAILS on L1-Info-Tree
injection ("L1InfoTreeLeaf was not injected / bridge not included in L1 Info
Tree"). This is the same architectural op-succinct-v3.5.0 / genesis-time
re-anchoring limitation P7 documented for op-fep (the snapshot's re-anchoring
changes the on-chain rollup-config-hash the proposer enforces). The package-level
`go test` shows this one failing leg; it is explicitly OUT OF P8 SCOPE — the
validation decision rules forbade failing P8 for it. Boot, LoadEnv, CheckEnv, the
quorum probe, build, lint, and provenance all hold independently of settlement.
Carry forward to P11 (CI smoke must exclude settlement) and P12 (PR description).

## Changed files

**kurtosis-cdk (`d71f4265`)** — snapshot scripts:
- `snapshot/scripts/discover-containers.sh` (discover committee members)
- `snapshot/scripts/extract-state.sh` (capture committee `/etc/aggkit`; long
  graceful geth flush + no force-kill; beacon/validator kill-fallback)
- `snapshot/scripts/generate-compose.sh` (emit committee member services)
- `snapshot/scripts/generate-summary.sh` (committee address + services + accounts)
- `snapshot/scripts/adapt-l2-config.sh` (adapt committee config hostnames)

**aggkit worktree (`e7c12277`)**:
- `test/e2e/envs/op-fep-committee/` — new env: `docker-compose.yml`,
  `summary.json`, `config/` (incl. `config/001/op-succinct/` and
  `config/001/committee/000|001/etc/` with their `aggoracle-N.keystore`),
  gitignored `aggkit-001-data/`
- `test/e2e/envs/loader.go` — bind `AggOracleCommittee`; summary field
- `test/e2e/committee_probe_test.go` — new load+read quorum probe
- `test/e2e/envs/README.md` — op-fep-committee provenance

## Commands run

- `kurtosis run --enclave op-fep-committee --args-file
  .github/tests/aggkit-e2e-envs/op-fep-committee.yml .` (live committee + FEP
  enclave bring-up)
- `./snapshot/snapshot.sh op-fep-committee --tag p8v4 --skip-verify` (capture)
- `docker compose up -d` / `docker compose ps` / `docker compose down -v` (env +
  snapshot dirs)
- `cast call 0x49C53709… "quorum()(uint64)" / "getAllAggOracleMembers()(address[])"
  / "getAggOracleMembersCount()(uint256)"` (independent on-chain committee reads)
- `E2E_ENV=op-fep-committee go test ./test/e2e/ -run
  TestAggOracleCommitteeQuorumProbe -v` (TestMain → LoadEnv + CheckEnv, then the
  quorum probe)
- `make build`; `golangci-lint run ./test/e2e/... --timeout 5m`; `go vet`
- `kurtosis enclave rm -f` + bulk `docker rm -f` of stale kurtosis task
  containers (daemon recovery)

## Blockers

None blocking P8 acceptance. Boot + LoadEnv + CheckEnv + 2/3 committee-quorum
probe + build + lint + provenance are all green. The only failing leg is the
inherited, architectural FEP `settled:false` limitation, documented out-of-scope.

## Future-step updates

- **P9 / P10:** unaffected — different, non-FEP topologies.
- **P11:** add `op-fep-committee` to the CI matrix at the `# P11:` markers as a
  BOOT / load / checks / quorum smoke ONLY (settlement excluded; the job must NOT
  rely on TestMain's post-test L2→L1 bridge flow). E.g. an
  `E2E_ENV=op-fep-committee` job running `go test ./test/e2e/ -run TestMain` plus
  `-run TestAggOracleCommitteeQuorumProbe`. This env is heavy (committee + FEP +
  op-reth + proposer) — budget timeout and resources accordingly.
- **P12:** pin kurtosis-cdk `d71f4265` for op-fep-committee provenance (aggkit
  side `e7c12277`); surface in the PR the committee-≠-DAC implementation detail
  and the FEP-settlement limitation. The L1-geth archive graceful-flush fix is a
  general snapshot-tool improvement worth calling out (hardens all restored
  envs). For any future committee-test migration: the loader now binds
  `AggOracleCommittee` and exposes `Quorum()`, `GetAllAggOracleMembers()`,
  `GetAggOracleMembersCount()`, `GetAggOracleMemberIndex(addr)`,
  `AggOracleMembers(i)` via `L2Config.Contracts.AggOracleCommittee` /
  `AggOracleCommitteeAddress` (populated only on committee envs).
