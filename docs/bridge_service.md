# Bridge service component

The bridge service abstracts interaction with the unified LxLy bridge. It represents decentralized indexer, that sequences the bridge data. Each bridge service sequences L1 network and a dedicated L2 one (which is uniquely defined by the network id parameter). Therefore, each agglayer connected chain runs its own bridge service. It is implemented as a JSON RPC service.

## Bridge flow


### Bridge flow L2 -> L2

The diagram below describes the basic L2 -> L2 bridge workflow.

```mermaid
sequenceDiagram
    participant User
    participant L2 (A)
    participant Aggkit (A)
    participant AggLayer
    participant L2 (B)
    participant Aggkit (B)
    participant L1

    User->>L2 (A): Bridge assets to L2 (B)
    L2 (A)->>L2 (A): Index bridge tx & updates the local exit tree
    Aggkit (A)->>AggLayer: Build & send certificate (Aggsender)
    AggLayer->>L1: Settle batch
    L1->>L1: update GER
    Note right of L1: rollupmanager updates the GER & RER (PolygonZKEVMGlobalExitRootV2.sol)
    AggLayer-->>L2 (A): L1 tx hash

    Aggkit (A)->>L1: Aggoracle fetches last finalized GER from L1
    Aggkit (A)->>L2 (A): Aggoracle injects the GER on L2 (A) GlobalExitRootManagerL2SovereignChain.sol
    Aggkit (B)->>L1: Aggoracle fetches last finalized GER from L1
    Aggkit (B)->>L2 (B): Aggoracle injects the GER on L2 (B) GlobalExitRootManagerL2SovereignChain.sol

    User->>Aggkit (A): Call bridge_l1InfoTreeIndexForBridge endpoint on the origin network(A)
    Aggkit (A)-->>User: Returns L1InfoTree index X for which the bridge was included
    loop Poll destination network, until `L1InfoTreeLeaf` is retrieved  
      User->>Aggkit (B): Poll bridge_injectedInfoAfterIndex on destination network L2(B) until a non-null response.  
      Aggkit (B)-->>User: Returns the first L1InfoTreeLeaf(GER=Y) for the GER injected on L2(B) at or after L1InfoTree index X
    end 
    User->>Aggkit (A): Call bridge_getProof on origin network(A) to generate merkle proof for bridge using l1InfoTreeIndex of GER Y and networkID(A)
    
    Aggkit (A)-->>User: Return claim proof
    User->>L2 (B): Claim (proof)
    L2 (B)->>L2 (B): Send claim tx<br/>(bridge is settled on the L2 (B))
    L2 (B)-->>User: Tx hash
```

### Bridge flow L1 -> L2

The diagram below describes the basic L1 -> L2 bridge workflow.

```mermaid
sequenceDiagram
    participant User
    participant L1
    participant Aggkit
    participant L2

    User->>L1: Bridge assets to L2
    L1->>L1: Updates the mainnet exit tree
    L1->>L1: Update GER
    Note right of L1: bridgeContract updates the GER<br/>only if `forceUpdateGlobalExitRoot` is true in the bridge transaction.
    Aggkit->>L1: Aggoracle fetches last finalized GER
    Aggkit->>L2: Aggoracle injects the GER on L2 GlobalExitRootManagerL2SovereignChain.sol

    User->>Aggkit: Call bridge_l1InfoTreeIndexForBridge endpoint on the origin network
    Aggkit-->>User: Returns L1InfoTree index X for which the bridge was included
    loop Poll destination network, until `L1InfoTreeLeaf` is retrieved  
      User->>Aggkit: Poll bridge_injectedInfoAfterIndex on destination network (L2) until a non-null response.  
      Aggkit-->>User: Returns the first L1InfoTreeLeaf(GER=Y) for the GER injected on L2 at or after L1InfoTree index X
    end 

    User->>Aggkit: Call bridge_getProof on origin network to generate merkle proof for bridge using l1InfoTreeIndex of GER Y and networkID=0 (L1)
    Aggkit-->>User: Return claim proof
    User->>L2: Claim (proof)
    L2->>L2: Send claimAsset/claimBridge tx on the destination network<br/>(bridge is settled on the L2)
    L2-->>User: Tx hash
```

**Notes:**  

1. In CDK-Erigon, the Global Exit Root (GER) on the L2 smart contract (`PolygonZKEVMGlobalExitRootL2.sol`) is automatically updated by the sequencer. In a sovereign chain, the GER is injected on L2 (`GlobalExitRootManagerL2SovereignChain.sol`) by the Aggoracle component.  

2. A non-null response from `bridge_injectedInfoAfterIndex` indicates that the bridge is ready to be claimed on the destination network.  

3. If `forceUpdateGlobalExitRoot` is set to false in a bridge transaction, the GER will not be updated with that transaction. The user must wait until the GER is updated by another bridge transaction before claiming. This is done to save gas costs while bridging.

### Bridge flow L2 -> L1

The diagram below describes the basic L2 -> L1 bridge workflow.

```mermaid
sequenceDiagram
    participant User
    participant L2
    participant Aggkit
    participant AggLayer
    participant L1

    User->>L2: Bridge assets to L1
    L2->>L2: Index bridge tx & updates the local exit tree
    Aggkit->>AggLayer: Build & send certificate (Aggsender)
    AggLayer->>L1: Settle batch
    L1->>L1: update GER
    Note right of L1: rollupmanager updates the GER & RER (PolygonZKEVMGlobalExitRootV2.sol)
    AggLayer-->>L2: Return L1 tx hash
    Aggkit->>L1: Fetch last finalized GER (Aggoracle)
    Aggkit->>L2: Aggoracle injects GER on L2 (GlobalExitRootManagerL2SovereignChain.sol)

    User->>Aggkit: Query bridge_l1InfoTreeIndexForBridge endpoint on the origin network(L2)
    Aggkit-->>User: Returns L1InfoTree index X for which the bridge was included 
    loop Poll destination network, until `L1InfoTreeLeaf` is retrieved
      User->>Aggkit: Poll bridge_injectedInfoAfterIndex on destination network (L1) until a non-null response.
      Aggkit-->>User: Returns the first L1InfoTreeLeaf(GER=Y) for the GER injected at or after L1InfoTree index X
    end

    Aggkit-->>User: Return claim proof
    User->>L1: Claim (proof)
    L1->>L1: Send claimAsset/claimBridge tx on the destination network<br/>(bridge is settled on the L1)
    L1-->>User: Tx hash
```

## Indexers

The bridge service relies on specific data located on different chains (such as `bridge`, `claim`, and `token mapping` events, as well as the L1 info tree). These data are retrieved using indexers. Indexers consists of three components: driver, downloader and processor. 

### Driver

Driver is in charge of retrieving the blocks and also monitors for the reorgs (using the reorg detector component). The idea is to have driver implementation per chain type (so far we have the EVM driver, but in future, each non-evm chain would require a new driver implementation).

### Downloader

Downloader is in charge of parsing the blocks and logs that are retrieved by the driver. Downloader (indirectly, via the driver) passes the parsed data to the processor.

### Processor

Processor represents the persistance layer, which writes retrieved indexer data in a format suitable for serving it via API. It utilizes SQL lite database.

The diagram below depicts the interaction between components of each indexer.

```mermaid
sequenceDiagram
    participant Driver
    participant Downloader
    participant Processor

    Driver->>Driver: Fetch blocks in a loop
    Driver->>Driver: Monitor reorgs & finalization
    Driver-->>Downloader: Send finalized blocks & logs
    Downloader->>Downloader: Parse blocks & event logs
    Downloader-->>Processor: Send parsed data
    Processor->>Processor: Persist data in SQLite DB
```

## Syncers

In this paragraph, we will list and briefly describe syncers that are of interest for the bridge service.

### L1 Info Tree Sync

It interacts with L1 execution layer (via RPC) in order to:

- Sync the L1 info tree,
- Generate merkle proofs,
- Build the relation `bridge <-> L1InfoTree index` for bridges originated on L1
- Sync the rollup exit tree (namely a tree consisted of all local exit trees, that tracks exits per rollup network), persist, generate proofs

### Bridge Sync

It interacts with the L2 or L1 execution layer (via RPC) in order to:

- Sync bridges, claims and token mappings. Needs to be modular as it's execution client specific.
- Build the local exit tree
- Generate merkle proofs

## Bridging custom ERC20 token

When a non-native ERC20 token, not yet mapped on a destination network, is bridged, its representation is deployed on the destination network using the `CREATE2` opcode. The mapping process emits the `NewWrappedToken` [event](https://github.com/0xPolygonHermez/zkevm-contracts/blob/21d3fd6ec0881731de49f1a6133fb97ed863a7ab/contracts/v2/PolygonZkEVMBridgeV2.sol#L561-L566) on the destination network.

Mapped token details are available via the `bridge_getTokenMappings` endpoint.

The following diagram depicts the basic flow of bridging the custom ERC20 token.

```mermaid
sequenceDiagram
    participant User
    participant OriginERC20 as Origin ERC20 Token
    participant OriginBridge as Origin Bridge Contract
    participant DestIndexer as Destination Bridge Indexer
    participant DestBridge as Destination Bridge Contract

    %% Step 1: Approve Transaction
    User->>OriginERC20: approve(amount)
    Note right of OriginERC20: User authorizes bridge to transfer tokens

    %% Step 2: Call Bridge Asset
    User->>OriginBridge: bridgeAsset(amount, destinationNetwork)
    OriginBridge-->>User: Transaction receipt (bridge asset event emitted)

    %% Step 3: Indexing on Destination
    DestIndexer-->>OriginBridge: Polls for bridge asset event
    OriginBridge-->>DestIndexer: Emits bridge asset event
    Note right of DestIndexer: Indexes bridge asset transaction

    %% Step 4: Polling for Claim Readiness
    loop Poll until ready for claim
        User->>DestIndexer: Is bridge ready for claim?
        DestIndexer-->>User: Not ready yet / Ready signal
    end

    %% Step 5: Claim Bridge on Destination
    User->>DestBridge: claimBridge(leafValue, proofLocalExitRoot, proofRollupExitRoot)
    Note right of DestBridge: `leafValue` consists of bridge data <br/> (e.g. globalIndex, originNetwork, originTokenAddress, <br/>destinationNetwork, destinationAddress etc.)
    DestBridge-->>DestBridge: Deploys wrapped token
    DestBridge-->>DestBridge: Performs token mapping
    DestBridge-->>DestBridge: Mints wrapped token to the destination address

    %% Step 6: Final Transaction Hash to User
    DestBridge-->>User: Transaction hash (wrapped token deployed and tokens minted to the destination address)
    Note right of User: Bridge process completed successfully
```

## API Documentation

<iframe src="assets/swagger/bridge_service/index.html" 
  style="width: 100%; height: 90vh; border: none;"
  loading="lazy"></iframe>
