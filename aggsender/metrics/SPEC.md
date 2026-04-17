# SPEC: aggsender/metrics

## Summary

Publishes the aggsender subsystem's Prometheus telemetry. Exposes a package-level registration entry point plus a set of observation helpers covering certificate lifecycle counts, prover activity, validator errors, multisig outcomes, and duration histograms. The metric names and label keys listed below are a public contract: dashboards and alerts outside this repository depend on them.

## Requirements

- **1.** The package MUST expose a registration entry point that, when invoked, registers all metrics in this SPEC with the process-global Prometheus registry.
- **2.** The registration entry point MUST be safe to call exactly once during process startup; it is not required to be idempotent across multiple calls.
- **3.** Every metric defined by this package MUST be published under the Prometheus namespace `aggsender` (i.e. exported metric names are `aggsender_<name>`).
- **4.** Each observation helper listed in "External interface" MUST update only its named metric and MUST NOT have other side effects beyond logging.
- **5.** Validator-labeled observation helpers MUST set the `aggsender_validator` label to the 0x-prefixed hex string representation of the supplied Ethereum address.
- **6.** Histogram helpers MUST record the caller-supplied value, in the caller-defined unit, into their named histogram; the package MUST NOT convert or scale the value.

## External interface

Metric names below are exported with the `aggsender_` prefix (from namespace `aggsender`).

Counters (no labels):

- `aggsender_number_of_certificates_sent` — incremented once per certificate sent.
- `aggsender_number_of_certificates_in_error` — incremented once per certificate transition to an error state.
- `aggsender_number_of_certificates_settled` — incremented once per certificate settlement.
- `aggsender_number_of_prover_errors` — incremented once per prover error.
- `aggsender_multisig_threshold_not_reached` — incremented once per occurrence where the multisig signature threshold was not reached.

Counter vectors (label: `aggsender_validator`, value is the validator's 0x-prefixed address string):

- `aggsender_validator_errors_total` — total errors returned by the validator, keyed by validator.
- `aggsender_validator_invalid_signature_total` — total invalid-signature responses, keyed by validator.

Histograms (default Prometheus buckets; values are seconds as provided by the caller):

- `aggsender_certificate_build_time` — time taken to build a certificate.
- `aggsender_prover_time` — time taken by the prover.
- `aggsender_validate_time` — time taken to validate a certificate.
- `aggsender_certificate_settlement_time` — time taken to settle a certificate.

Observation helpers (exported Go API, behavior-level):

- A registration function that installs all metrics listed above.
- One increment helper per counter/counter-vector metric. Counter-vector helpers take a validator address as input.
- One observation helper per histogram metric, taking a float64 value.

## Out of scope

- Exposing an HTTP scrape endpoint. This package only registers metrics with the shared registry; serving `/metrics` is the responsibility of the process-level Prometheus bootstrap.
- Resetting, deregistering, or mutating bucket boundaries at runtime.
- Deriving timing values. Callers measure elapsed time and pass it in; the package does not start or stop timers.
