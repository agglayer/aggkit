# ARCH: aggsender/metrics

## Overview

Single-file package (`metrics.go`). `Register()` assembles static `CounterOpts`, `CounterVecOpts`, and `HistogramOpts` slices — one entry per metric in SPEC's External interface — and hands them to the shared `github.com/agglayer/aggkit/prometheus` helpers (`RegisterCounters`, `RegisterCounterVecs`, `RegisterHistograms`). Each exported observation helper (`CertificateSent`, `ProverTime`, `ValidatorError`, etc.) is a thin wrapper over the shared helpers' `CounterInc` / `CounterVecInc` / `HistogramObserve`, keyed by the metric name constants at the top of the file. Upholds SPEC #1–#6.

All metric names, the namespace, and the validator label key are declared as package-level `const` strings and reused by both the registration block and the observation helpers, so a rename of a constant propagates atomically to registration and observation.

## Patterns

- **1.** Metric names, the namespace, and label keys SHOULD remain package-level `const` strings referenced by both registration and observation helpers; inlining a string literal in only one site would risk registering one name and observing another.
- **2.** New metrics SHOULD follow the existing shape: add a constant for the name, add an entry in the appropriate slice inside `Register()`, and add a single-line exported helper that forwards to the shared `prometheus` package.
- **3.** Observation helpers SHOULD stay side-effect-free beyond the one metric update they wrap; callers own timing, error classification, and conditional logic.

## Notable decisions

- **4.** Histograms use `prometheusClient.DefBuckets` rather than custom bucket boundaries. This is deliberate for now — tuning buckets is a dashboard-driven change and should be done only with evidence from production scrapes.
- **5.** Validator-scoped metrics use a `CounterVec` with the validator address string as the label value rather than one counter per validator. This keeps cardinality bounded by the active validator set and lets dashboards aggregate with PromQL `sum by (aggsender_validator)`.
- **6.** `Register()` is not guarded against double-invocation; the underlying Prometheus client panics on duplicate registration, which is the desired fail-fast behavior for a misconfigured process boot.
