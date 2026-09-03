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

4. Over the REST API, `bridge_injectedInfoAfterIndex` is served by `GET /bridge/v1/injected-l1-info-leaf`, which now
   responds `404 Not Found` (not `500`) when no injected global exit root covers the requested L1 info tree index
   yet — callers should treat `404` as "not ready yet, retry later" rather than a hard failure. The Go client
   (`bridgeservice/client.Client.GetInjectedL1InfoLeaf`) surfaces this as the `client.ErrNotFound` sentinel. This
   endpoint also backs the Auto Claim destination-readiness gate for L2-destination claimers — see
   [Auto Claim Service](./autoclaim.md#architecture) for how it is used to decide when a bridge is ready to claim.

5. The same `404`-for-not-ready contract from note 4 now also applies to `GET /bridge/v1/l1-info-tree-index`
   (`bridge_l1InfoTreeIndexForBridge`) and `GET /bridge/v1/claim-proof`: both previously returned `500` whenever
   the L1 info tree syncer or a bridge syncer had simply not caught up yet to the requested deposit/leaf, and now
   return `404` for that condition, reserving `500` for genuine faults. The Go client surfaces this as
   `client.ErrNotFound` on `Client.GetL1InfoTreeIndex` and `Client.GetClaimProof`. In addition, the `404`
   semantics described in note 4 for `/injected-l1-info-leaf` now cover its **L1** path (`network_id=0`) as well
   as its L2 path — previously the L1 path fell through to `500` when `l1infotreesync` had not yet indexed the
   requested leaf; it now answers `404` there too.

6. `/l1-info-tree-index`, `/claim-proof`, and `/injected-l1-info-leaf` can also respond `503 Service Unavailable`
   when a syncer they read from is halted or in an inconsistent state (e.g. resolving a reorg). `503` is a second
   retry-later code, but it does **not** mean the same thing as `404`: `404` means the syncer is healthy and
   simply hasn't indexed the requested data yet, while `503` means a syncer is in an operational fault state.
   Retrying is appropriate for both, but operators and client authors should not conflate them — persistent `503`s
   warrant investigating the syncer, whereas persistent `404`s only indicate lag.

7. The fallback inside `getFirstL1InfoTreeIndexForL1Bridge`, which backs `bridge_l1InfoTreeIndexForBridge` in the
   flow diagrams above, was corrected. When the primary `GetRootByLER` lookup misses because the L1 bridge syncer
   has not yet caught up to the tip of the L1 info tree, the fallback now clamps to the most recent L1 info tree
   leaf at or before the last block the L1 bridge syncer has indexed. It previously reused a position from the L1
   bridge **exit** tree (a deposit count) as if it were an L1 **info** tree index — two different counters in two
   different trees — which could surface as a `500` `sql: no rows in result set` error for a deposit that was
   already settled. The flow itself is unchanged; only the correctness of this internal fallback lookup was fixed.

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

## Claim candidates endpoint

`GET /bridge/v1/claim-candidates` lists bridges originated on the network that this bridge
service instance itself syncs (its own `bridgesync`) that are candidates for claiming against a
requested local exit root. It is intended for a remote consumer (e.g. a node running Auto Claim
for a different network) that needs to discover claimable bridges from a source network it does
not sync locally.

The response does **not** include a Merkle proof for each bridge. A consumer that needs the
leaf-to-local-exit-root proof for a specific bridge fetches it separately, at claim time, from
`GET /bridge/v1/claim-proof` (see the Auto Claim `RollupPreparer`, which always fetches this proof
fresh when preparing a claim rather than caching one derived at discovery time).

There is no `network_id` selector: the endpoint always answers for the bridge service's own
source network. If that instance has no L2/source `bridgesync` configured, it returns `503`.

| Param | Required | Meaning |
| --- | --- | --- |
| `destination_network_ids` | yes | Destination network IDs to filter by, sent as a **repeated** query parameter (`?destination_network_ids=1&destination_network_ids=2`), not comma-separated. Maximum 5. |
| `to_ler` | yes | Local exit root (0x-prefixed 32-byte hex hash) the proofs are built against. Must resolve to a root this bridge service has synced. |
| `from_ler` | no | Exclusive lower-bound local exit root (hex hash). When omitted, the full history is considered. |
| `page_number` | no | Page number (default `1`). |
| `page_size` | no | Page size (default `100`). |

Bridges are matched by `deposit_count ∈ (index(from_ler), index(to_ler)]` and
`destination_network ∈ destination_network_ids`. If `to_ler` (or `from_ler`, when provided) has
not been synced yet, the endpoint responds `404` with a body of the form
`{"error": "to_ler 0x... not found (not synced yet)"}` (same pattern for `from_ler`) — callers
should treat this as "not ready yet, retry later" rather than a hard failure.

Response shape:

```json
{
  "claim_candidates": [
    {
      "bridge": { "...": "a BridgeResponse, see /bridges" }
    }
  ],
  "count": 1
}
```

The `bridge` field is the only content per candidate — there is no per-bridge proof or local exit
root field. `to_ler` (and `from_ler`, when provided) still define the deposit-count range the
candidates are drawn from; they are request parameters, not part of each candidate.

Example request:

```
GET /bridge/v1/claim-candidates?destination_network_ids=0&destination_network_ids=2&to_ler=0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757
```

The Go client exposes this as `client.GetClaimCandidates(ctx, client.GetClaimCandidatesParams{...})`
(`bridgeservice/client/client.go`), which returns `client.ErrNotFound` when `to_ler`/`from_ler`
is not synced yet.

## Sync status

`GET /bridge/v1/sync-status` reports the synchronization status of the L1 and L2 bridge indexers, plus
(when applicable) the l2gersync (injected-GER) syncer. Response shape (`types.SyncStatus`):

```json
{
  "l1_info": {
    "contract_deposit_count": 100,
    "synchronized_deposit_count": 100,
    "is_synced": true,
    "is_active": true,
    "last_processed_block": 1234,
    "network_block": 2555
  },
  "l2_info": {
    "contract_deposit_count": 200,
    "synchronized_deposit_count": 200,
    "is_synced": true,
    "is_active": true,
    "last_processed_block": 5678,
    "network_block": 5680
  },
  "l2_ger_info": {
    "is_active": true,
    "last_processed_block": 12345678
  }
}
```

`l1_info` / `l2_info` (`NetworkSyncInfo`) compare on-chain bridge deposit counts against the local
`bridgesync` database counts, per network.

`l2_ger_info` (`L2GERSyncInfo`) reports the l2gersync (injected-GER) syncer's own progress, independent of
`l2_info`:

- `is_active` — `true` when this bridgeservice instance has an l2gersync syncer wired in. It is always
  `false` on an **L1 bridgeservice** (l2gersync only runs against an L2 sovereign chain), and `false` when
  running against an L2 that isn't configured with l2gersync.
- `last_processed_block` — the last L2 block l2gersync has processed. Compare this against the L2 chain
  head (`l2_info.network_block`) to tell whether l2gersync is keeping up. A value that stays pinned below
  a known block while the chain head keeps advancing indicates l2gersync is stuck — most commonly because
  an invalid GER was injected and not yet removed on-chain; see the
  [remove-GER runbook](./remove_ger_runbook.md#blocking-and-automatic-recovery) for the blocking/automatic
  recovery behavior and how to use this field to confirm recovery.

## Public configuration

`GET /bridge/v1/config` returns a sanitized view of this instance's configuration, useful e.g. to
configure a proxy in front of the bridge service without duplicating its contract addresses.
It never exposes RPC URLs, DB paths, private keys, or any other internal/sensitive configuration
value. Response shape (`types.PublicConfigResponse`):

```json
{
  "config_sha1sum": "356a192b7913b04c54574d18c28d46e6395428ab",
  "components": {
    "L1InfoTreeSync": {
      "block_finality": "FinalizedBlock",
      "initial_block": 0,
      "sync_block_chunk_size": 100
    },
    "BridgeL1Sync": {
      "block_finality": "LatestBlock",
      "initial_block": 0,
      "sync_block_chunk_size": 100
    },
    "BridgeL2Sync": {
      "block_finality": "LatestBlock",
      "initial_block": 0,
      "sync_block_chunk_size": 100
    },
    "L2GERSync": {
      "block_finality": "LatestBlock",
      "initial_block": 0,
      "sync_block_chunk_size": 100,
      "sync_mode": "SovereignChain"
    }
  },
  "contracts": {
    "L1": {
      "GlobalExitRootAddr": "0x0000000000000000000000000000000000000000",
      "RollupManagerAddr": "0x0000000000000000000000000000000000000000",
      "BridgeAddr": "0x0000000000000000000000000000000000000000"
    },
    "L2": {
      "GlobalExitRootAddr": "0x0000000000000000000000000000000000000000",
      "BridgeAddr": "0x0000000000000000000000000000000000000000"
    }
  }
}
```

`components` mirrors the public subset of each syncer's own configuration (`SyncComponentConfig`);
`contracts` deduplicates the smart contract addresses used by this instance instead of repeating
them once per component (as they appear in the raw aggkit configuration).

`components.L2GERSync.sync_mode` is not configuration — it's the GER manager mode (`Legacy` or
`SovereignChain`) l2gersync auto-detected by probing the L2 GER contract at startup (see
[l2_ger_syncer.go](../l2gersync/l2_ger_syncer.go)) — but useful operational information, so it's
reported alongside that component's config. It's omitted when this instance isn't running the
L2GERSync component.

`config_sha1sum` is the SHA-1 checksum (hex-encoded) of this instance's fully-resolved
configuration. It lets a caller (e.g. a proxy) detect when the running configuration differs from
what it last saw, without comparing full (and potentially sensitive) config contents.

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

## Prometheus Metrics

The bridge service exposes several Prometheus metrics to track the number of handled requests and their latencies for different API endpoints.
These metrics help monitor service performance, request volume, and latency distribution across various handlers.
Each handler is described with a unique handler id and these are the values, depending of what data they are providing:
- `get_bridges`,
- `get_claims`,
- `get_token_mappings`,
- `get_legacy_token_migrations`,
- `l1_info_tree_index_for_bridge`,
- `injected_info_after_index`,
- `claim_proof`,
- `get_claim_candidates`,
- `last_reorg_event`,
- `get_sync_status`,
- `health_check`,

| **Metric Name** | **Type** | **Description** |
| --- | --- | --- |
| `bridge_total_requests` | CounterVec | Total number of requests handled per endpoint (`handler_id`) and HTTP status code (`status_code`). |
| `bridge_request_latency_seconds` | HistogramVec | Latency of requests in seconds, recorded per endpoint (`handler_id`). Useful for analyzing request duration distributions. |

### Usage Notes

All metrics are counters, meaning they only increase over time.
Each metric helps monitor usage and performance of its corresponding API endpoint.

## API Documentation

<iframe src="assets/swagger/bridge_service/index.html" 
  style="width: 100%; height: 90vh; border: none;"
  loading="lazy"></iframe>

