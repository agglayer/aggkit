# Backward and Forward LET runbook

## Introduction

The **Local Exit Tree (LET)** is a Merkle tree maintained on L2 that tracks all bridge deposits originating from a given chain. Every time a bridge operation occurs on L2, a new leaf is appended to the LET. Periodically, the `aggsender` component bundles these leaves into a certificate and sends it to the AggLayer, which settles the resulting **Local Exit Root (LER)** on L1.

Under normal operation, the LET on L2 and the LER settled on L1 stay in sync. However, certain failure scenarios can cause them to **diverge**: L1 has a settled LER that does not match the actual state of the LET on L2. When this happens, the L2 network must reconcile its LET to match what was settled on L1, otherwise future certificates will be rejected by the AggLayer because the LER will not match.

To handle these cases, two admin smart contract functions are provided on the [`AgglayerBridgeL2`](https://agglayer.github.io/protocol-team-docs/smart-contracts/v12/AgglayerBridgeL2/) contract:

- **[`backwardLET`](https://agglayer.github.io/protocol-team-docs/smart-contracts/v12/AgglayerBridgeL2/#13-backwardlet)**: Rolls the LET backward to a previous state with fewer deposits. This is used to remove leaves that were added on L2 but do not match what was settled on L1. ([source](https://github.com/agglayer/agglayer-contracts/blob/v12.2.0/contracts/sovereignChains/AgglayerBridgeL2.sol#L732))
- **[`forwardLET`](https://agglayer.github.io/protocol-team-docs/smart-contracts/v12/AgglayerBridgeL2/#14-forwardlet)**: Advances the LET by adding one or more leaves in a single transaction. This is used to insert leaves that were settled on L1 but are missing from the L2 tree. ([source](https://github.com/agglayer/agglayer-contracts/blob/v12.2.0/contracts/sovereignChains/AgglayerBridgeL2.sol#L797))

Both functions can **only** be called while the `AgglayerBridgeL2` contract is in **emergency mode**, and only by an account holding the `GlobalExitRootRemover` role.

## Prerequisites

Before starting, ensure you have these environment variables set. They are referenced throughout the runbook:

```bash
# ── Network RPC endpoints ──
export L2_RPC_URL="<L2 RPC URL>"

# ── Contract addresses (L2) ──
export BRIDGE_L2_ADDR="<AgglayerBridgeL2 proxy address on L2>"
export GER_L2_ADDR="<AgglayerGERL2 proxy address on L2>"

# ── AggLayer endpoints ──
export AGGLAYER_GRPC="<AggLayer node gRPC host:port>"

# ── Bridge service endpoint ──
export BRIDGE_SERVICE_URL="<Bridge service base URL>"  # e.g. http://localhost:8080/bridge/v1

# ── Network ID of the affected L2 chain ──
export NETWORK_ID="<L2 network ID>"

# ── Private key of the account holding the GlobalExitRootRemover role ──
# This same account is used for backwardLET and forwardLET calls.
# For activateEmergencyState/deactivateEmergencyState, the emergencyBridgePauser
# and emergencyBridgeUnpauser keys are needed respectively (may be different accounts).
export GER_REMOVER_PK="<private key>"
export EMERGENCY_PAUSER_PK="<private key for emergencyBridgePauser>"
export EMERGENCY_UNPAUSER_PK="<private key for emergencyBridgeUnpauser>"
```

### Verify role addresses

Before proceeding, confirm which accounts hold each role:

```bash
# Who can call backwardLET / forwardLET (GlobalExitRootRemover)?
cast call $GER_L2_ADDR "globalExitRootRemover()(address)" --rpc-url $L2_RPC_URL

# Who can activate emergency state?
cast call $BRIDGE_L2_ADDR "emergencyBridgePauser()(address)" --rpc-url $L2_RPC_URL

# Who can deactivate emergency state?
cast call $BRIDGE_L2_ADDR "emergencyBridgeUnpauser()(address)" --rpc-url $L2_RPC_URL
```

## Detection

A backward/forward LET operation is needed when the LER settled on L1 diverges from the LET state on L2. This can be detected through the following indicators:

### 1. Certificate rejected by the AggLayer

The `aggsender` submits a certificate to the AggLayer, which rejects it because the `PrevLocalExitRoot` in the certificate does not match the last settled LER on L1. This is the most common first signal of divergence.

The certificate transitions to `InError` status on the AggLayer side. The `aggsender` detects this via its periodic status checker and logs:

| File | Line | Level | Message |
|------|------|-------|---------|
| `aggsender/statuschecker/cert_status_checker.go` | 187 | `INFO` | `certificate <ID> changed status from [<prev>] to [InError] elapsed time: <t> full_cert (agglayer): <cert>` |
| `aggsender/statuschecker/cert_status_checker.go` | 169 | `INFO` | `found <N> InError certificate(s) with no pending certs, enabling retry` |
| `aggsender/aggsender.go` | 332 | `INFO` | `An InError cert exists. Sending a new one (<cfg>)` |
| `aggsender/aggsender.go` | 365 | `ERROR` | `Certificate send trigger: error sending certificate: <err>` |
| `aggsender/aggsender.go` | 536 | `ERROR` | `error creating non accepted certificate: <brief>. Err: <err>` |
| `aggsender/aggsender.go` | 541 | `ERROR` | `error saving non accepted certificate: <brief>. Err: <err>` |

**Recommended alarms**: alert on the `InError` status transition (`INFO` log at `cert_status_checker.go:187` matching `"changed status from.*to \[InError\]"`) and on the `ERROR` at `aggsender.go:365` (`"Certificate send trigger: error sending certificate"`).

### 2. LER mismatch detected during certificate validation

When the `aggsender` attempts to build and validate a new certificate, the local validator compares the certificate's `PrevLocalExitRoot` against the expected value. A mismatch surfaces as an error in the following paths:

| File | Line | Level | Message |
|------|------|-------|---------|
| `aggsender/validator/validate_certificate.go` | 155 | `ERROR` (via `fmt.Errorf`) | `certificate PrevLocalExitRoot <A> is not equal to previous certificate NewLocalExitRoot <B>` |
| `aggsender/validator/validate_certificate.go` | 196 | `ERROR` (via `fmt.Errorf`) | `first certificate must have correct starting PrevLocalExitRoot: <expected>, but got: <actual>` |
| `aggsender/aggsender.go` | 432 | `WARN` | `error validating certificate locally: <err>` |
| `aggsender/aggsender.go` | 329 | `ERROR` | `error checking last certificate from agglayer: <err>` |

**Recommended alarms**: alert on `WARN` at `aggsender.go:432` (`"error validating certificate locally"`) and on any log containing `"PrevLocalExitRoot"` and `"is not equal"` or `"but got"`.

### 3. AggSender unable to build or send certificates

When the `aggsender` repeatedly fails to build or submit a valid certificate (e.g., after a restart following a key compromise), it logs continuously on each retry cycle:

| File | Line | Level | Message |
|------|------|-------|---------|
| `aggsender/aggsender.go` | 419 | `ERROR` (via `fmt.Errorf`) | `error getting certificate build params: <err>` |
| `aggsender/aggsender.go` | 428 | `ERROR` (via `fmt.Errorf`) | `error building certificate: <err>` |
| `aggsender/aggsender.go` | 460 | `ERROR` (via `fmt.Errorf`) | `error sending certificate: <err>` |
| `aggsender/aggsender.go` | 365 | `ERROR` | `Certificate send trigger: error sending certificate: <err>` |
| `aggsender/aggsender.go` | 359 | `ERROR` | `Certificate send trigger: error checking certificate status: <err>` |

**Recommended alarms**: alert on repeated occurrences of `ERROR` at `aggsender.go:365` (`"Certificate send trigger: error sending certificate"`). A single occurrence may be transient; sustained repetition indicates a structural issue requiring investigation.

---

**Root causes** that can trigger this divergence include:

- **Compromised or buggy `aggsender`**: The `aggsender` private key is compromised or the component has a bug, causing it to craft and submit a certificate with leaves that do not correspond to actual L2 bridge events.
- **L2 network reorg (outpost networks)**: The L2 network reorgs after a certificate has already been settled on L1, meaning the block that contained certain bridge events no longer exists or has different contents.

## Diagnosis

Once detection signals indicate a divergence, the next step is to **determine the exact state on both sides** and identify which recovery case applies. This section provides concrete commands to gather all the data needed.

### Step 1: Query the AggLayer for settled state (L1 truth)

The AggLayer's `GetNetworkInfo` gRPC call returns the last settled certificate details including the settled LER and leaf count:

```bash
grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetNetworkInfo
```

From the response, extract:
- `settled_ler` — the LER that L1 considers as truth
- `settled_let_leaf_count` — the deposit count at which L1 settled (this is the **L1 deposit count**)
- `settled_height` — the certificate height of the last settled certificate
- `settled_certificate_id` — the ID of that certificate

To get the full details of the last settled certificate:

```bash
grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID, \"type\": \"LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED\"}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetLatestCertificateHeader
```

This returns a `CertificateHeader` with:
- `prev_local_exit_root` — what the AggLayer expected as the starting LER
- `new_local_exit_root` — the LER after applying this certificate's leaves
- `height` — certificate height
- `status` — should be `SETTLED` (5)

If there is also a pending (possibly InError) certificate:

```bash
grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID, \"type\": \"LATEST_CERTIFICATE_REQUEST_TYPE_PENDING\"}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetLatestCertificateHeader
```

If `status` is `IN_ERROR` (4), the `error` field will contain the rejection reason.

### Step 2: Query the L2 bridge contract for current state

```bash
# Current deposit count on L2
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL

# Current LER (Merkle root of the LET) on L2
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL

# Is the bridge in emergency state?
cast call $BRIDGE_L2_ADDR "isEmergencyState()(bool)" --rpc-url $L2_RPC_URL

# Network ID (sanity check)
cast call $BRIDGE_L2_ADDR "networkID()(uint32)" --rpc-url $L2_RPC_URL
```

### Step 3: Query the bridge service for sync status

The bridge service exposes a sync status endpoint that compares on-chain deposit counts with its local database:

```bash
curl -s "$BRIDGE_SERVICE_URL/sync-status" | jq .
```

The response includes:
- `l2_info.contract_deposit_count` — on-chain deposit count
- `l2_info.synchronized_deposit_count` — how far the bridge service has synced
- `l2_info.is_synced` — whether the syncer is caught up

### Step 4: Compare L1 vs L2 and determine the case

Save the key values:

```bash
# From AggLayer (Step 1)
L1_SETTLED_LER="<settled_ler from GetNetworkInfo>"
L1_DEPOSIT_COUNT="<settled_let_leaf_count from GetNetworkInfo>"

# From L2 contract (Step 2)
L2_LER=$(cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL)
L2_DEPOSIT_COUNT=$(cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL)

echo "L1 settled LER:          $L1_SETTLED_LER"
echo "L1 settled deposit count: $L1_DEPOSIT_COUNT"
echo "L2 current LER:          $L2_LER"
echo "L2 current deposit count: $L2_DEPOSIT_COUNT"
```

The comparison determines the case:

| Condition | Meaning |
|-----------|---------|
| `L1_SETTLED_LER == L2_LER` | No divergence — investigate other causes |
| `L1_SETTLED_LER != L2_LER` and `L1_DEPOSIT_COUNT > L2_DEPOSIT_COUNT` | L1 has leaves that L2 doesn't — **forwardLET needed** |
| `L1_SETTLED_LER != L2_LER` and `L1_DEPOSIT_COUNT == L2_DEPOSIT_COUNT` | Same count but different roots — leaves diverged at the same position — **backwardLET + forwardLET needed** |
| `L1_SETTLED_LER != L2_LER` and `L1_DEPOSIT_COUNT < L2_DEPOSIT_COUNT` | L2 has extra leaves beyond what L1 settled, and possibly L1 settled different leaves — **backwardLET + forwardLET needed** |

### Step 5: List the L2 bridges (leaves) from the divergence point

To understand which bridges exist on L2 after the last matching point, query the bridge service for each deposit count from the divergence point onwards:

```bash
# Get the bridge at a specific deposit count on L2
# Repeat for each deposit count from (last_matching_count + 1) to L2_DEPOSIT_COUNT
DEPOSIT_IDX=3  # example: first divergent position
curl -s "$BRIDGE_SERVICE_URL/bridge-by-deposit-count?network_id=$NETWORK_ID&deposit_count=$DEPOSIT_IDX" | jq .
```

The response contains the full leaf data for that bridge:
- `leaf_type` (0=asset, 1=message)
- `origin_network`
- `origin_address`
- `destination_network`
- `destination_address`
- `amount`
- `metadata`

Loop through all positions to build the list of L2 leaves:

```bash
# Collect all L2 bridges from divergence point to current deposit count
DIVERGENCE_POINT=2  # last matching deposit count
for i in $(seq $((DIVERGENCE_POINT + 1)) $L2_DEPOSIT_COUNT); do
  echo "=== Deposit $i ==="
  curl -s "$BRIDGE_SERVICE_URL/bridge-by-deposit-count?network_id=$NETWORK_ID&deposit_count=$i" | jq '{
    deposit_count,
    leaf_type,
    origin_network,
    origin_address,
    destination_network,
    destination_address,
    amount,
    metadata
  }'
done
```

### Step 6: List the L1-settled leaves (divergent leaves)

The divergent leaves (BX, BY, ...) are the ones that were included in certificates settled on L1 but do not exist on L2. These leaves are part of the `bridge_exits` field of the settled certificates.

To retrieve them, first get the settled certificate header, then inspect its bridge exits. The bridge exits are available via the certificate submission data on the AggLayer. If you have the certificate ID:

```bash
# Get certificate details by ID (the ID comes from GetNetworkInfo.settled_certificate_id)
CERT_ID="<certificate_id hex>"
grpcurl -plaintext -d "{\"certificate_id\": {\"value\": {\"value\": \"$(echo $CERT_ID | xxd -r -p | base64)\"}}}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetCertificateHeader
```

The `CertificateHeader` includes `new_local_exit_root` and `prev_local_exit_root` but **not** the individual bridge exits. The bridge exits (the actual leaf data for BX, BY) must be obtained from the AggLayer's certificate storage or from the operator who submitted the certificate.

> **Note**: If the divergent leaves cannot be retrieved from the AggLayer (the gRPC API currently only exposes certificate headers, not full certificate bodies), contact the AggLayer operator or check the aggsender local database for the certificate that was submitted and settled.

### Summary: determining the recovery case

After collecting the data above:

| L2 has extra leaves beyond divergence? | L1 settled extra leaves beyond divergence? | Case |
|----------------------------------------|-------------------------------------------|------|
| No | No (single divergent leaf) | **Case 1** — forwardLET only |
| Yes | No (single divergent leaf) | **Case 2** — backwardLET then forwardLET |
| No | Yes (multiple divergent leaves) | **Case 3** — forwardLET only (multiple leaves) |
| Yes | Yes (multiple divergent leaves) | **Case 4** — backwardLET then forwardLET |

## Recovery

### Using the tool

A dedicated tool to automate the recovery process is **under development**. Once available, this tool will:

- Query the AggLayer node for the expected LER on L1
- Compare it against the current LET state on L2
- Determine the required sequence of `backwardLET` and `forwardLET` calls
- Compute the necessary Merkle proofs, frontiers, and leaf data
- Execute the smart contract calls in the correct order

Until the tool is available, recovery must be performed manually as described below.

### Contract function signatures reference

Before proceeding, here are the exact Solidity function signatures (from [`AgglayerBridgeL2.sol` v12.2.0](https://github.com/agglayer/agglayer-contracts/blob/v12.2.0/contracts/sovereignChains/AgglayerBridgeL2.sol)):

```solidity
// Roll the LET backward to a previous state
// Modifiers: onlyGlobalExitRootRemover, ifEmergencyState
function backwardLET(
    uint256 newDepositCount,
    bytes32[32] calldata newFrontier,
    bytes32 nextLeaf,
    bytes32[32] calldata proof
) external virtual onlyGlobalExitRootRemover ifEmergencyState;

// Advance the LET by adding new leaves in bulk
// Modifiers: onlyGlobalExitRootRemover, ifEmergencyState
function forwardLET(
    LeafData[] calldata newLeaves,
    bytes32 expectedLER
) external virtual onlyGlobalExitRootRemover ifEmergencyState;

struct LeafData {
    uint8 leafType;        // 0 = asset, 1 = message
    uint32 originNetwork;
    address originAddress;
    uint32 destinationNetwork;
    address destinationAddress;
    uint256 amount;
    bytes metadata;
}

// Emergency state management
// Modifier: onlyEmergencyBridgePauser
function activateEmergencyState() external onlyEmergencyBridgePauser;

// Modifier: onlyEmergencyBridgeUnpauser
function deactivateEmergencyState() external onlyEmergencyBridgeUnpauser;
```

### Manually

The manual recovery process follows these steps. Each step includes the exact CLI commands to execute.

#### Step 1: Stop the `aggsender`

Before performing any recovery operations, stop the `aggsender` to prevent it from interfering (e.g., attempting to send certificates while the bridge is in emergency mode).

```bash
# Stop the aggsender process/container.
# The exact command depends on your deployment (systemd, docker, kubernetes, etc.)
# Example for docker:
docker stop aggsender

# Example for systemd:
sudo systemctl stop aggsender
```

#### Step 2: Activate emergency mode

Call `activateEmergencyState` on the bridge contract. This is a prerequisite for both `backwardLET` and `forwardLET`.

```bash
# Verify emergency state is NOT already active
cast call $BRIDGE_L2_ADDR "isEmergencyState()(bool)" --rpc-url $L2_RPC_URL

# Activate emergency state (requires emergencyBridgePauser key)
cast send $BRIDGE_L2_ADDR "activateEmergencyState()" \
  --private-key $EMERGENCY_PAUSER_PK \
  --rpc-url $L2_RPC_URL

# Confirm activation
cast call $BRIDGE_L2_ADDR "isEmergencyState()(bool)" --rpc-url $L2_RPC_URL
# Expected: true
```

#### Step 3: Roll back the LET if needed (`backwardLET`)

This step is only needed if L2 has extra leaves beyond the divergence point (**Cases 2 and 4**). If only `forwardLET` is needed (**Cases 1 and 3**), skip to Step 4.

The `backwardLET` function requires:
- `newDepositCount` — the target deposit count to roll back to (the divergence point)
- `newFrontier` — 32-element Merkle tree frontier array at the target deposit count
- `nextLeaf` — the leaf hash at position `newDepositCount` in the current tree (proof of inclusion)
- `proof` — Merkle proof that `nextLeaf` exists at position `newDepositCount`

> **Computing `newFrontier`, `nextLeaf`, and `proof`**: These values require off-chain computation from the Merkle tree state. The recovery tool (when available) will compute these automatically. For manual computation, you need access to the full tree state (all leaves up to the current deposit count) to generate the frontier at the target count, the leaf hash at the boundary position, and a Merkle inclusion proof.

```bash
# Example: roll back from deposit count 4 to deposit count 2
# NEW_DEPOSIT_COUNT, NEW_FRONTIER, NEXT_LEAF, and PROOF must be computed off-chain
NEW_DEPOSIT_COUNT=2
NEW_FRONTIER="[0x...,0x...,...]"  # 32-element bytes32 array
NEXT_LEAF="0x..."                  # leaf hash at position newDepositCount
PROOF="[0x...,0x...,...]"         # 32-element bytes32 Merkle proof

cast send $BRIDGE_L2_ADDR \
  "backwardLET(uint256,bytes32[32],bytes32,bytes32[32])" \
  $NEW_DEPOSIT_COUNT \
  "$NEW_FRONTIER" \
  $NEXT_LEAF \
  "$PROOF" \
  --private-key $GER_REMOVER_PK \
  --rpc-url $L2_RPC_URL

# Verify the rollback
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL
# Expected: 2
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL
# Should match the LER at deposit count 2
```

#### Step 4: Advance the LET (`forwardLET`)

Call `forwardLET` to add the required leaves. This includes:
- The divergent leaf(s) settled on L1 (BX, BY, ...)
- If a `backwardLET` was performed in Step 3, the legitimate L2 bridges that were rolled back (B3, B4, ...)

The leaves must be passed as an array of `LeafData` structs **in the correct order**: divergent leaves first, then the re-added legitimate L2 bridges.

The `expectedLER` is the expected Merkle root after all leaves are inserted. It acts as a health check — if the computed root doesn't match, the transaction reverts.

```bash
# Build the leaf data array.
# Each leaf is a tuple: (leafType, originNetwork, originAddress, destinationNetwork, destinationAddress, amount, metadata)
#
# Example for Case 2: insert BX (divergent), then B3 and B4 (legitimate)
# The leaf data comes from the diagnosis phase (Step 5 and Step 6 above)

EXPECTED_LER="0x..."  # the expected LER after all leaves are inserted

cast send $BRIDGE_L2_ADDR \
  "forwardLET((uint8,uint32,address,uint32,address,uint256,bytes)[],bytes32)" \
  "[(0,1,0xOrigAddr1,2,0xDestAddr1,1000000000000000000,0x),(0,1,0xOrigAddr2,3,0xDestAddr2,2000000000000000000,0x),(0,1,0xOrigAddr3,3,0xDestAddr3,500000000000000000,0x)]" \
  $EXPECTED_LER \
  --private-key $GER_REMOVER_PK \
  --rpc-url $L2_RPC_URL

# Verify the new state
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL
# The root should match EXPECTED_LER
```

**Computing `expectedLER`**: This is the Merkle root you expect after inserting all the leaves. It must be computed off-chain from the full leaf set. For **Cases 1 and 3** (forward-only), the expected LER after inserting all missing leaves should match the L1 settled LER if you're inserting exactly the leaves that were settled. For **Cases 2 and 4** (backward + forward), the expected LER must account for both the divergent leaves and the re-added legitimate leaves.

#### Step 5: Deactivate emergency mode

```bash
# Deactivate emergency state (requires emergencyBridgeUnpauser key)
cast send $BRIDGE_L2_ADDR "deactivateEmergencyState()" \
  --private-key $EMERGENCY_UNPAUSER_PK \
  --rpc-url $L2_RPC_URL

# Confirm deactivation
cast call $BRIDGE_L2_ADDR "isEmergencyState()(bool)" --rpc-url $L2_RPC_URL
# Expected: false
```

#### Step 6: Rebalance the chain (if needed)

The bridge will be **undercollateralized** by the sum of amounts of all divergent leaves (BX, BY, ...). The AggLayer tracks a Local Balance Tree (LBT) for each chain, and if the LBT shows a negative balance, the next certificate will be rejected.

Check whether rebalancing is urgent by computing the total amount of divergent leaves:

```bash
# Sum of amounts of all divergent leaves (BX, BY, ...)
# If this amount is significant, rebalancing must happen BEFORE starting the aggsender.

# Rebalancing steps:
# 1. Bridge the required amount from another network (LX) into this chain
# 2. Claim the bridge on L2
# 3. Burn the claimed amount on L2
#
# These are standard bridge operations and depend on the specific token and network involved.
```

#### Step 7: Start the `aggsender`

Once the LET is corrected and rebalancing is complete (if needed), restart the `aggsender`:

```bash
# Start the aggsender process/container
# Example for docker:
docker start aggsender

# Example for systemd:
sudo systemctl start aggsender
```

After starting, the `aggsender` must craft a certificate covering the block range that includes the `BackwardLET` and `ForwardLET` events. Monitor its logs to verify:

```bash
# Watch for successful certificate submission
# Look for log lines indicating successful certificate send
# and absence of the error patterns listed in the Detection section
```

The `aggsender` handles `BackwardLET` events (removing leaves from its internal DB) and `ForwardLET` events (adding leaves to its internal DB) automatically.

#### Post-recovery verification

After the `aggsender` resumes and submits a new certificate, verify everything is in sync:

```bash
# 1. Check that the latest certificate is settled (not InError)
grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID, \"type\": \"LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED\"}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetLatestCertificateHeader

# 2. Verify L2 LER matches what AggLayer expects
grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetNetworkInfo

cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL
# These should be consistent

# 3. Check bridge service sync status
curl -s "$BRIDGE_SERVICE_URL/sync-status" | jq .

# 4. Verify no pending InError certificates
grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID, \"type\": \"LATEST_CERTIFICATE_REQUEST_TYPE_PENDING\"}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetLatestCertificateHeader
```

### Cases

The key factor determining the recovery steps is not just the root cause of the divergence, but the **combination of events that occurred after the LET diverged**. Specifically:

- Did further bridges occur on L2 after the divergence point?
- Did further settlements occur on L1 after the first invalid one?

The following scenarios use this notation:

```
L2: B1 -> LET_1, B2 -> LET_2, B3 -> LET_3, B4 -> LET_4
L1: B1 -> LET_1, B2 -> LET_2, BX -> LET_X
                                 ^ divergence point
```

Where `B1..B4` are bridge events, `BX` is a divergent leaf (settled on L1 but not matching L2), and `LET_N` is the LET root after leaf N.

---

#### Case 1: Divergence with no further L2 bridges and no further L1 settlements

**Scenario**: A single divergent leaf was settled on L1, no additional bridges have occurred on L2 since, and no further settlements have been made on L1.

```
L2: B1 -> LET_1, B2 -> LET_2
L1: B1 -> LET_1, B2 -> LET_2, BX -> LET_X
```

**Diagnosis check**:

```bash
# Confirm: L2 deposit count == L1 divergence point (e.g., 2)
# L1 settled deposit count == divergence point + number of divergent leaves (e.g., 3)
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL
# Expected: 2

grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetNetworkInfo
# settled_let_leaf_count expected: 3
```

**Recovery steps**:

```bash
# 1. Stop the aggsender
# 2. Activate emergency state
cast send $BRIDGE_L2_ADDR "activateEmergencyState()" \
  --private-key $EMERGENCY_PAUSER_PK --rpc-url $L2_RPC_URL

# 3. forwardLET — add BX to match L1
#    BX leaf data must be obtained from the settled certificate (see Diagnosis Step 6)
cast send $BRIDGE_L2_ADDR \
  "forwardLET((uint8,uint32,address,uint32,address,uint256,bytes)[],bytes32)" \
  "[(BX_LEAF_TYPE,BX_ORIGIN_NET,BX_ORIGIN_ADDR,BX_DEST_NET,BX_DEST_ADDR,BX_AMOUNT,BX_METADATA)]" \
  $LET_X \
  --private-key $GER_REMOVER_PK --rpc-url $L2_RPC_URL

# 4. Verify
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL  # Expected: 3
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL       # Expected: LET_X

# 5. Deactivate emergency state
cast send $BRIDGE_L2_ADDR "deactivateEmergencyState()" \
  --private-key $EMERGENCY_UNPAUSER_PK --rpc-url $L2_RPC_URL

# 6. (Optional) Re-collateralize, then start the aggsender
```

This is the simplest case: no backward operation is needed since L2 has no extra leaves beyond the divergence point.

**Collateralization**: The bridge is **undercollateralized** by `amount(BX)` — L1 has credited those assets as having left L2, but they were never actually burned on L2.

**Optional re-collateralization steps**:

1. Bridge `amount(BX)` from another network into this chain
2. Claim the bridged funds on L2
3. Burn the claimed amount on L2

This realigns the LBT on L2 with the LBT tracked by the AggLayer node. If the amount is significant, this must be done before starting the `aggsender` (step 6 above), as the AggLayer will reject the next certificate if the LBT shows a negative balance.

---

#### Case 2: Divergence with further L2 bridges but no further L1 settlements

**Scenario**: After the divergent leaf was settled on L1, additional bridges happened on L2 (but no further settlements occurred on L1).

```
L2: B1 -> LET_1, B2 -> LET_2, B3 -> LET_3, B4 -> LET_4
L1: B1 -> LET_1, B2 -> LET_2, BX -> LET_X
```

L2 has leaves B3 and B4 that were added after the divergence point. These must be removed, the divergent leaf inserted, and then the legitimate leaves re-added.

**Diagnosis check**:

```bash
# L2 has more deposits than the divergence point
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL
# Expected: 4 (divergence point 2 + 2 extra L2 bridges)

grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetNetworkInfo
# settled_let_leaf_count expected: 3 (divergence point 2 + 1 divergent leaf)

# Collect leaf data for B3 and B4 (the L2 bridges to re-add)
curl -s "$BRIDGE_SERVICE_URL/bridge-by-deposit-count?network_id=$NETWORK_ID&deposit_count=3" | jq .
curl -s "$BRIDGE_SERVICE_URL/bridge-by-deposit-count?network_id=$NETWORK_ID&deposit_count=4" | jq .
```

**Recovery steps**:

```bash
# 1. Stop the aggsender
# 2. Activate emergency state
cast send $BRIDGE_L2_ADDR "activateEmergencyState()" \
  --private-key $EMERGENCY_PAUSER_PK --rpc-url $L2_RPC_URL

# 3. backwardLET — roll back to deposit count 2 (removing B3 and B4)
#    NEW_FRONTIER, NEXT_LEAF, PROOF must be computed off-chain
cast send $BRIDGE_L2_ADDR \
  "backwardLET(uint256,bytes32[32],bytes32,bytes32[32])" \
  2 \
  "$NEW_FRONTIER" \
  $NEXT_LEAF \
  "$PROOF" \
  --private-key $GER_REMOVER_PK --rpc-url $L2_RPC_URL

# Verify rollback
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL  # Expected: 2

# 4. forwardLET — add BX, then B3, B4 in a single call
cast send $BRIDGE_L2_ADDR \
  "forwardLET((uint8,uint32,address,uint32,address,uint256,bytes)[],bytes32)" \
  "[(BX_LEAF...),(B3_LEAF...),(B4_LEAF...)]" \
  $EXPECTED_LER \
  --private-key $GER_REMOVER_PK --rpc-url $L2_RPC_URL

# Verify
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL  # Expected: 5
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL       # Expected: EXPECTED_LER

# 5. Deactivate emergency state
cast send $BRIDGE_L2_ADDR "deactivateEmergencyState()" \
  --private-key $EMERGENCY_UNPAUSER_PK --rpc-url $L2_RPC_URL

# 6. (Optional) Re-collateralize, then start the aggsender
```

After recovery, the L2 LET will contain: B1, B2, BX, B3, B4 — with the first three matching L1's settled state.

**Collateralization**: Same exposure as Case 1 — the bridge is **undercollateralized** by `amount(BX)`. The legitimate re-added leaves (B3, B4) correspond to real L2 events and do not contribute to undercollateralization.

**Optional re-collateralization steps**:

1. Bridge `amount(BX)` from another network into this chain
2. Claim the bridged funds on L2
3. Burn the claimed amount on L2

This must be done before starting the `aggsender` if the resulting negative LBT balance would cause the next certificate to be rejected.

---

#### Case 3: Divergence with no further L2 bridges but continued L1 settlements

**Scenario**: Multiple settlements have occurred on L1 after the first divergent one, but no additional bridges happened on L2.

```
L2: B1 -> LET_1, B2 -> LET_2
L1: B1 -> LET_1, B2 -> LET_2, BX -> LET_X, BY -> LET_Y
```

**Diagnosis check**:

```bash
# L2 deposit count == divergence point
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL
# Expected: 2

grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetNetworkInfo
# settled_let_leaf_count expected: 4 (divergence point 2 + 2 divergent leaves)
```

**Recovery steps**:

```bash
# 1. Stop the aggsender
# 2. Activate emergency state
cast send $BRIDGE_L2_ADDR "activateEmergencyState()" \
  --private-key $EMERGENCY_PAUSER_PK --rpc-url $L2_RPC_URL

# 3. forwardLET — add BX and BY to match L1
cast send $BRIDGE_L2_ADDR \
  "forwardLET((uint8,uint32,address,uint32,address,uint256,bytes)[],bytes32)" \
  "[(BX_LEAF...),(BY_LEAF...)]" \
  $LET_Y \
  --private-key $GER_REMOVER_PK --rpc-url $L2_RPC_URL

# Verify
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL  # Expected: 4
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL       # Expected: LET_Y

# 4. Deactivate emergency state
cast send $BRIDGE_L2_ADDR "deactivateEmergencyState()" \
  --private-key $EMERGENCY_UNPAUSER_PK --rpc-url $L2_RPC_URL

# 5. Re-collateralize (URGENT), then start the aggsender
```

No backward operation is needed since L2 has no extra leaves. The `forwardLET` call can batch-insert all missing leaves in a single transaction.

**Collateralization**: The bridge is **undercollateralized** by `amount(BX) + amount(BY)`. This is the most collateralization-sensitive case among those with no backward step, as multiple bad settlements have accumulated.

**Optional re-collateralization steps**:

1. Bridge `amount(BX) + amount(BY)` from another network into this chain
2. Claim the bridged funds on L2
3. Burn the claimed amount on L2

This is **urgent** — the AggLayer will reject the next certificate if the LBT shows a negative balance, so this must be done before starting the `aggsender`.

---

#### Case 4: Divergence with both further L2 bridges and continued L1 settlements

**Scenario**: This is the most complex case. After the divergence, both additional bridges occurred on L2 and additional settlements were made on L1.

```
L2: B1 -> LET_1, B2 -> LET_2, B3 -> LET_3, B4 -> LET_4
L1: B1 -> LET_1, B2 -> LET_2, BX -> LET_X, BY -> LET_Y
```

L2 has extra leaves (B3, B4) and L1 has settled additional leaves (BX, BY) beyond the divergence point.

**Diagnosis check**:

```bash
# L2 has more deposits than the divergence point
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL
# Expected: 4

grpcurl -plaintext -d "{\"network_id\": $NETWORK_ID}" \
  $AGGLAYER_GRPC \
  agglayer.node.v1.NodeStateService/GetNetworkInfo
# settled_let_leaf_count expected: 4 (2 matching + 2 divergent)

# The LERs will differ
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL
# L2 root != L1 settled_ler, even though deposit counts may match

# Collect leaf data for B3 and B4
curl -s "$BRIDGE_SERVICE_URL/bridge-by-deposit-count?network_id=$NETWORK_ID&deposit_count=3" | jq .
curl -s "$BRIDGE_SERVICE_URL/bridge-by-deposit-count?network_id=$NETWORK_ID&deposit_count=4" | jq .
```

**Recovery steps**:

```bash
# 1. Stop the aggsender
# 2. Activate emergency state
cast send $BRIDGE_L2_ADDR "activateEmergencyState()" \
  --private-key $EMERGENCY_PAUSER_PK --rpc-url $L2_RPC_URL

# 3. backwardLET — roll back to deposit count 2 (removing B3 and B4)
cast send $BRIDGE_L2_ADDR \
  "backwardLET(uint256,bytes32[32],bytes32,bytes32[32])" \
  2 \
  "$NEW_FRONTIER" \
  $NEXT_LEAF \
  "$PROOF" \
  --private-key $GER_REMOVER_PK --rpc-url $L2_RPC_URL

# Verify rollback
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL  # Expected: 2

# 4. forwardLET — add BX, BY (divergent), then B3, B4 (legitimate) in a single call
cast send $BRIDGE_L2_ADDR \
  "forwardLET((uint8,uint32,address,uint32,address,uint256,bytes)[],bytes32)" \
  "[(BX_LEAF...),(BY_LEAF...),(B3_LEAF...),(B4_LEAF...)]" \
  $EXPECTED_LER \
  --private-key $GER_REMOVER_PK --rpc-url $L2_RPC_URL

# Verify
cast call $BRIDGE_L2_ADDR "depositCount()(uint256)" --rpc-url $L2_RPC_URL  # Expected: 6
cast call $BRIDGE_L2_ADDR "getRoot()(bytes32)" --rpc-url $L2_RPC_URL       # Expected: EXPECTED_LER

# 5. Deactivate emergency state
cast send $BRIDGE_L2_ADDR "deactivateEmergencyState()" \
  --private-key $EMERGENCY_UNPAUSER_PK --rpc-url $L2_RPC_URL

# 6. Re-collateralize (URGENT), then start the aggsender
```

After recovery, the L2 LET will contain: B1, B2, BX, BY, B3, B4 — with the first four matching L1's settled state.

**Collateralization**: The bridge is **undercollateralized** by `amount(BX) + amount(BY)`. This is the worst-case scenario: multiple bad settlements on L1 combined with legitimate L2 bridge activity. The legitimate re-added leaves (B3, B4) correspond to real L2 events and do not add to the undercollateralization.

**Optional re-collateralization steps**:

1. Bridge `amount(BX) + amount(BY)` from another network into this chain
2. Claim the bridged funds on L2
3. Burn the claimed amount on L2

This must be done before starting the `aggsender`. Given that multiple invalid settlements have occurred, this is the case where the negative LBT balance is most likely to block the very next certificate.

---

#### Important considerations across all cases
- **Re-collateralization**: The bridge will always be undercollateralized after recovery by the sum of amounts of all divergent leaves. Re-collateralization (bridge from another chain -> claim on L2 -> burn) must be completed before starting the `aggsender` whenever the resulting negative LBT balance would cause the next certificate to be rejected. See each case above for the specific amounts involved.
- **Stop aggsender first**: Always stop the `aggsender` before starting any recovery operations and only start it again after everything is complete (including deactivating emergency mode and re-collateralizing if needed).
- **Certificate crafting**: After recovery, the `aggsender` must craft a certificate that covers the block range containing all the `BackwardLET` and `ForwardLET` events. The certificate's initial block must be correct and all events in the range must be included.
- **Event parsing**: The `aggsender` must correctly handle `BackwardLET` events (removing leaves from its DB) and `ForwardLET` events (adding leaves to its DB) to maintain internal consistency.
- **Single `forwardLET` call**: Since `forwardLET` accepts an array of leaves, the divergent leaves and the re-added legitimate bridges should be combined into a single call when possible (e.g., `forwardLET([BX, B3, B4], ...)`), reducing the number of transactions.
- **Order of operations matters**: The `backwardLET` must always come before `forwardLET` when both are needed, since `backwardLET` requires the current tree state to compute valid Merkle proofs. After a `forwardLET`, the tree state has changed and any previously computed proofs for `backwardLET` would be invalid.

## Appendix: API and gRPC reference

### AggLayer gRPC — `NodeStateService`

**Proto package**: `agglayer.node.v1`

| RPC Method | Description | Key response fields |
|------------|-------------|---------------------|
| `GetNetworkInfo` | Current network state and settlement info | `settled_ler`, `settled_let_leaf_count`, `settled_height`, `settled_certificate_id`, `network_status` |
| `GetLatestCertificateHeader` | Latest certificate (settled or pending) | `prev_local_exit_root`, `new_local_exit_root`, `height`, `status`, `error` |
| `GetCertificateHeader` | Specific certificate by ID | Same as above |

**`CertificateStatus` enum values**: `PENDING` (1), `PROVEN` (2), `CANDIDATE` (3), `IN_ERROR` (4), `SETTLED` (5)

**`LatestCertificateRequestType` enum values**: `LATEST_CERTIFICATE_REQUEST_TYPE_SETTLED`, `LATEST_CERTIFICATE_REQUEST_TYPE_PENDING`

### Bridge Service REST API

**Base path**: `/bridge/v1`

| Endpoint | Method | Key params | Description |
|----------|--------|------------|-------------|
| `/bridge-by-deposit-count` | GET | `network_id`, `deposit_count` | Get a single bridge by deposit count and network |
| `/bridges` | GET | `network_id`, `page_number`, `page_size` | Paginated list of bridges for a network |
| `/sync-status` | GET | — | Compare on-chain vs synced deposit counts |
| `/claim-proof` | GET | `network_id`, `leaf_index`, `deposit_count` | Merkle proofs for local and rollup exit roots |
| `/l1-info-tree-index` | GET | `network_id`, `deposit_count` | First L1 info tree index after a deposit count |

### Smart contract view functions (`AgglayerBridgeL2`)

| Function | Returns | Description |
|----------|---------|-------------|
| `depositCount()` | `uint256` | Current number of deposits in the LET |
| `getRoot()` | `bytes32` | Current Merkle root (LER) of the LET |
| `isEmergencyState()` | `bool` | Whether emergency mode is active |
| `networkID()` | `uint32` | Network ID of this L2 chain |
| `emergencyBridgePauser()` | `address` | Account that can activate emergency state |
| `emergencyBridgeUnpauser()` | `address` | Account that can deactivate emergency state |

### Smart contract view functions (`AgglayerGERL2`)

| Function | Returns | Description |
|----------|---------|-------------|
| `globalExitRootRemover()` | `address` | Account that can call `backwardLET`/`forwardLET` |
| `globalExitRootUpdater()` | `address` | Account that can insert global exit roots |
