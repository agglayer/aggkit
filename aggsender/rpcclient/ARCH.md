# ARCH: aggsender/rpcclient

## Overview

The package is a single `Client` struct wrapping a target URL, with one method per remote endpoint. Every method follows an identical three-step shape: invoke a package-level `jSONRPCCall` function variable with the URL, remote method name, and any parameters; check the transport error and the response's `Error` field; JSON-unmarshal `response.Result` into the documented return type. The package-level `jSONRPCCall` defaults to `github.com/0xPolygon/cdk-rpc/rpc.JSONRPCCall` and exists as a variable only so tests can swap it.

Upholds SPEC #1–#5 (constructor + per-method call shape), #6 (each method returns either a decoded result or an error, never both), #7–#10 (the three-branch error handling at the top of every method, with no retry layer).

## Patterns

- **1.** New remote methods SHOULD be added as a new instance method that repeats the same three-step shape (call, protocol-error check with remote-method name embedded in the wrapped message, unmarshal into the typed return). Do not introduce a shared helper that hides the remote-method identifier — SPEC #8 requires it to appear verbatim in the error message for caller-side disambiguation.
- **2.** Transport invocation MUST continue to go through the package-level `jSONRPCCall` variable, not a direct call to `rpc.JSONRPCCall`, so the unit tests in `client_test.go` can stub it without a network double.

## Notable decisions

- **3.** The client is intentionally context-free and retry-free. Callers that need cancellation, deadlines, or retry policy are expected to implement that in their own layer. Adding a transparent retry here would change the observable behavior contract (SPEC #10) for every existing caller.
- **4.** A nil `*uint64` height is forwarded as-is to the JSON-RPC layer so the server's "latest certificate" selection remains reachable. Replacing this with a value type would silently remove that capability.

## Dependencies

- `github.com/0xPolygon/cdk-rpc/rpc` — provides `JSONRPCCall` and the `Response`/`ErrorObject` shapes the client decodes against. The choice is load-bearing: `Response.Result` being a `json.RawMessage` is what lets each method unmarshal into its own typed shape without an intermediate decode.
