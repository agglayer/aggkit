# P13 Step Log

## Summary

P13 added L1 to L2 Auto Claim e2e coverage in the op-pp environment.

The new e2e tests cover two Auto Claim policy paths. The `allow-all` path bridges from L1 to L2 without manually
claiming, waits for the Auto Claim request to reach confirmed status, and verifies the L2 claim result. The
`api-approve` path waits for manual approval status, approves the request through the Auto Claim API, then waits for
confirmed status and verifies the L2 claim.

The op-pp e2e configuration now keeps Auto Claim disabled by default, adds an opt-in `allow-all` Auto Claim config
fragment, and exposes the Auto Claim API port through docker compose while including the `autoclaim` component in the
aggkit service command.

## Decisions And Deviations

- Kept Auto Claim disabled in the base op-pp aggkit config so existing manual e2e helpers and TestMain cleanup remain
  compatible.
- Added per-test runtime config patching so each Auto Claim e2e can opt into the target policy and restore the disabled
  config during cleanup.
- Added the `api-approve` e2e path because the API could be exposed through the op-pp compose environment on host port
  `11579`.
- The prompt referenced `test/e2e/e2e_tests.md`, but that file was not present in the worktree. The worker used the
  existing `test/e2e/README.md`, e2e tests, and op-pp config files instead.
- Full e2e execution could not complete in this host environment because the process was killed during docker compose
  startup after rebuilding `aggkit:local`.

## Final Validation

- Final outcome: THUMBS_UP
- Change-request count: 0
- Validator summary: THUMBS_UP
- Failed acceptance criteria: none
- Requested changes: none

## Changed Files

- `test/e2e/autoclaim_test.go`
- `test/e2e/envs/op-pp/config/001/aggkit-config.toml`
- `test/e2e/envs/op-pp/config/001/autoclaim-allow-all.toml`
- `test/e2e/envs/op-pp/docker-compose.yml`

## Commands Run

Worker implementation commands:

- `gofmt -w test/e2e/autoclaim_test.go` - passed.
- `go test -run TestAutoClaimL1ToL2 -short ./test/e2e` - passed with
  `ok github.com/agglayer/aggkit/test/e2e 0.008s`.
- `go test -run 'TestLoadConfigWithAutoClaimEnabled|TestAutoClaimDefaultRender' ./config` - passed.
- `docker info --format '{{.ServerVersion}}'` - passed; Docker server version was `29.2.1`.
- `docker image inspect aggkit:local --format '{{.Id}}'` - passed; found existing image
  `sha256:20e04b1905d6112a389fe06a72bd489cae9a736790d20417fd91a4eaf50fab7a`.
- `go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e` - failed before tests ran
  because the stale `aggkit:local` image exited with `unknown component: autoclaim`.
- `docker compose up -d && sleep 20 && docker compose ps && docker compose logs --tail=120 aggkit-001` - failed at
  aggkit service startup and confirmed the stale image reported `unknown component: autoclaim`.
- `docker compose down -v --remove-orphans` - passed and removed the partially started op-pp compose environment.
- `make build-docker` - passed and rebuilt `aggkit:local`; final image started with
  `sha256:1d679b23c36585088fb58272c6eb44a40c0875c04a779`.
- `go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e` - attempted again after the
  image rebuild but failed before tests ran because the host killed the test process during `docker compose up` with
  `signal: killed`.
- `docker compose ps && docker compose down -v --remove-orphans` - passed; only `geth` had partially started and compose
  resources were removed.
- `docker compose down -v --remove-orphans && docker compose ps` - passed; final cleanup left no running compose
  services.

Validator commands/evidence:

- Reviewed `test/e2e/autoclaim_test.go` for the `allow-all` and `api-approve` e2e paths.
- Reviewed op-pp Auto Claim config and compose wiring.
- Confirmed the worker's focused validation commands passed.
- Confirmed the full e2e command and concrete environment blocker were recorded.

## Validation Evidence

The validator confirmed that:

- `test/e2e/autoclaim_test.go` adds `TestAutoClaimL1ToL2AllowAll` and
  `TestAutoClaimL1ToL2APIApprove`.
- Both tests use `BridgeL1NoClaim`, which sends `BridgeAsset` from L1 to the L2 network and returns without manually
  calling the L2 claim method.
- The `allow-all` path configures `PolicyName = "allow-all"` and waits for `RequestStatusConfirmed`.
- The `api-approve` path configures `PolicyName = "api-approve"`, waits for
  `RequestStatusManualApprovalRequired`, posts to `/autoclaim/v1/bridges/{id}/approve`, then waits for
  `RequestStatusConfirmed`.
- The tests verify a non-nil Auto Claim claim transaction hash, the matching bridge transaction hash,
  `assertClaimedOnL2`, and the destination L2 balance increase.
- `test/e2e/envs/op-pp/config/001/aggkit-config.toml` contains disabled-by-default Auto Claim settings with disabled
  API, no claimers, and disabled L2-to-Lx watchdog.
- `test/e2e/envs/op-pp/config/001/autoclaim-allow-all.toml` provides the opt-in `allow-all` Auto Claim config.
- `test/e2e/envs/op-pp/docker-compose.yml` includes the `autoclaim` component and exposes `11579:5579` for the Auto
  Claim API.
- No L2-to-L1 or L2-to-Lx Auto Claim e2e coverage was added.

## Blockers

Full e2e validation is blocked in this host environment. The exact command attempted was:

```sh
go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e
```

The first attempt failed because the existing `aggkit:local` image was stale and did not include the new `autoclaim`
component. After `make build-docker` rebuilt `aggkit:local`, the same e2e command was attempted again and the host
killed the test process during `docker compose up` with `signal: killed`, before the e2e test bodies ran. The worker
cleaned up the interrupted op-pp compose project afterward.

## Future-Step Updates

- P14 should rerun
  `go test -v -run 'TestAutoClaimL1ToL2(AllowAll|APIApprove)' -timeout 30m ./test/e2e`
  in an environment that can complete op-pp docker compose startup.
- If `aggkit:local` is stale, run `make build-docker` first; the first attempted e2e run showed older images fail with
  `unknown component: autoclaim`.
- The op-pp compose environment was cleaned with `docker compose down -v --remove-orphans`; final `docker compose ps`
  returned no running services.
