# GER validator signer

`ger_validator_signer` is a **GER validator** of the AggOracleCommittee v2: a service that signs the Global Exit Roots (GER) that are in the `AgglayerGER` contract of L1 and are final. The [AggOracle](./aggoracle.md) of each chain asks the validators to sign a GER, and injects it on the L2 with the signatures of a threshold of them.

It is a separate binary and a separate docker image, and it is **its own Go module** (`tools/ger_validator_signer`, with its own `go.mod`): it doesn't import aggkit, nor the other way round. The goal is to keep its dependencies small and its code easy to audit. It doesn't share anything with the [aggsender validator](./aggsender_validator.md).

A validator doesn't depend on the destination chain: the same validator (the same key) can serve many L2s. It never sends transactions.

## What it signs, and when

It signs `keccak256(GER)`, the hash of the 32 bytes of the GER with no EIP-191 prefix, which is what `AggOracleCommittee.injectGER` verifies. The signature has 65 bytes (`r`, `s`, `v`) with `v` 27 or 28 and `s` in the lower half of the curve order, or the transaction would revert. The signature is verified before it is returned: it must recover the address of the key.

A GER is signed only if all this is true:

1. **It is in L1.** `AgglayerGER.globalExitRootMap(GER)` is not zero. The contract stores there the hash of the block **previous** to the one that inserted the GER (`blockhash(block.number - 1)`).
2. **The block is canonical.** The hash is resolved to its block `N`, and the canonical block of height `N` must have the same hash: a node can return an orphan block when asked by hash.
3. **The block is final.** The insertion block is `N + 1`, and it must not be after the final block: the number of the block of the configured tag (`latest`, `safe` or `finalized`) minus `NumBlocks`. For example `Tag = "latest"` with `NumBlocks = 6` is the head minus 6 blocks, which is the setup AggOracle had before the validators existed.

Each validator decides its own finality. Only block headers are needed: no historical state.

## REST API

The API is documented with swagger. The specification is served by the validator at `/swagger/doc.json` and `/swagger/doc.yaml`, and the source is in [`tools/ger_validator_signer/api/docs`](../tools/ger_validator_signer/api/docs/swagger.yaml).

| Endpoint | Description |
|---|---|
| `POST /v1/ger/sign` | Signs a GER. Body: `{"ger": "0x<32 bytes>"}`. Answer: `{"ger", "signature", "signer"}` |
| `GET /v1/info` | Version, the address that signs (the one to register in the committee), the finality and the chain |
| `GET /health` | The process is alive. It doesn't query L1 nor sign |

The **code** of the body of an error decides what the AggOracle does, not the HTTP status:

| Code | Meaning | Retryable | HTTP |
|---|---|---|---|
| `INVALID_REQUEST` | Malformed request (a bad or zero GER, an unknown field, a body too big) | no | 400, 413 |
| `GER_UNKNOWN` | The GER is not in `AgglayerGER.globalExitRootMap` | no | 404 |
| `GER_BLOCK_NOT_CANONICAL` | The L1 block that inserted the GER is not in the canonical chain | no | 409 |
| `GER_NOT_FINAL` | The block is not final for this validator yet | yes (`Retry-After`) | 425 |
| `RATE_LIMITED` | The rate limit of the client was reached | yes (`Retry-After`) | 429 |
| `INTERNAL` | A failure of the validator, of L1 or of the signer. The details are logged, not sent | yes | 500 |

The wire format is defined by golden fixtures ([`api/testdata`](../tools/ger_validator_signer/api/testdata/README.md)) that the tests of the validator and of the AggOracle client read, so the two modules, that don't import each other, agree on it.

## Configuration

An example with every option is in [`config.toml.example`](../tools/ger_validator_signer/config.toml.example). An unknown key is an error.

- `L1RPCURL`, `L1ChainID`, `AgglayerGERAddr` are mandatory. At startup the validator checks that the endpoint is the configured chain, and that the address is a contract that answers `globalExitRootMap`. If L1 is not as configured it doesn't start. The URL of L1 can have a token: it is never logged, not even its host (also removed from the errors of L1).
- `[Finality]`: `Tag` (`finalized` by default) and `NumBlocks`.
- `[Signer]`: how to sign, with [go_signer](https://github.com/agglayer/go_signer). `Method` is `local` (a keystore, with `Path` and `Password`), `GCP` or `AWS` (a KMS). The method `remote` can't be used: it signs with EIP-155, so it can't sign a hash. The address of the key is the validator address to register in the committee (`GET /v1/info` shows it).
- `[REST]`: `Host`, `Port`, the TLS files, the timeout of a request and the limit of the body.
- `[Cache]` and `[RateLimit]`: see below.

### Cache

A GER that was signed is cached (it is never queried again, nor signed again). A GER that can't be signed (unknown, not canonical, not final) is cached for `NegativeTTL` (5 seconds by default), so a client that insists on it doesn't hit the L1 RPC every time. Concurrent requests for the same GER share one query to L1 and one signature. Failures of L1 or of the signer are never cached.

### Security

- **TLS is required.** Set `REST.TLSCertFile` and `REST.TLSKeyFile` (TLS 1.2 or later). Serving HTTP needs an explicit `InsecureHTTP = true`, for when the TLS is terminated in front of the validator or for tests: it logs a warning.
- **Rate limit per client.** Requests per second and burst of each client, identified by the IP address of the connection. The headers that proxies add (`X-Forwarded-For`) are never used, because any client can send them: behind a reverse proxy every client is the proxy, so limit there. The memory of the limiter is bounded.
- **The request is small and strict.** The body is limited, it must be one JSON object with only the field `ger`, and the response of an internal error says nothing about the cause.
- **A validator signs a GER it checked in L1.** The signature has no chain id nor contract address (it is what lets many chains share the validators), so do not reuse the key of a validator across environments.
- **The signer is checked.** The signature that go_signer returns is normalized and must recover the address of the key before it is returned.

## Running it

```bash
# From the root of the repository
make build-ger_validator_signer                  # ./target/ger_validator_signer
make build-docker-ger_validator_signer           # the docker image ger_validator_signer:local, with no shell

./target/ger_validator_signer run --cfg config.toml
./target/ger_validator_signer version

# With docker (the default command is: run --cfg /etc/ger_validator_signer/config.toml)
docker run --rm -p 8080:8080 \
  -v "$PWD/config.toml:/etc/ger_validator_signer/config.toml:ro" \
  -v "$PWD/validator.keystore:/etc/ger_validator_signer/validator.keystore:ro" \
  -v "$PWD/tls:/etc/ger_validator_signer/tls:ro" \
  ger_validator_signer:local
```

The image runs as a non-root user and, in the production variant, has no shell. The binary needs CGO (the GCP KMS signer uses the C implementation of secp256k1), so it is built on Alpine as aggkit is.

To register the validator in the committee, take its address from `GET /v1/info` (or from the logs at startup) and set it, with the URL of the validator, in the AggOracleCommittee contract.

## Calling a validator by hand

The binary has a client of the REST API, to test a validator that is running: does it answer, what does it say of a GER, is its signature right. The AggOracle doesn't use it (it has its own client, in `aggoracle/gervalidator`).

```bash
export GER_VALIDATOR_URL=http://localhost:8080   # or --url in each command. This is the default

ger_validator_signer client health               # is it running
ger_validator_signer client info                 # version, signer, finality and L1
ger_validator_signer client sign --ger 0x<32 bytes>
ger_validator_signer client sign --ger 0x<32 bytes> --expect-signer 0x<address>

# A burst of requests, to see the rate limit and the cache: how many got each answer, and the latency
ger_validator_signer client sign --ger 0x<32 bytes> --count 300 --parallel 30

# From the docker image (the binary is its entrypoint), if you have no binary
docker run --rm --network host ger_validator_signer:local client info --url http://127.0.0.1:8080
```

- **The signature is always verified**, as the contract does it: 65 bytes, `v` 27 or 28, `s` in the lower half of the curve, over `keccak256(GER)`. It must recover the `signer` of the answer, and with `--expect-signer` that signer must be the address registered in the committee. A validator that answers with a signature that fails is reported as `bad signature`.
- **A GER that is not signed** prints the code of the answer (`GER_UNKNOWN`, `GER_NOT_FINAL`, ...), says if it can be retried, and exits with 1. The last GER of L1 (`cast call <AgglayerGER> "getLastGlobalExitRoot()(bytes32)"`) answers `GER_NOT_FINAL` until its block is final for the validator.
- `--json` prints the result as JSON, for scripts. `--timeout` (20 seconds by default) is the timeout of each request, and `--ca-cert` the PEM file with the CA of a validator that serves TLS with a certificate that is not public.
- Exit code: 0 if everything was signed, 1 if the validator didn't sign or didn't answer (in a burst: if any request wasn't signed), 2 for a wrong usage.

## Development

It is its own module, so `go ./...` from the root of the repository doesn't see it, and it has its own targets:

```bash
make test-unit-ger_validator_signer
make lint-ger_validator_signer
make generate-mocks-ger_validator_signer              # mockery
make generate-swagger-docs-ger_validator_signer       # swag
```

CI runs them in the Lint and Test Unit workflows, checks the image in `Test GER Validator Signer Image`, and the image can be published with `Build and Push GER Validator Signer Image`.
