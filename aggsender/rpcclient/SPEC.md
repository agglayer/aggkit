# SPEC: aggsender/rpcclient

## Summary

A thin Go client that lets in-process callers query an aggsender node's JSON-RPC endpoint. It exposes one Go method per supported remote method, each responsible for issuing the JSON-RPC call, translating transport, protocol-level, and decoding failures into Go errors, and returning strongly-typed result values. It holds no state beyond the target URL and is purely a request/response adapter — no retries, no caching, no concurrency control.

## Requirements

- **1.** Constructing a client MUST take a target URL and MUST produce a reusable client bound to that URL for all subsequent calls.
- **2.** Each call MUST be issued to the configured URL as a JSON-RPC request naming the remote method specified by the External interface for that call.
- **3.** When the remote method accepts a parameter, the client method MUST forward the caller-supplied argument as the JSON-RPC parameter of that call, including forwarding a nil pointer as a JSON null, so the server's "latest" semantics are reachable.
- **4.** On a successful JSON-RPC response, the client MUST JSON-decode the `result` field into the Go type documented for that method and MUST return a non-nil typed value together with a nil error.
- **5.** The client MUST be safe for serial reuse across multiple calls against the same URL.

## Invariants

- **6.** Every exported method MUST return exactly one of: (a) a decoded non-nil result and nil error, or (b) a nil result and a non-nil error. It MUST NOT return a partially-decoded value together with an error.

## External interface

Go package API. All methods are instance methods on the client and perform a single JSON-RPC request per call.

- Constructor: takes a target URL string, returns a client handle.
- Get status: no parameters. Maps to remote method `aggsender_status`. Returns an `AggsenderInfo` value defined in `aggsender/types`.
- Get certificate header at height: takes a `*uint64` height (nil means the server's "latest" selection). Maps to remote method `aggsender_getCertificateHeaderPerHeight`. Returns a `Certificate` value defined in `aggsender/types`.
- Get certificate bridge exits at height: takes a `*uint64` height (nil means the bridge exits of the last sent certificate). Maps to remote method `aggsender_getCertificateBridgeExits`. Returns a slice of `*BridgeExit` values defined in `agglayer/types`.

The remote-method names above are part of the contract: they must match what the aggsender RPC server exposes (see `aggsender/rpc/aggsender_rpc.go`).

## Error modes

- **7.** If the underlying JSON-RPC transport returns an error (e.g., network failure, malformed HTTP response), the client method MUST propagate that error to the caller unchanged in type semantics (a non-nil Go error) and MUST NOT return a decoded result.
- **8.** If the JSON-RPC response carries a protocol-level error object, the client method MUST return a non-nil error whose message identifies the remote method that was called, so callers can distinguish which operation failed when composing multiple calls.
- **9.** If decoding the `result` field into the documented Go type fails, the client method MUST return the decoding error and MUST NOT return a partially-filled result.
- **10.** The client MUST NOT retry, back off, or otherwise hide any of the three failure modes above; each call is a single attempt from the caller's perspective.

## Out of scope

- Authentication, TLS configuration, custom HTTP headers, or connection pooling tuning — the URL is the entire transport configuration.
- Retry, rate limiting, circuit breaking, caching of responses.
- Context-based cancellation or per-call timeouts.
- Concurrency primitives; callers needing parallel requests instantiate their own calls and manage synchronization themselves.
- Server-side semantics of the remote methods — those are governed by the aggsender RPC server's contract, not this client.
