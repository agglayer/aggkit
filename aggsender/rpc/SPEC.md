# SPEC: aggsender/rpc

## Summary

Public JSON-RPC surface that external callers use to observe and nudge the aggsender: query its runtime status, ask for the header of a specific certificate (or the latest one), extract the bridge-exit list embedded inside a certificate, and force a certificate creation cycle to run immediately. The endpoints are read-only against aggsender storage except for the trigger endpoint, which is a side-effecting command. All handlers are synchronous request/response with no streaming, no pagination, and no authentication — access control is expected to be enforced by the transport layer that hosts this RPC namespace.

## Requirements

- **1.** The `aggsender_status` method MUST return the aggsender's current runtime info snapshot, unmodified from what the aggsender reports.
- **2.** The `aggsender_triggerCertificate` method MUST request the aggsender to produce a certificate immediately (bypassing its normal scheduler) and MUST return a successful response with a `null` result once the request has been dispatched.
- **3.** The `aggsender_triggerCertificate` method MUST NOT wait for certificate creation to complete before responding.
- **4.** The `aggsender_getCertificateHeaderPerHeight` method, when called with no height parameter, MUST return the certificate corresponding to the most recently sent certificate tracked by the aggsender's storage.
- **5.** The `aggsender_getCertificateHeaderPerHeight` method, when called with a concrete `height`, MUST return the certificate stored at exactly that height.
- **6.** The `aggsender_getCertificateHeaderPerHeight` method MUST return a not-found error when the storage has no certificate matching the resolved selection (latest or explicit height).
- **7.** The `aggsender_getCertificateBridgeExits` method, when called with no height parameter, MUST resolve the target height from the last-sent certificate and then return the bridge exits associated with the certificate at that height.
- **8.** The `aggsender_getCertificateBridgeExits` method, when called with a concrete `height`, MUST return the bridge exits associated with the certificate at exactly that height.
- **9.** The `aggsender_getCertificateBridgeExits` method MUST return a not-found error when the resolved certificate does not exist, has no signed-certificate payload attached, or the signed payload carries no bridge exits.
- **10.** The `aggsender_getCertificateBridgeExits` method MUST return a not-found error for any certificate whose origin is the agglayer recovery path (i.e. the signed certificate is a placeholder rather than the originally produced payload), so consumers cannot mistake a reconstructed header for a full certificate.
- **11.** Every RPC method MUST treat a storage error as a server-side failure distinct from a not-found result and MUST surface the underlying error description to the caller.

## Invariants

- **12.** For any height `h` accepted by the storage, the response of `aggsender_getCertificateHeaderPerHeight` with parameter `h` MUST describe the same certificate record that the storage would return for `h`, regardless of how many times the method is called.
- **13.** Calling `aggsender_getCertificateBridgeExits` with no height and calling it with the height reported by the latest header at that instant MUST yield equivalent results (same exits list, or the same class of not-found).

## External interface

JSON-RPC namespace: `aggsender`.

| Method | Params | Result |
| --- | --- | --- |
| `aggsender_status` | `[]` | Aggsender info object (runtime status snapshot). |
| `aggsender_triggerCertificate` | `[]` | `null` on success. |
| `aggsender_getCertificateHeaderPerHeight` | `[]` or `[height: uint64]` | Certificate record (header plus optional signed payload). |
| `aggsender_getCertificateBridgeExits` | `[]` or `[height: uint64]` | Array of bridge-exit objects as serialized by the agglayer certificate payload. |

Signed-certificate payloads returned embedded in certificate records, and the bridge-exit array returned by `aggsender_getCertificateBridgeExits`, use the agglayer certificate JSON schema; this RPC namespace does not redefine those shapes.

## Error modes

- **14.** Not-found conditions (missing certificate, missing signed payload, missing bridge exits, placeholder/recovered certificate) MUST be reported with the RPC not-found error code.
- **15.** All other failures (storage read error, payload decode error) MUST be reported with the RPC default/server error code, with a message identifying the failing operation and the underlying cause.

## Out of scope

- Transport, routing, authentication, and rate limiting — those belong to the server that mounts this namespace.
- Writing, mutating, or deleting certificates. The only state-changing call is the trigger, and it acts on the aggsender scheduler, not on storage.
- Pagination, filtering, or range queries over certificates. Each read targets a single height (explicit or implicit "latest").
