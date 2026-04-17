# SPEC: aggsender/statuschecker

## Summary

The status checker reconciles the AggSender's local view of submitted certificates with the authoritative state held by the AggLayer. It has two concerns: (a) a startup recovery pass that aligns the local store with whatever the AggLayer already knows, retrying until it succeeds or the context is cancelled, and (b) a steady-state polling pass that walks non-settled local certificates, updates their persisted status from the AggLayer, and reports whether pending work or new failures exist.

The core of recovery is a decision over two pairs of observations — (local last certificate, local last settled certificate) vs. (AggLayer latest pending certificate, AggLayer latest settled certificate) — that yields a small action: do nothing, update the local record, or insert a new record from the AggLayer. The status checker does not itself build or resend certificates; it only observes, reconciles, and records.

## Requirements

- **1.** The package MUST expose a certificate status checker constructed from a logger, a local certificate storage, an AggLayer client, a certificate querier, and an L2 origin network identifier.
- **2.** The status checker MUST provide a startup reconciliation operation that repeats the full reconciliation until either it succeeds or the caller-provided context is cancelled, sleeping the caller-provided retry delay between attempts.
- **3.** The startup reconciliation operation MUST record the most recent reconciliation error (or nil on success) into the caller-provided AggsenderStatus before each retry decision.
- **4.** The startup reconciliation operation MUST return as soon as one reconciliation attempt completes without error, and MUST return when the context is cancelled regardless of outcome.
- **5.** The status checker MUST provide a periodic status operation that runs one full reconciliation pass and returns both the error of the AggLayer-recovery step and a certificate-status summary derived from the pending-certificates scan.
- **6.** The pending-certificates scan MUST load every local certificate whose status is non-settled, ask the AggLayer for each one's current header, and persist any status change back to local storage.
- **7.** Persisting a status change MUST update the certificate's status and its updated-at timestamp to the current wall-clock UTC seconds.
- **8.** When a certificate transitions to Settled, the scan MUST emit the Settled metric, and if the local creation time is non-zero it MUST also emit the settlement-time metric in seconds.
- **9.** When a certificate transitions to InError, the scan MUST emit the InError metric.
- **10.** The pending scan MUST report `ExistPendingCerts=true` whenever any scanned local certificate is not in a closed state after reconciliation, or whenever any error is encountered loading or updating a certificate during the scan.
- **11.** The pending scan MUST report `ExistNewInErrorCert=true` if any scanned certificate transitioned from a non-error status locally to an InError status on the AggLayer during this pass.
- **12.** If the pending scan finds zero non-settled certificates and detected no new-InError transition, it MUST additionally query for certificates currently in the InError status, and MUST report `ExistNewInErrorCert=true` when at least one such certificate exists.
- **13.** Errors loading the secondary InError-only list MUST NOT cause the overall scan to fail; the checker MUST log and return the already-computed status.
- **14.** The AggLayer-recovery step MUST fetch, for the configured L2 origin network, the latest settled and latest pending certificate headers from the AggLayer, plus the last-sent and last-settled certificate headers from local storage, and make a single decision from those four inputs.
- **15.** The AggLayer-recovery step MUST refuse to proceed with an "AggLayer inconsistency" error when the AggLayer reports a pending certificate with non-zero height in a non-error status while reporting no settled certificate.
- **16.** The AggLayer-recovery step MUST refuse to proceed with an "AggLayer inconsistency" error when the AggLayer reports pending and settled certificates at the same height where the settled one is not InError.
- **17.** The AggLayer-recovery step MUST refuse to proceed with an "AggLayer inconsistency" error when the AggLayer reports a settled certificate at a higher height than its pending certificate while the settled one is not InError.
- **18.** The AggLayer-recovery step MUST refuse to proceed when local storage has a last certificate but the AggLayer reports no certificate.
- **19.** The AggLayer-recovery step MUST refuse to proceed when the AggLayer's latest certificate height is strictly less than the local last certificate height.
- **20.** The AggLayer-recovery step MUST refuse to proceed when local and AggLayer agree on height but disagree on certificate ID.
- **21.** The AggLayer-recovery step MUST refuse to proceed when local storage holds a settled certificate but the AggLayer holds none, or when local and AggLayer hold settled certificates at the same height with different IDs, or when the local settled height exceeds the AggLayer settled height.
- **22.** When neither local storage nor the AggLayer has any certificate, the recovery step MUST take no storage action.
- **23.** When local storage is empty and the AggLayer has a certificate (settled or non-error pending), the recovery step MUST insert that certificate into local storage, unless it is an open (non-error, non-settled) pending certificate with height > 0, in which case it MUST return an error so the caller will retry later, or unless it is an InError certificate, in which case it MUST NOT persist anything and MUST NOT error.
- **24.** When the AggLayer's latest certificate is exactly one height above the local last certificate, the recovery step MUST insert it into local storage.
- **25.** When local and AggLayer agree on the latest certificate (same ID at same height), the recovery step MUST update the local record's status from the AggLayer header.
- **26.** When the AggLayer holds a settled certificate that local storage lacks, or one at a higher height than the local settled certificate, the recovery step MUST persist it as the local settled certificate.
- **27.** Persisting a settled certificate sourced from the AggLayer MUST mark its source as AggLayer, MUST derive its `ToBlock` from the certificate querier, MUST derive its type from the certificate querier given the derived `ToBlock`, and MUST leave `FromBlock`, `CreatedAt`, and `UpdatedAt` zero because they are not reconstructible from the AggLayer header.
- **28.** Persisting a settled certificate sourced from the AggLayer MUST copy the AggLayer header's previous-local-exit-root when present.
- **29.** A reopen transition (local closed status changing to an AggLayer open status) MUST be logged as a warning but MUST still be applied.

## Invariants

- **30.** After a successful reconciliation pass, for every local certificate scanned, its persisted status equals the status returned by the AggLayer for that certificate at the time it was read.
- **31.** The checker MUST NOT expose a partially-updated view: if the AggLayer-recovery step returns an error, no local settled certificate is created for that pass.

## External interface

- Constructor: takes `(*log.Logger, db.AggSenderStorage, agglayer.AgglayerClientInterface, types.CertificateQuerier, uint32)` and returns a `types.CertificateStatusChecker`.
- The returned value MUST implement `types.CertificateStatusChecker` (`CheckPeriodicallyStatus(ctx, logFn) (CertStatus, error)` and `CheckInitialStatus(ctx, delay, *AggsenderStatus)`) as defined in `aggsender/types/interfaces.go`.
- Exported sentinel: `ErrAgglayerInconsistence`, used to flag the AggLayer-consistency failures in #15–#17 so callers can distinguish them.

## Error modes

- **32.** AggLayer-inconsistency errors (claims #15–#17) MUST wrap `ErrAgglayerInconsistence` so callers can match on it.
- **33.** All errors crossing the package boundary MUST be annotated with enough context to identify whether they originated in recovery, pending-scan, or status-update.

## Out of scope

- Constructing, signing, or sending new certificates. The status checker only observes and records.
- Decoding block ranges (`FromBlock`) or creation timestamps for AggLayer-sourced certificates; these are explicitly not reconstructed.
- Driving the periodic schedule itself: the caller chooses when to invoke `CheckPeriodicallyStatus`.
