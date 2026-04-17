# SPEC: aggsender/trigger/types

## Summary

Defines the shared vocabulary used by the trigger subsystem to describe and observe epoch progression. An *epoch* is a bounded window of time (or blocks) during which the aggsender decides when to emit certified batches; consumers subscribe to *epoch events* to react as an epoch approaches its end, and can poll *epoch status* to read the current position within the active epoch. This package contains no production logic — only the concepts, their invariants, and the subscriber-facing interface that every epoch-notifier implementation must honour.

## Requirements

- **1.** An epoch event MUST identify exactly one epoch by a monotonically-assigned non-negative integer.
- **2.** An epoch event MUST carry implementation-defined extra information that is renderable as a human-readable string.
- **3.** An epoch status MUST report the identifier of the currently active epoch alongside the fraction of that epoch that has elapsed, expressed as a value in the closed interval [0, 1].
- **4.** The human-readable rendering of an epoch status MUST express the elapsed fraction as a percentage with two fractional digits.
- **5.** A subscription operation MUST accept a caller-supplied identifier and return a channel that delivers epoch events addressed to that subscriber.
- **6.** A notifier MUST expose a blocking start operation that runs until the supplied context is cancelled.
- **7.** A notifier MUST expose a non-blocking query that returns the current epoch status.
- **8.** A notifier MUST expose an operation that forces immediate publication of the current epoch event to every live subscription, irrespective of where the epoch clock currently sits.
- **9.** A notifier MUST be renderable as a human-readable string describing its current configuration or state.

## Invariants

- **10.** Successive epoch events observed on any single subscription MUST have non-decreasing epoch identifiers.

## External interface

Exported Go surface (package `types`):

- `EpochEvent{Epoch uint64, ExtraInfo fmt.Stringer}` with `String() string`.
- `EpochStatus{Epoch uint64, PercentEpoch float64}` with `String() string`.
- `EpochNotifier` interface:
  - `Subscribe(id string) <-chan EpochEvent`
  - `Start(ctx context.Context)`
  - `GetEpochStatus() EpochStatus`
  - `String() string`
  - `ForcePublishEpochEvent()`

Consumers of this package depend on these names and signatures; changing them breaks every notifier implementation and every subscriber.

## Out of scope

- How epochs are measured (wall-clock, block height, external oracle). Implementations live in the parent `aggsender/trigger` package.
- Delivery semantics of the subscription channel (buffering, drop policy, fan-out fairness). Defined by each implementation.
- Persistence or replay of epoch events across process restarts.
