# P10 Step Log

## Outcome
ACCEPTED. Validation THUMBS_UP on attempt 1; change-request count: 0. Heavy long run; the live 3-chain cdk-erigon enclave was brought up successfully and independently re-verified.

## Summary of work done
Added a new `cdk-erigon-3chains` env at `test/e2e/envs/cdk-erigon-3chains/` — three cdk-erigon L2s (incl. custom gas) under one shared L1/agglayer.

Commits:
- kurtosis-cdk `feat/aggkit-e2e-envs` **`da0f0845`** ("snapshot: faithfully capture + boot 3-chain cdk-erigon (incl. custom-gas)").
- aggkit worktree `feat/e2e-envs-integration` **`a51f2508`** ("test(e2e): add cdk-erigon-3chains env (3 cdk-erigon L2s, two custom-gas)").

All 3 cdk-erigon L2s boot healthy (10 compose services) from the restored snapshot under one shared L1/agglayer. `LoadEnv` exposes 3 L2s; `CheckEnv` + the per-chain probe pass; `make build` + scoped lint (0 issues) are green; README provenance added. Independently re-verified by validation via live boot + on-chain reads.

Per-chain:
- 001 — chainId 2151908, custom-gas (`0x72ae2643518179cF01bcA3278a37ceAD408DE8b2`), gas-token surfaced, MintableERC20 SKIPPED.
- 002 — chainId 20202, custom-gas (`0xB965D10739e19a9158e7f713720B0145D996E370`), gas-token surfaced, MintableERC20 SKIPPED.
- 003 — chainId 20203, native, MintableERC20 deployed (`0xB9a916D0…`).

## Key decisions & deviations
All deviations were validated as in-scope for an env build and break no P10 acceptance criterion.

1. **cdk-erigon aggkit drops the `aggoracle` component** (runs `--components=aggsender,bridge` only). A snapshot-clean fresh-sequencer cdk-erigon chain doesn't replay the post-genesis L2 GER-manager `initialize()`, so `globalExitRootUpdater()` reverts and aggoracle would crash-loop. Confirmed by direct eth_call: the GER proxy delegates correctly (`bridgeAddress()` returns) while `globalExitRootUpdater()`/`depositCount()`/`rollupManager()` revert and proxy storage slot 0 is empty. aggsender + bridge come up healthy — which is exactly what the loader's readiness dependency needs. (Aggoracle/GER functionality would be test-migration scope, out of P10.)
2. **Chain 001 keeps the cdk-erigon default chainId 2151908** (not 20201) — doc 1 of the preset does not set `l2_chain_id`. The probe asserts the live chainId + per-network NetworkID (1) rather than hardcoding 20201.
3. **`EnvCDKErigon3Chains.NativeGas` reinterpreted as "native deploys permitted"** (flipped from P5's `false` to `true`) so the per-network `gas_token` gate drives the real decision: 001/002 skip MintableERC20 + surface their gas token, 003 deploys MintableERC20. op-* envs have no gas_token, so they are unaffected.
4. **cdk-erigon is a genuinely different sequencer** (`cdk-erigon:v2.65.0-RC1`, not op-geth/op-reth). This required bounded snapshot-tooling fixes (discover/extract/summary/compose) to capture all 3 networks + chain ids + contracts + the two custom gas tokens + funded keys, and to make the cdk-erigon EL bootable (entrypoint/command, `user: "0:0"`, kurtosis-hostname aliases). The loader gained a cdk-erigon RPC selection helper + gas-token surfacing + a per-network gas model (extending P9's per-network dialing). op-* env paths were left untouched and byte-compatible.

## Changed files
kurtosis-cdk (`feat/aggkit-e2e-envs` @ `da0f0845`) — all gated on `chain_type == "cdk-erigon"` / absence of op-stack artifacts (backward-compatible):
- `snapshot/scripts/discover-containers.sh`
- `snapshot/scripts/extract-state.sh`
- `snapshot/scripts/generate-summary.sh`
- `snapshot/scripts/generate-compose.sh`

aggkit worktree (`feat/e2e-envs-integration` @ `a51f2508`):
- `test/e2e/envs/cdk-erigon-3chains/` (new: `summary.json`, `docker-compose.yml`, `config/{001,002,003,agglayer}/…`; empty `aggkit-{001,002,003}-data/` dirs are gitignored, recreated by the loader).
- `test/e2e/envs/loader.go` (cdk-erigon RPC selection helper `l2RPCURLForNetwork`, gas-token surfacing on `L2Contracts.GasTokenAddress`, per-network gas model).
- `test/e2e/envs/zz_p10_probe_test.go` (new — per-chain health probe harness; verification only, not a migrated bridge test).
- `test/e2e/envs/README.md` (provenance section).

## Commands run
- Multi-doc kurtosis bring-up: 3 docs applied IN ORDER into ONE shared enclave `cdk-erigon` (`kurtosis run --enclave cdk-erigon --args-file /tmp/cdk-{1,2,3}.yml .`); preset split with PyYAML (`yaml.safe_load_all`) since `yq` is absent on host.
- `snapshot/snapshot.sh cdk-erigon --out … --skip-verify` (produced snapshot `cdk-erigon-20260529-225318`).
- `docker compose up -d` / `docker compose ps` / `docker compose down -v --remove-orphans` to prove bootability and tear down.
- `E2E_ENV=cdk-erigon-3chains go test ./test/e2e/envs/ -run TestP10_CDKErigon3Chains_LoadAndProbe -v -count=1` (TestMain LoadEnv + CheckEnv + per-chain probe) — PASS.
- On-chain reads: chainId / advancing block / NetworkID / gas-token per chain.
- `make build` (green); `golangci-lint run ./test/e2e/...` (0 issues); `go vet ./test/e2e/envs/...` (clean).
- `kurtosis enclave rm -f cdk-erigon`.

## Blockers
None. The bundled `verify.sh` was not used (docker-py host bug); bootability was proven instead via `docker compose up -d` + the LoadEnv path.

## Future-step updates
**P11 (now unblocked — all of P7/P8/P9/P10 done):** add `cdk-erigon-3chains` to the live CI matrix (currently only `# P11:` markers exist; the env is NOT yet wired into a live matrix) as a 3-L2 boot/load/checks/per-chain smoke. It is the heaviest non-FEP env (3 cdk-erigon ELs + 3 aggkits + L1 geth/beacon/validator + agglayer; the 3 ELs re-derive from L1 on boot, so allow generous startup — EL healthcheck `start_period` is 180s). No settlement caveat (cdk-erigon-PP is not FEP). Selectable via `E2E_ENV=cdk-erigon-3chains`. The full new env set for P11's matrix: `op-fep`, `op-fep-committee` (FEP — boot/quorum smoke, settlement excluded), `op-pp-2chains`, `cdk-erigon-3chains`.

**P12:** pin kurtosis-cdk `da0f0845` for cdk-erigon-3chains provenance. Note for provenance/cleanup:
- the cdk-erigon aggoracle-component-dropped decision (and that restoring it would require restoring the captured erigon datadir holding the initialized GER manager instead of booting a fresh-volume sequencer);
- the custom-gas-on-001+002 detail (gas token sourced from the deployer `combined-<prefix>.json` `gasTokenAddress`);
- the `verify.sh` docker-py host bug;
- consider generalizing the loader's `L2Config.OpGethRPCURL` field name to a sequencer-agnostic name (e.g. `L2RPCURL`) — it is now populated sequencer-agnostically (op-geth or cdk-erigon); a `// NOTE (P12)` marker is in the field doc.

Final branch HEADs after P10: kurtosis-cdk `da0f0845`, aggkit worktree `a51f2508`.
