# Description
This project is to describre the work into aggkit-proxy component

## Generic Bridge states

A bridge (deposit) goes through the following states, from its creation with `bridgeAsset` or `bridgeMessage` (both are supervised) until it is claimed on the destination network:

| State | Description |
| --- | --- |
| Pending to be included in certificate | The bridge has been created (`BridgeEvent` emitted) but is not yet part of any certificate. |
| Certificate: Pending | The bridge is included in a certificate sent to the Agglayer. Covers every status the certificate goes through (Pending, Proven, Candidate, InError) — none of them move the bridge to a different state, they only change the certificate data shown alongside this one, until it settles. |
| WaitL1SettledGER | The certificate has settled, but its settlement tx has not been confirmed on L1 yet: waits for that tx to reach L1 finality and its receipt to carry both `VerifyBatchesTrustedAggregator` and `UpdateL1InfoTree` (`UpdateL1InfoTreeV2` is captured too, if present, but not required). Only visited by L2-originated bridges (L2->L1, L2->L2). |
| WaitingL1InfoLeafAvailable | L2->L1 only: the settlement tx is confirmed on L1, but the destination network's own bridge-service instance has not indexed the resulting L1 info tree leaf yet — its own L1 info tree sync can lag behind the finality this tracker uses for `WaitL1SettledGER` (e.g. it waits for the block to be finalized). See #1823. |
| WaitingClaim | The Global Exit Root that includes the bridge has been injected on the destination network (L1->L2, L2->L2), or its L1 info tree leaf is indexed by the destination's own bridge-service instance (L2->L1), so the bridge is ready to be claimed. |
| Claimed | The bridge has been claimed on the destination network. |

```mermaid
stateDiagram-v2
    state "Waiting GER update" as WaitingGERUpdate
    state "Waiting LER update" as WaitingLERUpdate
    state "Pending to be included in certificate" as PendingInclusion
    state "Certificate: Pending" as CertPending
    state "Wait L1 settled GER" as WaitL1SettledGER
    state "Waiting L1 Info Leaf Available" as WaitingL1InfoLeafAvailable
    state " Waiting GER Injection" as  WaitingGERInjection
    state "WaitingClaim" as WaitingClaim

    [*] --> WaitingGERUpdate: bridgeAsset() / bridgeMessage() (L1->L2)
    [*] --> WaitingLERUpdate: bridgeAsset() / bridgeMessage() (L2->Lx)
    WaitingLERUpdate --> PendingInclusion: LER update (L2->Lx)
    WaitingGERUpdate --> WaitingGERInjection: GER update (L1->L2)
    PendingInclusion --> CertPending: included in a certificate
    CertPending --> CertPending: certificate status change (still not settled)
    CertPending --> WaitL1SettledGER: settled by Agglayer (L2->L1, L2->L2)
    WaitL1SettledGER --> WaitingGERInjection: settlement tx confirmed on L1 (L2->L2)
    WaitL1SettledGER --> WaitingL1InfoLeafAvailable: settlement tx confirmed on L1 (L2->L1)
    WaitingL1InfoLeafAvailable --> WaitingClaim: L1 info tree leaf indexed by destination (L2->L1)
     WaitingGERInjection --> WaitingClaim: GER injected on destination network
    WaitingClaim --> Claimed: claim on destination network
    Claimed --> [*]
```


## L1->L2 Bridge states 
The bridge don't need to be part of certificate to the state is: 


# L1 -> L2 Sequence diagram

```mermaid
sequenceDiagram
    actor User
    participant L1
    participant Aggoracle
    participant L2

    User->>L1: bridgeAsset() / bridgeMessage()
    L1-->>L1: BridgeEvent / GER updated
    Aggoracle->>L1: read GER
    Aggoracle->>L2: inject GER
    User->>L2: claimAsset() / claimMessage()
    L2-->>User: asset received
```

# L2 -> L1 Sequence diagram

```mermaid
sequenceDiagram
    actor User
    participant L2
    participant Aggsender
    participant Agglayer
    participant L1

    User->>L2: bridgeAsset() / bridgeMessage()
    L2-->>L2: BridgeEvent / LER updated
    Aggsender->>L2: read bridge events
    Aggsender->>Aggsender: compose certificate
    Aggsender->>Agglayer: send certificate
    Agglayer->>L1: settle certificate (GER updated)
    User->>L1: claimAsset() / claimMessage()
    L1-->>User: asset received
```

# L2 -> L2 Sequence diagram

```mermaid
sequenceDiagram
    actor User
    participant L2
    participant Aggsender
    participant Agglayer
    participant L1
    participant Aggoracle
    participant L2P as L2'

    User->>L2: bridgeAsset() / bridgeMessage()
    L2-->>L2: BridgeEvent / LER updated
    Aggsender->>L2: read bridge events
    Aggsender->>Aggsender: compose certificate
    Aggsender->>Agglayer: send certificate
    Agglayer->>L1: settle certificate (GER updated)
    Aggoracle->>L1: read GER
    Aggoracle->>L2P: inject GER
    User->>L2P: claimAsset() / claimMessage()
    L2P-->>User: asset received
```

# How obtain status
- User provide TxHash -> `[ TxBlockNumber, GER/GERBlockNumber, bridgeEvent , Type:L1->L2, L2->L1, L2->L2]`
  - Tx BlockNumber 
  - L1: get event `BridgeEvent`
  - Wait event `UpdateL1InfoTree` >=TxBlockNumber -> `GER` (maybe the call to `BridgeAsset`/`BridgeMessage` have `forceUpdateGlobalExitRoot=false`)

- Aggoracle injected[bridgeEvent.depositCount]: 
  - bridge: `GET /bridge/v1/injected-l1-info-leaf?network_id=X&leaf_index=N`

- L2: Certificate Agglayer[LER] -> [ certID, certStatus, SettlementTxHash]
  - from LER we can get the L2BlockNumber (need LER synchronized)
  - agglayer:GetLatestSettledCertificateHeader aka LS
    - LER <= LS.NewLocalExitRoot ? -> [certID, status: settled, SettlementTxHash]
    - LER >  LS.NewLocalExitRoot: GetLatestPendingCertificateHeader aka LP
      - LER <= LP.NewLocalExitRoot: -> [certID, status: LP.status ]
      - LER >  LS.NewLocalExitRoot -> [-, status: not include yet ]

# Data source
- Wait event `UpdateL1InfoTree`: 
- GER we can get the L2BlockNumber: we need a LER synchronized
   - ask to bridge?
   - have the bridgesyncer??


