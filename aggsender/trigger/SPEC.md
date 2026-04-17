# SPEC: aggsender/trigger

## Summary

The trigger subsystem decides *when* the aggsender should attempt to build and send a new certificate. A consumer (the aggsender main loop) obtains a `CertificateSendTrigger`, consumes events from a channel, and builds one certificate per event. This package provides three interchangeable trigger strategies — one that fires on every new L2 bridge notification, one driven by agglayer epoch transitions, and one that fires as soon as the previous certificate is settled — plus a factory that picks the correct strategy from configuration.

Two orthogonal concerns live here. The *factory* resolves the configured trigger mode (including an `Auto` mode that depends on the aggsender's operating mode) into a concrete implementation. The *epoch notifier per block* is a reusable component, shared only by the epoch-based strategy, that translates a stream of L1 block-number notifications into epoch-boundary events using the agglayer clock configuration.

All strategies expose the uniform `CertificateSendTrigger` surface defined in `aggsender/types`; the subpackage `aggsender/trigger/types` defines the epoch vocabulary (see `aggsender/trigger/types/SPEC.md`).

## Requirements

- **1.** The trigger factory MUST produce a trigger whose event semantics correspond to the configured trigger mode: a bridge-driven trigger for "new bridge" mode, an epoch-driven trigger for "epoch based" mode, and an ASAP trigger for "ASAP" mode.
- **2.** When the configured trigger mode is "Auto", the factory MUST resolve it to "new bridge" for preconfirmation aggsender mode and to "epoch based" for both pessimistic-proof and aggchain-proof aggsender modes.
- **3.** The factory MUST refuse to construct a trigger when the aggsender mode is itself "Auto" (the unresolved sentinel), and MUST return an error identifying that the aggsender mode must be resolved first.
- **4.** The factory MUST return an error when the configured trigger mode is not one of the three supported concrete modes (or "Auto").
- **5.** The factory MUST propagate any construction-time failure of an underlying notifier or syncer dependency as a wrapped error and MUST NOT return a partially-initialised trigger.
- **6.** Every trigger implementation MUST deliver events through the uniform surface declared by `aggsender/types.CertificateSendTrigger` (`Setup`, `Status`, `TriggerCh`, `ForceTriggerEvent`, `OnIdle`).
- **7.** Every trigger implementation MUST close its event channel when the context passed to `TriggerCh` is cancelled, so the consumer's range loop terminates cleanly.
- **8.** `ForceTriggerEvent` MUST emit exactly one event on the active channel without regard to the trigger's internal scheduling state, for use as an operator override.

### Bridge-driven trigger

- **9.** The bridge-driven trigger MUST emit one event for each new L2 bridge synchronisation notification received from the configured L2 bridge syncer.
- **10.** Each event emitted by the bridge-driven trigger MUST carry the L2 block number of the underlying bridge notification.
- **11.** When `ForceTriggerEvent` is invoked on the bridge-driven trigger and the L2 bridge syncer has a last processed block, the emitted event MUST carry that block number.
- **12.** When `ForceTriggerEvent` is invoked on the bridge-driven trigger before any block has been processed, the trigger MUST log an error and MUST NOT emit an event.

### Epoch-based trigger

- **13.** The epoch-based trigger MUST emit one event per epoch transition, where epoch boundaries are computed from the agglayer clock configuration fetched at construction time (genesis block and epoch duration).
- **14.** The epoch-based trigger MUST allow the operator to configure the fractional position within an epoch at which notification fires, expressed as an integer percentage strictly less than 100.
- **15.** The epoch-based trigger MUST fail construction if it cannot retrieve the agglayer clock configuration.
- **16.** The epoch-based trigger MUST fail construction if the resolved epoch duration is zero or if the notification percentage is not strictly less than 100.
- **17.** The epoch-based trigger MUST report its current epoch and fractional progress through `Status`.
- **18.** The fractional notification threshold MUST be clamped so that notification always fires strictly before the last block of the epoch, even when the configured percentage would round past that block.

### ASAP trigger

- **19.** The ASAP trigger MUST emit one event as soon as the consumer signals (via `OnIdle`) that it is ready to build a new certificate, subject to a configured delay between certificates.
- **20.** The ASAP trigger MUST refuse construction when its configuration requests bridge-event-driven triggering but no L2 bridge syncer is supplied.
- **21.** When configured to subscribe to new L2 bridges, the ASAP trigger MUST emit an event upon receiving a new bridge notification, collapsing concurrent notifications so at most one event is in flight at a time.
- **22.** The ASAP trigger MUST enforce a configurable minimum wall-clock interval between successive emitted events, so that regardless of how fast the consumer calls `OnIdle` the emission rate is bounded.
- **23.** When the configured minimum interval elapses and no other event is already scheduled, the ASAP trigger MUST emit an event unconditionally; this guarantees liveness even in the absence of bridge activity or `OnIdle` invocations.
- **24.** `ForceTriggerEvent` on the ASAP trigger MUST update the internal last-event timestamp so the minimum-interval window restarts from the forced emission.

## Invariants

- **25.** Epoch identifiers delivered by the epoch-based trigger MUST be strictly increasing across the lifetime of a single trigger instance.
- **26.** No trigger implementation MAY retain references to the channel returned by `TriggerCh` after the governing context is cancelled.

## External interface

The package exports:

- `NewCertificateSendTrigger(ctx, cfg, log, l1Client, l2BridgeSync, agglayerClient) (types.CertificateSendTrigger, error)` — factory.
- `ConfigEpochNotifierPerBlock` struct with `Validate()`, `String()`.
- `NewConfigEpochNotifierPerBlock(ctx, agglayerClient, epochNotificationPercentage)` — config loader.
- `EpochNotifierPerBlock` type implementing `aggsender/trigger/types.EpochNotifier` (see `aggsender/trigger/types/SPEC.md#EpochNotifier`), with `NewEpochNotifierPerBlock`, `Start`, `StartAsync`, `GetEpochStatus`, `ForcePublishEpochEvent`, `String`.
- `ExtraInfoEventEpoch` struct used as the `ExtraInfo` payload of epoch events emitted by `EpochNotifierPerBlock`; carries the number of blocks remaining until the next epoch boundary.

Mode constants (`NewBridgeTriggerMode`, `EpochBasedTriggerMode`, `ASAPTriggerMode`, `AutoTriggerMode`) and the `CertificateSendTrigger`, `CertificateTriggerEvent` interfaces are declared in `aggsender/types` and consumed here.

## Error modes

- **27.** Every error returned from this package MUST be wrapped with a phrase identifying the failed step (e.g. which notifier, which config, which resolution), so the aggsender startup log points at the responsible component.
- **28.** A nil `agglayerClient` passed to `NewConfigEpochNotifierPerBlock` MUST produce an error; the function MUST NOT panic.

## Out of scope

- Building, signing, or sending certificates. The trigger only *signals when*; certificate construction lives in `aggsender/flows`.
- Defining the `CertificateTriggerEvent` interface itself (it lives in `aggsender/types`).
- Persisting the last-emitted epoch or event across process restarts.
- Back-pressure on slow consumers: channels are unbuffered and block the trigger until the aggsender reads.

## Children

- `types/` — vocabulary and interface for epoch notifiers; see `aggsender/trigger/types/SPEC.md#5` and `#6`.
