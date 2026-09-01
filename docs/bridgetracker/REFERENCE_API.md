# Bridge Contract

## AgglayerBridge: `bridgeAsset`

Function defined in [AgglayerBridge.sol:290](src/agglayer-contracts/contracts/AgglayerBridge.sol#L290) and declared in the [IAgglayerBridge.sol:127](src/agglayer-contracts/contracts/interfaces/IAgglayerBridge.sol#L127) interface.

```solidity
function bridgeAsset(
    uint32 destinationNetwork,
    address destinationAddress,
    uint256 amount,
    address token,
    bool forceUpdateGlobalExitRoot,
    bytes calldata permitData
) public payable virtual ifNotEmergencyState nonReentrant;
```

### Parameters

| Parameter | Type | Description |
| --- | --- | --- |
| `destinationNetwork` | `uint32` | Destination network. Reverts with `DestinationNetworkInvalid` if it is the current network. |
| `destinationAddress` | `address` | Destination address on the other network. |
| `amount` | `uint256` | Amount of tokens to bridge. |
| `token` | `address` | Token address. `address(0)` is reserved for the gas token (ether or a custom ERC-20 depending on `WETHToken`). |
| `forceUpdateGlobalExitRoot` | `bool` | If `true`, updates the global exit root at the end of the call. |
| `permitData` | `bytes` | Raw data of the token's `permit` call (optional, executed if not empty). |

### Emitted events

The only event emitted directly by the function is `BridgeEvent` ([AgglayerBridge.sol:122](src/agglayer-contracts/contracts/AgglayerBridge.sol#L122)):

```solidity
event BridgeEvent(
    uint8 leafType,              // _LEAF_TYPE_ASSET (0) in this case
    uint32 originNetwork,
    address originAddress,
    uint32 destinationNetwork,
    address destinationAddress,
    uint256 amount,              // leafAmount: the amount actually received
    bytes metadata,
    uint32 depositCount
);
```

Note: the event's `amount` is `leafAmount`, not necessarily the `amount` sent — for fee-on-transfer tokens or remapped wrapped tokens, the amount actually received/burned by the bridge is recorded ([AgglayerBridge.sol:359-372](src/agglayer-contracts/contracts/AgglayerBridge.sol#L359-L372)).

Indirect events:

- If `forceUpdateGlobalExitRoot == true`, `globalExitRootManager.updateExitRoot(getRoot())` is called, which emits its own event on the AgglayerGER contract (e.g. `UpdateL1InfoTree` on L1).
- The token emits its usual `Transfer`/`Approval` events (transfer to the bridge, or burn if it is a wrapped token).

## AgglayerBridge: `bridgeMessage`

Function defined in [AgglayerBridge.sol:417](src/agglayer-contracts/contracts/AgglayerBridge.sol#L417) and declared in the [IAgglayerBridge.sol:136](src/agglayer-contracts/contracts/interfaces/IAgglayerBridge.sol#L136) interface. Bridges created with `bridgeMessage` are supervised the same way as those created with `bridgeAsset`.

```solidity
function bridgeMessage(
    uint32 destinationNetwork,
    address destinationAddress,
    bool forceUpdateGlobalExitRoot,
    bytes calldata metadata
) external payable virtual ifNotEmergencyState;
```

### Parameters (bridgeMessage)

| Parameter | Type | Description |
| --- | --- | --- |
| `destinationNetwork` | `uint32` | Destination network. Reverts with `DestinationNetworkInvalid` if it is the current network. |
| `destinationAddress` | `address` | Destination address on the other network. |
| `forceUpdateGlobalExitRoot` | `bool` | If `true`, updates the global exit root at the end of the call. |
| `metadata` | `bytes` | Message payload delivered to the destination address on claim. |

The ETH value to bridge is sent as `msg.value`. On networks with a gas token different from ether, sending value reverts with `NoValueInMessagesOnGasTokenNetworks`; the `bridgeMessageWETH` variant ([AgglayerBridge.sol:446](src/agglayer-contracts/contracts/AgglayerBridge.sol#L446)) must be used instead, which burns `amountWETH` WETH tokens.

### Emitted events (bridgeMessage)

Both variants call the internal `_bridgeMessage` ([AgglayerBridge.sol:480](src/agglayer-contracts/contracts/AgglayerBridge.sol#L480)), which emits the same `BridgeEvent` as `bridgeAsset` but with `leafType = _LEAF_TYPE_MESSAGE` (1) and `amount` set to the ETH value sent (or the WETH `leafAmount` for `bridgeMessageWETH`). If `forceUpdateGlobalExitRoot == true`, the global exit root update emits its event on the AgglayerGER contract, same as `bridgeAsset`.


# Rollup Contract

## How to obtain the sequencer URL

The consensus contract of each rollup exposes the sequencer URL on-chain as a public variable, `trustedSequencerURL`, defined in [PolygonConsensusBase.sol:51](src/agglayer-contracts/contracts/lib/PolygonConsensusBase.sol#L51) (next to `trustedSequencer`, [PolygonConsensusBase.sol:48](src/agglayer-contracts/contracts/lib/PolygonConsensusBase.sol#L48)). All consensus contracts inherit it: `AggchainFEP` / `AggchainECDSAMultisig` (via `AggchainBase`), `PolygonZkEVMEtrog` / `PolygonValidiumEtrog` (via `PolygonRollupBaseEtrog`) and `PolygonPessimisticConsensus`.

```solidity
// PolygonConsensusBase (inherited by all consensus contracts)
address public trustedSequencer;
string public trustedSequencerURL;
```

The value is set at `initialize` and can be changed by the admin with `setTrustedSequencerURL(string)` ([PolygonConsensusBase.sol:221](src/agglayer-contracts/contracts/lib/PolygonConsensusBase.sol#L221)), which emits `SetTrustedSequencerURL(string)` ([PolygonConsensusBase.sol:99](src/agglayer-contracts/contracts/lib/PolygonConsensusBase.sol#L99)).

### Resolving it from the rollupID

Starting only from the `rollupID` and an L1 RPC:

1. Get the consensus contract address from the AgglayerManager (rollup manager) with `rollupIDToRollupData(uint32)` ([AgglayerManager.sol:1825](src/agglayer-contracts/contracts/AgglayerManager.sol#L1825)) or `rollupIDToRollupDataV2(uint32)` ([AgglayerManager.sol:1891](src/agglayer-contracts/contracts/AgglayerManager.sol#L1891)). In both return structs (`RollupDataReturn` / `RollupDataReturnV2`) the first field is `rollupContract`.
2. Call `trustedSequencerURL()` on that contract.

```bash
# 1. rollupContract is the first field of the returned struct
cast call $AGGLAYER_MANAGER "rollupIDToRollupData(uint32)" $ROLLUP_ID --rpc-url $L1_RPC

# 2. Read the sequencer URL from the consensus contract
cast call $ROLLUP_CONTRACT "trustedSequencerURL()(string)" --rpc-url $L1_RPC
```

### Notes

- It is a "trusted" value maintained manually by the chain admin: it can be empty, outdated, or point to an internal endpoint. Treat it as a hint with a configuration fallback, not as a guaranteed source.
- It is usually the sequencer's JSON-RPC endpoint, not necessarily a public endpoint suitable for a service.
- In the new aggchains (`AggchainFEP`, `AggchainECDSAMultisig`) the sequencer URL is also migrated into the multisig signers list on initialization ([AggchainFEP.sol:517-523](src/agglayer-contracts/contracts/aggchains/AggchainFEP.sol#L517-L523)): `trustedSequencer` is added as a signer with `trustedSequencerURL` as its URL (`"NO_URL"` if empty). Signer URLs can be queried with `getAggchainSignerInfos()` ([AggchainBase.sol:717](src/agglayer-contracts/contracts/lib/AggchainBase.sol#L717)) or `signerToURLs(address)` ([AggchainBase.sol:77](src/agglayer-contracts/contracts/lib/AggchainBase.sol#L77)).


# GlobalExitRoot Contract

## AgglayerGER: `UpdateL1InfoTree` event

Event defined in [AgglayerGER.sol:38](src/agglayer-contracts/contracts/AgglayerGER.sol#L38) (same definition in the previous version, [PolygonZkEVMGlobalExitRootV2Pessimistic.sol:31](src/agglayer-contracts/contracts/previousVersions/pessimistic/PolygonZkEVMGlobalExitRootV2Pessimistic.sol#L31)).

```solidity
event UpdateL1InfoTree(
    bytes32 indexed mainnetExitRoot,
    bytes32 indexed rollupExitRoot
);
```

### Parameters (UpdateL1InfoTree)

| Parameter | Type | Description |
| --- | --- | --- |
| `mainnetExitRoot` | `bytes32` (indexed) | Mainnet exit root (MER) at the moment of the update. |
| `rollupExitRoot` | `bytes32` (indexed) | Rollup exit root (RER) at the moment of the update. |

### Emission (UpdateL1InfoTree)

Emitted by `updateExitRoot` ([AgglayerGER.sol:127](src/agglayer-contracts/contracts/AgglayerGER.sol#L127)) whenever the global exit root is updated — i.e. when the bridge or the rollup manager pushes a new MER/RER and a new leaf is added to the L1 Info Tree. The new `l1InfoRoot` is stored in `l1InfoRootMap` right before the event is emitted.

## AgglayerGER: `UpdateL1InfoTreeV2` event

Event defined in [AgglayerGER.sol:46](src/agglayer-contracts/contracts/AgglayerGER.sol#L46) (same definition in the previous version, [PolygonZkEVMGlobalExitRootV2Pessimistic.sol:39](src/agglayer-contracts/contracts/previousVersions/pessimistic/PolygonZkEVMGlobalExitRootV2Pessimistic.sol#L39)).

```solidity
event UpdateL1InfoTreeV2(
    bytes32 currentL1InfoRoot,
    uint32 indexed leafCount,
    uint256 blockhash,
    uint64 minTimestamp
);
```

### Parameters (UpdateL1InfoTreeV2)

| Parameter | Type | Description |
| --- | --- | --- |
| `currentL1InfoRoot` | `bytes32` | Root of the L1 Info Tree after adding the new leaf. |
| `leafCount` | `uint32` (indexed) | Number of leaves in the L1 Info Tree (the new leaf's index is `leafCount - 1`). |
| `blockhash` | `uint256` | Hash of the previous block (`lastBlockHash`), part of the leaf data. |
| `minTimestamp` | `uint64` | Timestamp of the block in which the leaf was added. |

### Emission (UpdateL1InfoTreeV2)

Emitted by `updateExitRoot` ([AgglayerGER.sol:132](src/agglayer-contracts/contracts/AgglayerGER.sol#L132)), immediately after `UpdateL1InfoTree` in the same transaction. It complements the V1 event with the L1 Info Tree leaf information (root, leaf count, block hash and timestamp), which is what syncers such as aggkit's `l1infotreesync` consume to rebuild the tree off-chain.


# AggOracle (via Bridge)

## `GET /bridge/v1/injected-l1-info-leaf`

Handler `InjectedL1InfoLeafHandler` defined in [bridge.go:845](aggkit/bridgeservice/bridge.go#L845).

Returns a leaf of the **L1 Info Tree**. The semantics differ depending on the network queried.

### Query parameters (both required)

| Parameter | Type | Description |
| --- | --- | --- |
| `network_id` | `uint32` | Network to query: `0` = L1 (mainnet), or the network ID of the L2 served by this aggkit instance. Any other value returns `400`. |
| `leaf_index` | `uint32` | L1 Info Tree index to look up (L1) or to start searching from (L2). |

### Behavior per `network_id`

- **`network_id = 0` (L1):** returns the L1 Info Tree leaf **at that exact index** (`l1InfoTree.GetInfoByIndex`). "Injected" does not really apply here; it is a direct lookup.
- **`network_id = <L2>`:** looks up in the L2 GER syncer (`l2gersync`) the **first Global Exit Root actually injected into the L2** (via the L2 GER contract) whose `l1_info_tree_index >= leaf_index` — the query in [processor.go:229](aggkit/l2gersync/processor.go#L229) does `WHERE l1_info_tree_index >= $1 ORDER BY l1_info_tree_index ASC LIMIT 1` — and then returns the L1 Info Tree leaf associated with that injected GER.

In other words, for L2 it answers: *"give me the first L1 Info Tree leaf, at or after this index, whose GER is already available on the L2"*. This is what a claimer on the L2 needs: it is not enough for the leaf to exist on L1 — the GER must have been injected into the L2 for the claim to be verifiable there.

### Response (`200`, `L1InfoTreeLeafResponse`, defined in [types.go:319](aggkit/bridgeservice/types/types.go#L319))

```json
{
  "block_num": 123456,            // L1 block where the leaf was recorded
  "block_pos": 5,                 // position of the event within the block
  "l1_info_tree_index": 42,       // index of the returned leaf (may be > leaf_index in the L2 case)
  "previous_block_hash": "0x...",
  "timestamp": 1684500000,        // L1 block timestamp (seconds since Unix epoch)
  "mainnet_exit_root": "0x...",   // MER at this leaf
  "rollup_exit_root": "0x...",    // RER at this leaf
  "global_exit_root": "0x...",    // GER = hash(MER, RER)
  "hash": "0x...",                // unique hash of the leaf
  "injected_l2_block_num": 654321 // L2 case only: the L2 block where this GER was actually
                                   // injected (from l2gersync); omitted for network_id=0
}
```

`block_num`/`timestamp` above always describe the **L1** event that produced the leaf, even in
the `network_id=<L2>` case — they are not the block where the GER got injected on the L2.
`injected_l2_block_num` fills that gap: it is `l2gersync`'s own `block_num` for the matched
`imported_global_exit_root_v2` row (see [processor.go:229](aggkit/l2gersync/processor.go#L229)),
set only on the L2 branch. There is no equivalent L2 timestamp yet — `l2gersync` does not persist
one (see [evm_downloader_sovereign.go](aggkit/l2gersync/evm_downloader_sovereign.go)).

### Errors

- `400` — invalid parameter, or a `network_id` that is neither `0` nor the served L2.
- `500` — lookup failure; in the L2 case this includes the situation where **no injected GER with index >= `leaf_index` exists yet** (the query returns `ErrNotFound` and the handler maps it to `500`, not `404`).



# Agglayer gRPC


## GetLatestSettledCertificateHeader
This is a call to `GetLatestCertificateHeader(LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED)`

Implemented in [agglayer_grpc_client.go:88](aggkit/agglayer/grpc/agglayer_grpc_client.go#L88). It calls the Agglayer's `NodeStateService.GetLatestCertificateHeader` RPC with `NetworkId` and `Type = LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED`, and converts the proto response to `*types.CertificateHeader` via `convertProtoCertificateHeader` ([agglayer_grpc_client.go:364](aggkit/agglayer/grpc/agglayer_grpc_client.go#L364)).

### Response (`types.CertificateHeader`, defined in [types.go:1261](aggkit/agglayer/types/types.go#L1261))

| Field | Type | Description |
| --- | --- | --- |
| `NetworkID` | `uint32` | Network (rollup) the certificate belongs to. |
| `Height` | `uint64` | Height of the certificate in that network's certificate chain. |
| `EpochNumber` | `*uint64` | Agglayer epoch in which it was settled; nullable (normally set for settled certs). |
| `CertificateIndex` | `*uint64` | Index within the epoch; nullable. |
| `CertificateID` | `common.Hash` | Unique identifier of the certificate. |
| `PreviousLocalExitRoot` | `*common.Hash` | LER before the certificate; nullable. |
| `NewLocalExitRoot` | `common.Hash` | LER resulting from applying the certificate. |
| `Status` | `CertificateStatus` | Mapped from proto in [agglayer_grpc_client.go:559](aggkit/agglayer/grpc/agglayer_grpc_client.go#L559): `Pending` / `Proven` / `Candidate` / `InError` / `Settled`. Always `Settled` for this call. |
| `Metadata` | `common.Hash` | Certificate metadata packed into 32 bytes (encodes version, L2 block from/offset, timestamp). |
| `Error` | `error` | Only set if the proto carries `Error.Message` (relevant for `InError` certs). Not serialized to JSON (`json:"-"`). |
| `SettlementTxHash` | `*common.Hash` | Hash of the L1 transaction that settled the certificate; nullable. |

### Notes (GetLatestSettledCertificateHeader)

- **May return `nil, nil`**: if the network has no settled certificate yet, the proto response carries a nil `CertificateHeader` and the conversion returns `nil` without error — callers must check for `nil`.
- gRPC errors are wrapped with `RepackGRPCErrorWithDetails`, which extracts the Agglayer error details into the message.
- The proto→Go status mapping has a silent default: any unknown proto status falls back to `Pending`.



## GetLatestPendingCertificateHeader
This is a call to `GetLatestCertificateHeader(LATEST_CERTIFICATE_REQUEST_TYPE_PENDING)

## GetNetworkInfo

Implemented in [agglayer_grpc_client.go:149](aggkit/agglayer/grpc/agglayer_grpc_client.go#L149). It calls the Agglayer's `NodeStateService.GetNetworkInfo` RPC with `NetworkId`, and converts the proto response to `types.NetworkInfo` via `convertProtoNetworkState` ([agglayer_grpc_client.go:165](aggkit/agglayer/grpc/agglayer_grpc_client.go#L165)).

### Response (`types.NetworkInfo`, defined in [types.go:1440](aggkit/agglayer/types/types.go#L1440))

| Field | Type | Description |
| --- | --- | --- |
| `Status` | `string` | Current status of the network on the Agglayer (proto enum as string, e.g. `NETWORK_STATUS_ACTIVE`). |
| `NetworkType` | `string` | Aggchain type of the network (proto enum as string). |
| `NetworkID` | `uint32` | Unique identifier of the network. |
| `SettledHeight` | `*uint64` | Height of the latest settled certificate; nullable. |
| `SettledCertificateID` | `*common.Hash` | ID of the latest settled certificate; nullable. |
| `SettledPPRoot` | `*common.Hash` | Pessimistic proof root of the latest settled certificate; nullable. |
| `SettledLER` | `*common.Hash` | Local exit root of the latest settled certificate; nullable. |
| `SettledLETLeafCount` | `*uint64` | Leaf count of the latest settled local exit tree; nullable. |
| `SettledImportedBridgeExit` | `*SettledImportedBridgeExit` | Latest settled claim: `BridgeExitHash` (`common.Hash`) and `GlobalIndex` (`*big.Int`); nullable. |
| `LatestPendingHeight` | `*uint64` | Height of the latest pending certificate; nullable. |
| `LatestPendingStatus` | `*CertificateStatus` | Status of the latest pending certificate (`Pending` / `Proven` / `Candidate` / `InError` / `Settled`); nullable. |
| `LatestPendingError` | `string` | Error message of the latest pending certificate, if any (empty string otherwise). |
| `LatestEpochWithSettlement` | `*uint64` | Epoch number of the latest settlement; nullable. |

### Notes (GetNetworkInfo)

- If the response carries no network info (`!status.HasNetworkInfo()`), the client returns an error `"network info is not available"` instead of an empty struct.
- Unlike the certificate header calls, this method does **not** apply the client's `RequestTimeout` (no `context.WithTimeout`); it uses the caller's context as-is.
- gRPC errors are wrapped with `RepackGRPCErrorWithDetails`.
- `LatestPendingStatus` uses a different status mapping than the certificate header calls (`convertProtoCertStatus`, [agglayer_grpc_client.go:214](aggkit/agglayer/grpc/agglayer_grpc_client.go#L214)): proto `CERTIFICATE_STATUS_UNSPECIFIED` (or nil) maps to `nil`, and any other value is converted by subtracting 1 from the proto enum value.
