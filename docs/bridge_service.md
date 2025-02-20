# Bridge service component

The bridge service abstracts interaction with the unified LxLy bridge. It represents decentralized indexer, that sequences the bridge data. Each bridge service sequences L1 network and a dedicated L2 one (which is uniquely defined by the network id parameter). It is implemented as a JSON RPC service.

## Bridge flow

The diagram below describes the basic L2 -> L2 bridge workflow. Note that L2 networks consist of the aggkit node and execution client.

```mermaid
sequenceDiagram
User->>L2 (A): Send bridge tx to L2 (B)
L2 (A)->>L2 (A): Index bridge tx
L2 (A)->>AggLayer: Send certificate
AggLayer->>L1: Settle batch
AggLayer-->>L2 (A): L1 tx hash
L2 (A)->>L2 (A): Add relation bridge : included in L1InfoTree index X
User->>L2 (A): Poll is bridge ready for claim
L2 (A)-->>User: L1InfoTree index X
User->>L2 (B): Get first GER injected that happened at or after L1InfoTree index X
L2 (B)-->>User: GER Y
User->>L2 (A): Build proof for bridge using GER Y
L2 (A)-->>User: Proof
User->>L2 (B): Claim (proof)
L2 (B)->>L2 (B): Send claim tx<br/>(bridge is settled on the L2 (B))
L2 (B)-->>User: Tx hash
```

## Endpoints
This paragraph explains a set of endpoints, that are providing indexed bridge data.

### Get bridges
<!-- TODO: @temaniarpit27 -->

### Get claims
<!-- TODO: @rachit77 -->

### Get token mappings
<!-- TODO: @Stefan-Ethernal -->

### L1 info tree index for bridge

### Injected L1 tree info after index

### Get proof