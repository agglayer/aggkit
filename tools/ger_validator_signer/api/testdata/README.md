# Golden fixtures of the GER validator REST API

These files define the wire contract between the AggOracle (client, `aggoracle/gervalidator`) and the
GER validator (`tools/ger_validator_signer`, server). Both live in different Go modules that don't import each
other, so the fixtures are what keeps them compatible: the tests of each side read these same files.

- `sign_request.json`: body of `POST /v1/ger/sign`.
- `sign_response.json`: body of the `200` response. `signature` is `r || s || v` (65 bytes) with `v` 27 or 28 and
  `s` in the lower half of the curve order, over `keccak256(ger)` (no EIP-191 prefix), which is what
  `AggOracleCommittee.injectGER` verifies. It was signed with the well known test private key `0x…01`
  (address `0x7e5f4552091a69125d5dfcb7b8c2659029395bdf`), so it is reproducible: signing is deterministic (RFC 6979).
- `error_*.json`: bodies of the error responses. The `code` decides the meaning, not the HTTP status:

| code                      | meaning                                                   | retryable | suggested HTTP status |
| ------------------------- | --------------------------------------------------------- | --------- | --------------------- |
| `INVALID_REQUEST`         | malformed request (bad or zero GER)                       | no        | 400                   |
| `GER_UNKNOWN`             | GER not found in `AgglayerGER.globalExitRootMap` on L1    | no        | 404                   |
| `GER_BLOCK_NOT_CANONICAL` | the L1 block that inserted the GER is not canonical       | no        | 409                   |
| `GER_NOT_FINAL`           | the GER does not meet the validator finality yet          | yes       | 425                   |
| `RATE_LIMITED`            | rate limit reached                                        | yes       | 429                   |
| `INTERNAL`                | internal error                                            | yes       | 500                   |

Hashes, addresses and byte strings are lowercase `0x` hex, as `go-ethereum` encodes them. Parsers must not
depend on the case (an address with EIP-55 checksum is valid).

The fixtures are generated once and edited by hand only if the protocol changes. Do not regenerate them
with a different key or GER: the tests of both modules rely on those values.
