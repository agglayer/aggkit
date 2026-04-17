# SPEC: aggsender

## Summary

The `aggsender` subsystem is the in-process agent that turns L2 bridge/claim activity into agglayer certificates and ships them to the agglayer. It owns the lifecycle of a locally-proposed certificate from block-range selection through proof/signature assembly, optional multisig validation, submission, status reconciliation with the agglayer, and persistence. Surrounding the core proposer there is also a validator role (second-opinion signer reachable over gRPC) and a standalone aggchain-proof generation tool that reuses the same proof pipeline without certificate submission.

This directory is intermediate-level. It contains a top-level `AggSender` struct that composes the subsystems under `aggsender/*`, plus an `AggsenderValidator` struct and a `validatorPoller` that sits between the proposer and the remote validator committee. Child subsystems own their own behaviour; this level defines the composition contract: what roles exist, which subsystems they wire together, the cross-child invariants the composition relies on, and the startup/steady-state/shutdown shape of the proposer's main loop.

The three roles the directory exposes are:

- **Proposer (`AggSender`)** — builds certificates, collects multisig signatures from the committee, submits to the agglayer, reconciles status, persists state, and serves the local RPC namespace.
- **Validator (`AggsenderValidator`)** — hosts the remote-validation gRPC service a committee member runs; its in-process logic delegates to `aggsender/validator`.
- **Standalone prover tool (`aggsender/prover/*`, not re-composed here)** — reuses the flow pipeline under a JSON-RPC facade; its composition rules are local to that child.

## Requirements

- **1.** Construction of the proposer MUST resolve the aggsender operating mode before any subsystem is wired: when the configured mode is `Auto`, construction MUST delegate to the multisig-committee querier's contract-backed resolution (see `aggsender/query/SPEC.md#51`) and MUST treat the returned mode as authoritative for every subsequent subsystem choice.
- **2.** Construction MUST fail, without returning a usable proposer, if any of the subsystems it composes fails to construct: the trigger, the storage, the aggchain-FEP querier, the flow manager (builder + local verifier), the L1 info tree querier, or the status checker. Errors from sub-constructors MUST be surfaced with a prefix identifying the failing subsystem so startup logs attribute the failure.
- **3.** The proposer MUST compose exactly one flow manager selected from the set of modes supported by `aggsender/flows/SPEC.md#1`; it MUST NOT switch flows at runtime.
- **4.** The proposer MUST compose exactly one certificate-send trigger selected from the set of modes supported by `aggsender/trigger/SPEC.md#1`; it MUST NOT switch trigger strategies at runtime.
- **5.** The proposer's startup sequence MUST execute, in this order and before entering the steady-state loop: (a) storage compatibility check, (b) priming of the claim syncer's next-required block from the latest settled certificate, (c) initial reconciliation of local storage with the agglayer, (d) flow-manager startup sanity check, (e) trigger-subsystem setup. A failure in (a), (c) via persistent non-cancellation exit, (d), or a panic inside (b) MUST stop the proposer.
- **6.** The proposer's steady-state loop MUST act on three concurrent sources: a periodic status-check ticker, a trigger event channel, and the parent context's cancellation. On context cancellation the loop MUST exit.
- **7.** On each periodic status-check tick, the proposer MUST invoke the status checker's periodic-status operation (see `aggsender/statuschecker/SPEC.md#5`) and MUST update its last-error field with the result.
- **8.** The proposer MUST NOT attempt to build a new certificate while the status-check result reports pending certificates; it MUST skip the build cycle in that case without raising an error.
- **9.** When the status-check result reports no pending certificates but an `InError` certificate exists, the proposer MUST either resend immediately (if retry-on-in-error is configured) or skip and log. If retry-on-in-error is not configured, the proposer MUST NOT resend, MUST NOT raise an error, and MUST log the skipped state.
- **10.** On a trigger event with no pending certificates, the proposer MUST invoke the build-and-send pipeline under the configured retry handler (see `aggsender/config/SPEC.md#5`). A retry pipeline that exhausts its budget MUST surface the final error into the last-error field and MUST NOT crash the loop.
- **11.** The build-and-send pipeline MUST execute, in order and per attempt: obtain build params from the flow manager, build the certificate, invoke the local validator (warn-only on failure), poll the validator committee for a multisig, stamp the multisig onto the certificate via the flow manager, submit to the agglayer, and persist the sent certificate. Failure at any step except the local validator step MUST abort the attempt with a wrapped error.
- **12.** A failed local-validator check during build-and-send MUST be logged as a warning and MUST NOT abort the attempt; the attempt MUST proceed to committee polling and submission.
- **13.** If the flow manager reports "nothing to send" (nil build params and nil error), the build-and-send pipeline MUST return without error and MUST NOT submit anything to the agglayer.
- **14.** When the flow manager reports `ErrComplete` (all certificates permitted by the configured maximum have been sent, see `aggsender/flows/SPEC.md#39`), the pipeline MUST stop retrying that error and, if the configured "stop on finished" flag is set, the proposer MUST terminate the process; otherwise it MUST continue running but cease attempting to send.
- **15.** Multisig collection MUST be delegated to the validator poller. The poller MUST fetch the latest committee via `aggsender/query/SPEC.md#47`, MUST construct one remote-validator client per committee signer (using the shared gRPC client config with the signer's URL override), MUST require that the first committee signer equals the configured proposer's public address, MUST run all validator calls concurrently, and MUST return a multisig as soon as the committee threshold is reached or surface a threshold-not-reached error otherwise.
- **16.** The validator poller MUST self-sign as the proposer when the matched committee member's address equals the proposer's public address; otherwise it MUST delegate to a remote validator per `aggsender/validator/SPEC.md#16`. Signatures from validators whose length differs from the configured signature size MUST be rejected.
- **17.** On a dry-run configuration, the build-and-send pipeline MUST complete assembly and validation through multisig stamping but MUST NOT submit to the agglayer and MUST NOT persist the certificate.
- **18.** When the agglayer rejects a submission, the proposer MUST persist a non-accepted-certificate record (see `aggsender/db/SPEC.md#20`) carrying the originally built certificate and the error string before surfacing the error; failure to persist the non-accepted record MUST be logged but MUST NOT suppress the underlying submission error.
- **19.** On successful submission, the proposer MUST persist the certificate to local storage via `aggsender/db/SPEC.md#2`, retrying under the configured `MaxRetriesStoreCertificate` policy (zero meaning infinite retries); a persistent storage failure MUST surface an error rather than proceed silently.
- **20.** The proposer MUST expose its runtime state via an info snapshot that composes (a) its internal aggsender status, (b) the current trigger subsystem status, (c) the resolved L2 origin network id, and (d) the resolved operating mode. The snapshot MUST be computable without blocking the main loop.
- **21.** The proposer MUST expose a force-trigger operation that delegates unconditionally to the trigger subsystem's force-trigger emission (`aggsender/trigger/SPEC.md#8`).
- **22.** When RPC is enabled, the proposer MUST expose exactly one JSON-RPC service named `aggsender` whose handler is the surface defined by `aggsender/rpc/SPEC.md`; when disabled, it MUST expose no services under that namespace.
- **23.** Storage compatibility MUST be enforced against a runtime-data record that identifies this DB by L2 origin network; a mismatch MUST panic at startup so a mis-wired storage is fatal (unless the operator sets the bypass flag, see `aggsender/config/SPEC.md` `RequireStorageContentCompatibility`).
- **24.** The validator role's construction MUST produce a gRPC server hosting the validator service defined by `aggsender/validator/SPEC.md`, with the flow-verifier, L1-info-tree querier, certificate querier, and aggchain-FEP querier composed identically to the proposer's verifier path; the validator MUST share no mutable state with a proposer instance.
- **25.** The validator role's startup MUST prime the claim syncer's next-required block exactly once (single attempt with the configured retry delay); a persistent failure MUST panic because the validator cannot serve correct decisions without a primed claim syncer.
- **26.** The validator role's in-process `ValidateCertificate` operation MUST delegate to the same `CertificateValidator` instance that the gRPC service uses, so in-process and remote validations produce identical verdicts for the same inputs.

## Invariants

- **27.** The `AggSender.Start` function MUST NOT return before the parent context is cancelled under normal operation; any early return other than a `StopOnFinishedSendingAllCertificates` panic is a bug in the main loop.
- **28.** Across the whole subsystem, the set of subsystems that perform L2 claim-syncer reads (flow manager, certificate querier, validator, prover tool) MUST see the claim syncer primed at a block that is less than or equal to the earliest settled block across the three settlement sources tracked by `aggsender/query/SPEC.md#66`.
- **29.** Mode resolution MUST be consistent across subsystems: every subsystem that reads `cfg.Mode` MUST observe the same resolved mode that was written back to `cfg.Mode` by requirement #1.
- **30.** Multisig signatures collected by the validator poller are anchored to the proposer's public address being the first committee signer; if that anchor is violated the poller MUST NOT produce a partial multisig.

## External interface

Exported Go surface of the top-level package (load-bearing for `cmd/` wiring and the aggsender server bootstrap):

- `New(ctx, logger, cfg, aggLayerClient, l1InfoTreeSyncer, l2Syncer, l2ClaimSyncer, l1Client, l2Client, rollupDataQuerier, committeeQuerier, initialLER) (*AggSender, error)` — proposer constructor.
- `(*AggSender).Start(ctx)` — starts the proposer; blocks until ctx cancellation.
- `(*AggSender).Info() AggsenderInfo` — runtime info snapshot per requirement #20.
- `(*AggSender).ForceTriggerCertificate()` — force-trigger per requirement #21.
- `(*AggSender).GetRPCServices() []jRPC.Service` — RPC registration hook per requirement #22.
- `NewAggsenderValidator(ctx, logger, cfg, l2ClaimSyncer, flow, l1InfoTreeDataQuerier, aggLayerClient, certQuerier, aggchainFEPQuerier, initialLER, signer, initialBlockClaimSyncerSetter) (*AggsenderValidator, error)` — validator-role constructor.
- `(*AggsenderValidator).Start(ctx)` / `(*AggsenderValidator).ValidateCertificate(ctx, VerifyIncomingRequest) error`.
- `NewValidatorPoller(log, storage, proposerSigner, multisigQuerier, validatorClientCfg) *validatorPoller` — committee polling, consumed by the proposer.
- `RateLimiter` interface — transport-layer surface required by RPC consumers.

Configuration: the whole of `aggsender/config/SPEC.md` External interface is the public key surface for operator-facing configuration. This level imposes no additional configuration beyond what its children own.

Package import rule: the top-level package is the only site that may compose `aggsender/flows`, `aggsender/trigger`, `aggsender/statuschecker`, `aggsender/rpc`, and the `aggsender/validator` server; subsystem children MUST NOT import each other except as documented by their own SPECs (types, db, config, converters, query, and metrics are shared leaf dependencies).

## Error modes

- **31.** Errors crossing `AggSender.New`, `NewAggsenderValidator`, and `AggSender.Start`'s sub-steps MUST be wrapped with a prefix identifying the failing step ("error creating runner", "error creating flow manager", "error checking flow Initial Status", etc.) so failures route to the correct subsystem child.
- **32.** The proposer's main loop MUST NOT propagate transient per-iteration errors upward; it MUST record them on the aggsender status's last-error field and continue. The only exit paths are context cancellation and the `StopOnFinishedSendingAllCertificates` panic.
- **33.** A storage compatibility failure during startup MUST be a panic, not a returned error, because the proposer cannot safely operate against an incompatible DB.

## Out of scope

- Certificate construction rules, block-range selection, and proof assembly — owned by `aggsender/flows/SPEC.md` and `aggsender/query/SPEC.md`.
- Certificate persistence semantics, retention policy, and file spill — owned by `aggsender/db/SPEC.md`.
- Configuration decoding, validation, and rendering — owned by `aggsender/config/SPEC.md`.
- The exact JSON-RPC surface semantics, the validator gRPC surface, and the rpc client — owned by `aggsender/rpc/SPEC.md`, `aggsender/validator/SPEC.md`, and `aggsender/rpcclient/SPEC.md`.
- Trigger strategy decision — owned by `aggsender/trigger/SPEC.md`.
- Standalone aggchain-proof generation tool — owned by `aggsender/prover/SPEC.md`; the top-level proposer does not compose that tool and vice-versa.
- Generated mocks under `aggsender/mocks/` — `mockery` output for the interfaces declared under `aggsender/types` and siblings; contracts belong with the sources, not the mocks.

## Children

- `aggchainproofclient/` — gRPC adapter to the aggkit prover; see `aggsender/aggchainproofclient/SPEC.md#1`.
- `config/` — configuration surface; see `aggsender/config/SPEC.md#1`.
- `converters/` — pure translations between aggsender-domain and agglayer-wire types; see `aggsender/converters/SPEC.md#1`.
- `db/` — persistent storage for certificates and non-accepted slot; see `aggsender/db/SPEC.md#1`.
- `flows/` — mode-keyed builder / verifier pipelines; see `aggsender/flows/SPEC.md#1`.
- `metrics/` — Prometheus telemetry; see `aggsender/metrics/SPEC.md#1`.
- `optimistic/` — optimistic-mode signature calculator and querier; see `aggsender/optimistic/SPEC.md#1`.
- `prover/` — standalone aggchain-proof JSON-RPC tool; see `aggsender/prover/SPEC.md#1`.
- `query/` — read-side queriers composed by flows and the proposer; see `aggsender/query/SPEC.md#1`.
- `rpc/` — JSON-RPC surface under the `aggsender` namespace; see `aggsender/rpc/SPEC.md#1`.
- `rpcclient/` — Go client for the `aggsender` JSON-RPC namespace; see `aggsender/rpcclient/SPEC.md#1`.
- `statuschecker/` — startup and periodic reconciliation against the agglayer; see `aggsender/statuschecker/SPEC.md#1`.
- `trigger/` — certificate-send trigger strategies; see `aggsender/trigger/SPEC.md#1`.
- `types/` — shared value types and interfaces; see `aggsender/types/SPEC.md#1`.
- `validator/` — in-process + gRPC certificate validator; see `aggsender/validator/SPEC.md#1`.
- `mocks/` — `mockery`-generated test doubles (no contract; see Out of scope).
