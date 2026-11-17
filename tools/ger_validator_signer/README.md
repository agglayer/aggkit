# ger_validator_signer

A GER validator of the AggOracleCommittee v2: it signs the Global Exit Roots that are in the `AgglayerGER` contract of
L1 and are final, so the AggOracle of a chain can inject them on the L2 with the signatures of a threshold of
validators.

The documentation is in [`docs/gervalidator.md`](../../docs/gervalidator.md).

## It is its own Go module

`tools/ger_validator_signer` has its own `go.mod`, its own `Dockerfile` and its own image. It **doesn't import aggkit**,
and aggkit doesn't import it, to keep the dependencies small and the code easy to audit. Because of that:

- `go build ./...` from the root of the repository doesn't see it. Use the `*-ger_validator_signer` targets of the
  root `Makefile`, or work from this directory.
- The AggOracle client duplicates the few JSON types of the REST API (`aggoracle/gervalidator`). The golden fixtures in
  [`api/testdata`](api/testdata/README.md) are what keeps both sides compatible: the tests of both read them.
- Mocks are generated with mockery from this directory (`.mockery.yaml`).
- To work on it with an IDE from the root of the repository, create a `go.work` (it is ignored by git):
  `go work init . ./tools/ger_validator_signer`. Mind that, with a `go.work`, `go ./...` from the root includes both
  modules and resolves their dependencies together, which is not what CI does: remove it (or use `GOWORK=off`) before
  running the root targets.

## Layout

| Package | What it does |
|---|---|
| `cmd` | The command: `run --cfg <file>`, `client <info\|health\|sign>`, `version` |
| `app` | Puts it together and checks L1 at startup |
| `api` | REST API, swagger, rate limit |
| `client` | Client of the REST API, to call a validator by hand (the `client` command). It verifies the signatures |
| `service` | Decides if a GER can be signed, signs it and caches the answers |
| `l1` | Minimal JSON-RPC client of L1 |
| `finality` | The rule that decides if a block is final |
| `signer` | Signs `keccak256(GER)` with go_signer |
| `cache` | Answers by GER, with a TTL for the negative ones |
| `config` | Strict TOML configuration |
| `version` | Build information |

## Quick start

```bash
cp config.toml.example config.toml   # edit it
make -C ../.. build-ger_validator_signer
../../target/ger_validator_signer run --cfg config.toml

# From another terminal: call it (see "Calling a validator by hand" in docs/gervalidator.md)
../../target/ger_validator_signer client info --url http://localhost:8080
```
