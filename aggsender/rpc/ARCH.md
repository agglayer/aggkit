# ARCH: aggsender/rpc

## Overview

A single struct `AggsenderRPC` holds a logger plus two collaborator interfaces — `AggsenderStorer` (reads certificates by height / latest, also exposes a save used by other packages) and `AggsenderInterface` (runtime info + force-trigger) — and exposes four methods that map one-to-one onto the JSON-RPC surface. Each method is a thin adapter: look up state via the injected collaborator, translate `nil`/error/empty into the appropriate `cdk-rpc` error code, return the value otherwise. There is no caching, no goroutine, no internal state.

Upholds SPEC #1 (Status), #2–#3 (TriggerCertificate — fires and returns), #4–#6, #11, #12 (GetCertificateHeaderPerHeight), #7–#10, #11, #13 (GetCertificateBridgeExits), #14–#15 (error-code mapping across both handlers).

## Patterns

- **1.** New RPC methods SHOULD follow the `(result interface{}, err rpc.Error)` signature from `github.com/0xPolygon/cdk-rpc/rpc` so the surface stays consistent with the rest of the aggkit RPC handlers.
- **2.** Storage-layer access SHOULD go through the `AggsenderStorer` interface defined here; direct coupling to a concrete storage type would bypass the mock surface used by tests and tighten this package's dependency graph unnecessarily.
- **3.** Not-found conditions SHOULD be mapped to `rpc.NotFoundErrorCode` and every other failure to `rpc.DefaultErrorCode`, matching the convention used by sibling RPC packages (e.g. `l1infotreesync`, `claimsync`).

## Notable decisions

- **4.** `GetCertificateBridgeExits` does its own JSON unmarshalling of the stored `SignedCertificate` string into an `agglayertypes.Certificate` rather than asking storage for a typed object. Rationale: the storage record keeps the signed payload as an opaque string (the serialized form sent to / received from agglayer), and the bridge-exits view is the only consumer that needs the decoded structure, so the decode stays local.
- **5.** Certificates whose `Header.CertSource == CertificateSourceAggLayer` are treated as not-found by `GetCertificateBridgeExits` even when a signed-certificate string is present. Rationale: those records are placeholders reconstructed from the agglayer during recovery and do not carry the original signed payload, so returning them would surface meaningless exits to the caller (upholds SPEC #10).
- **6.** `TriggerCertificate` dispatches synchronously to `aggsender.ForceTriggerCertificate` and returns immediately; it deliberately does not block on certificate completion. Rationale: the method is a nudge, not a command-with-result — callers poll via `Status` / `GetCertificateHeaderPerHeight` for the outcome.
