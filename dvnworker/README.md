# dvnworker

`dvnworker` is the AggLayer DVN worker service.  It drives the end-to-end
LayerZero verification workflow: correlate a pending job against an AggLayer
bridge event, wait for certificate settlement and GER injection, build a
`claimAndVerify` proof locally, then submit it to the destination chain.

No remote bridge service (e.g., `bridgeservice` HTTP API) is required.
All proof inputs are read from local `bridgesync` and `l1infotreesync` state
that aggkit already maintains.

## Workflow (section 4.3)

```
dvnsyncer (L1 + L2)
     |
     | ListPendingJobs / GetPacketByHash
     v
correlator — validates packet vs AggLayer bridge event (9 checks, C1-C9)
     |
     | ValidationResult{Accepted: true}
     v
waiter — gates on two conditions:
     1. AggLayer certificate covering bridge leaf is settled on L1
     2. The resulting GER is visible on the destination chain
     |
     v
proofbuilder — builds AggLayerClaim from local bridgesync + l1infotreesync data
     |
     v
submitter — signs and sends claimAndVerify tx to AggLayerDVNCoordinator
             retries up to RetryBudget times; AlreadyProcessed revert = success
```

## Config

```toml
[DVNWorker]
SourceChain             = "l1"     # which chain is the LZ source ("l1" or "l2")
DestinationChain        = "l2"     # which chain is the LZ destination
RPCUrl                  = "https://polygon-mainnet.example.com"
CoordinatorAddr         = "0x..."  # AggLayerDVNCoordinator on destination chain
OFTReceiverAddr         = "0x..."  # AggLayerOFTReceiver on destination chain
SigningKeyPath          = "/app/keystore/dvnworker.keystore"
SettlementPollInterval  = "5s"     # how often waiter re-checks settlement/GER
RetryBudget             = 3        # max claimAndVerify submission retries
```

All address fields must be checksummed hex strings starting with `0x`.
`SigningKeyPath` must point to a standard Ethereum keystore JSON file whose
private key has gas funds on the destination chain.

## Sub-packages

### correlator/

Joins a `dvnsyncer` job (`JobAssignedRecord`) with the matching AggLayer
`Bridge` event and runs all nine §3 checks (C1-C9):

- C1: `packet.Sender` must be the authorized source OFT contract.
- C2: `packet.Receiver` must be the destination OFT custody contract.
- C3: `payloadHash` in the job must equal `payloadHash` in the packet.
- C4: `globalIndex` must decode to a valid (sourceBridgeNetwork, depositCount).
- C5: decoded `depositCount` must match `bridge.DepositCount`.
- C6: decoded `sourceBridgeNetwork` must match `RouteConfig.SourceBridgeNetwork`.
- C7: `amountSD * DecimalConversionRate` must equal `bridge.Amount`.
- C8: OFT recipient (`sendTo`) must NOT equal `bridge.DestinationAddress`
  (the custody contract is not the end recipient).
- C9: `bridge.OriginNetwork`, `bridge.OriginAddress`, `bridge.DestinationNetwork`,
  and `bridge.DestinationAddress` must all match `RouteConfig`.

Returns a `ValidationResult` with `Accepted: true` only when all checks pass.

### waiter/

Blocks a validated job until:

1. `CertificateChecker.IsLeafSettled(networkID, depositCount)` returns true —
   the AggLayer certificate covering the source bridge leaf has been settled on L1.
2. `GERChecker.IsGERInjected(atOrAfterL1InfoTreeIndex)` returns true —
   a Global Exit Root at or after the required L1 info-tree index is visible on
   the destination chain.

Both conditions are polled at `SettlementPollInterval` (default 5 s).

### proofbuilder/

Assembles an `AggLayerClaim` struct (field-for-field mirror of the Solidity
struct) using only local data:

- `BridgeSyncer` — provides the `Bridge` record and its local SMT proof.
- `L1InfoTreer` — provides the current `MainnetExitRoot`, `RollupExitRoot`, and
  the rollup exit tree Merkle proof.

Returns `ErrNotReady` when local syncer data is not yet sufficient; the caller
should retry after a delay rather than treat this as a fatal error.

### submitter/

Signs and submits `claimAndVerify` to `AggLayerDVNCoordinator`:

- Uses `bind.TransactOpts` built from `SigningKeyPath` and destination chain ID.
- Retries up to `maxAttempts` (currently 3) with exponential back-off.
- Treats an `AlreadyProcessed(bytes32)` revert as success (idempotent replay).
- Optionally waits for the transaction receipt via `ReceiptWaiter`.

### bindings/

Generated Go ABI bindings for the AggLayer DVN contracts:

- `agglayerdvn.go` — `AggLayerDVN` contract (source chain, `JobAssigned` event).
- `agglayerdvncoordinator.go` — `AggLayerDVNCoordinator` contract (destination
  chain, `claimAndVerify` method).

Regenerate with:

```sh
make generate-dvn-bindings
```

Do not edit these files manually.

## Running

```sh
aggkit run --components agglayer-dvn-worker --cfg /path/to/config.toml
```

This starts three goroutines: `dvnsyncer` for L1, `dvnsyncer` for L2, and the
`dvnworker` service, all sharing the same context.

See `config.toml.example` for all required configuration fields and the
`dvnsyncer/README.md` for the syncer-specific fields.
