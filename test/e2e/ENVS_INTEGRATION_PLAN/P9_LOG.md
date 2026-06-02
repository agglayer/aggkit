# P9 Step Log

## Outcome
ACCEPTED. Validation returned THUMBS_UP on attempt 1. Change-request count: 0.

## Summary of work done
Added a new `op-pp-2chains` env (2 OP-PP L2s under one shared L1/agglayer) at `test/e2e/envs/op-pp-2chains/`.

- Worktree commit on branch `feat/e2e-envs-integration`: **`83b4abbf`** (`83b4abbf565296f8f5998b3bc61fe11aa1100e0a`).
- kurtosis-cdk **UNCHANGED** at `d71f4265` (`d71f4265a51467b5b665fa3ad80110117446ffd3`) — the multi-L2 capture path worked out of the box, no source change warranted.
- Both L2s boot healthy from the restored snapshot: chain **001** (chainId **20201**, networkID 1) and chain **002** (chainId **20202**, networkID 2), sharing one L1 (chainId 271828) and one agglayer.
- `LoadEnv` returns `len(Env.L2s)==2`; `L2ByNetworkID(1)`→001, `L2ByNetworkID(2)`→002 with distinct per-chain wiring (aggkit-001 rpc :11576 / aggkit-002 rpc :12576), bridge + GER bindings populated per chain.
- `CheckEnv` validates BOTH networks (iterates `e.L2s`); per-chain health probes pass for both (chainId match + advancing block + non-zero pre-funded balance).
- `make build` green (`target/aggkit`, `v0.10.0-rc1-19-g83b4abbf`); scoped lint `golangci-lint run ./test/e2e/...` → 0 issues; `go vet` exit 0.
- README provenance entry added.
- Independently re-verified by validation: all 10 services booted via `docker compose up -d`, real on-chain per-chain reads (chainId / block advancing / balance), Go LoadEnv/CheckEnv/probe test re-run to PASS.

## Key decisions & deviations
1. **Multi-doc preset applied into ONE enclave.** The 2-chain enclave was built by sequential `kurtosis run` of the two-document `op-pp-2chains.yml`: doc 1 (chain 001) deploys L1 + agglayer + L2-001; doc 2 (chain 002) reuses them via `deploy_l1:false` / `deploy_agglayer:false` and adds L2-002. The whole enclave was then snapshotted. The resulting `summary.json` lists BOTH `001` and `002` with distinct ports (op-geth 11545 / 12545, aggkit 11576 / 12576) and distinct per-network accounts (35 each, non-identical descriptions). The shared L1 bridge / rollup-manager addresses are intentionally the SAME across both networks — correct for shared-L1 OP-PP (l2_bridge + GER are OP predeploys at fixed L2 addresses; l1_bridge + rollup_manager are the shared L1 contracts both rollups register on). The distinguishing identity (chainId, ports, accounts, separate aggkit service, distinct per-rollup sovereign address on L1) is genuinely distinct — not a fabricated duplicate. Rollup 002 registered on the shared rollup manager `0x6c6c009cC348976dB4A908c92B24433d4F6edA43` (rollupChainID 20202, rollupTypeID 2, sovereign rollup `0x5D1A491A416feEbf8C123A558ec28A239960bd0E`).
2. **Real loader defect found + fixed.** Non-primary L2 networks were being dialed via the **aggkit node RPC** (`l2.AggsenderRPCURL`, e.g. :12576), which does NOT serve `eth_*` — so `CheckEnv` failed for 002 with `the method eth_chainId does not exist`. Fix: added **`L2Config.OpGethRPCURL`** (populated from `networks.l2_networks.<key>.services.op-geth.http_rpc.external` in `loader.go`) and made `clientForNetwork` (in `checks.go`) prefer it for non-primary networks, falling back to `AggsenderRPCURL` only if empty. Parsing/wiring-only, no test logic. The primary network still reuses `e.Clients.L2`, so single-chain envs (op-pp / op-fep) are unaffected. This is a key N-network loader refinement that P10 must reuse.
3. **OP-PP is real op-geth + native ETH (NOT FEP).** No op-succinct / proposer / committee, no `settled:false` limitation — both chains genuinely boot, load, and check (no settlement carve-out needed). `NativeGas:true`, so MintableERC20 was auto-deployed on both chains. (The live EL is op-reth `v2.2.5` reconciled to the `op-geth` logical key per inherited P3/P7 convention, same as op-fep / op-fep-committee.) The P8 L1-geth archive graceful-flush fix mattered for reliable L1 restore from the snapshot.

## Changed files
**aggkit worktree** (`/home/aigent/repos/agglayer/aggkit-envs`, branch `feat/e2e-envs-integration`, committed at `83b4abbf`):
- `test/e2e/envs/loader.go` — add `L2Config.OpGethRPCURL` field + populate from summary.
- `test/e2e/envs/checks.go` — `clientForNetwork` prefers op-geth RPC for non-primary networks (AggsenderRPCURL retained as fallback).
- `test/e2e/envs/zz_p9_probe_test.go` — NEW generic per-chain LoadEnv + CheckEnv + health probe (no bridging logic).
- `test/e2e/envs/README.md` — `## op-pp-2chains` provenance entry.
- `test/e2e/envs/op-pp-2chains/**` — NEW: `docker-compose.yml`, `summary.json`, `config/{001,002,agglayer}/*`. Data dirs (`aggkit-001-data` / `aggkit-002-data`) gitignored, recreated by the loader at runtime.

**kurtosis-cdk** (`/home/aigent/repos/0xPolygon/kurtosis-cdk`): NONE. HEAD unchanged at `d71f4265`; snapshot output is an uncommitted artifact.

## Commands run
- Split multi-doc preset (python3 `yaml.safe_load_all`, equivalent to `yq 'select(documents()==N)'` — `yq` not installed).
- Multi-doc kurtosis bring-up into ONE enclave: `kurtosis run --enclave op-pp-2chains --args-file /tmp/op-pp-1.yml .` (chain 001) then `... /tmp/op-pp-2.yml .` (chain 002). `kurtosis enclave inspect op-pp-2chains`.
- `./snapshot/snapshot.sh op-pp-2chains --tag op-pp-2chains` → `snapshots/op-pp-2chains-20260529-220608/`.
- Integrate snapshot into `test/e2e/envs/op-pp-2chains/`; strip non-genesis `.bak` files to match op-pp tracked layout.
- `docker compose up -d` / `docker compose ps` / `docker compose down -v --remove-orphans`; `kurtosis enclave rm -f op-pp-2chains`.
- `E2E_ENV=op-pp-2chains go test ./test/e2e/envs/ -run TestP9_OpPP2Chains_LoadAndProbe -v -count=1` → PASS (~80s); plus per-chain probes.
- `curl` eth_chainId / eth_blockNumber / eth_getBalance against L1 and both L2s (independent of the Go harness).
- `make build`; `golangci-lint run ./test/e2e/...`; `go vet ./test/e2e/envs/`.
- `git add ... ; git commit`.

## Blockers
None.

NOTE: the bundled kurtosis-cdk `verify.sh` reported FAILED, but ONLY due to a docker-py version bug in the verifier itself (`'UnixHTTPConnection' object has no attribute 'is_closed'`), NOT a snapshot defect. Authoritative bootability was proven directly via `docker compose up -d` (10/10 services healthy, both L2s restored and advancing) and independently re-confirmed by validation. This is a host-tooling issue — flag for P12 if `verify.sh` is part of the PR story.

Minor deviations (none blocking): used python3 instead of `yq` for the multi-doc split; the chain-002 `kurtosis run` was issued twice (idempotent, into the same enclave) before `aggkit-002` finished deploying — expected behavior of the sequential-into-one-enclave model.

## Future-step updates
- **P10 (cdk-erigon-3chains):** builds directly on this N-network multi-chain path — it must expose 3 L2s and should reuse `L2Config.OpGethRPCURL` / `clientForNetwork` per-network dialing. NOTE: cdk-erigon's RPC service name differs from op-geth, so the loader must wire the erigon RPC per network (not the op-geth logical key). The multi-doc-into-one-enclave bring-up model applies; expect the chain-00N `kurtosis run` may need a second idempotent invocation before each aggkit-00N is RUNNING — re-run until all are up before snapshotting. cdk-erigon is `NativeGas:false`, so the MintableERC20 path is skipped (already handled). Use distinct port bands (e.g. 13xxx for 003), which the snapshot tool assigns automatically. Any future per-network eth client must use `clientForNetwork(ctx, l2, primary)` or `l2.OpGethRPCURL`, NOT `l2.AggsenderRPCURL`.
- **P11 (CI matrix):** add `op-pp-2chains` to the CI matrix (reserved `# P11:` markers in `checks.go` / `testmain_test.go`) as a both-L2 boot/load/checks smoke. Heavier than single-chain (10 services) but no FEP/settlement caveat. Env boots in ~1 min; LoadEnv+CheckEnv+probe completes in ~80s.
- **P12 (provenance/merge):** pin kurtosis-cdk `d71f4265` for op-pp-2chains provenance (README entry already in place; worktree committed at `83b4abbf`; kurtosis-cdk unchanged). No PR opened yet. Consider noting the `verify.sh` docker-py host bug in the PR story.
