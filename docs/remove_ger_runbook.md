# Remove GER and Unset claims runbook

This runbook provides instructions on how to recover a network in the case an invalid GER has been injected, and potentially one or more claims have been done using the invalid GER

## Detection

This section will detail how to identify the issue

### Invalid GER injected detection

#### AggSender logs

The aggsender will fail to generate certificates when an invalid GER has been injected. Look for these error patterns:

**1. Merkle proof generation failure (GER doesn't exist on L1)**

Location: [aggsender/query/ger_query.go:83](https://github.com/agglayer/aggkit/blob/develop/aggsender/query/ger_query.go#L83)
```
error getting proof for GER: <GER_HASH>: ...
```

Location: [aggsender/converters/imported_bridge_exit_converter.go:100](https://github.com/agglayer/aggkit/blob/develop/aggsender/converters/imported_bridge_exit_converter.go#L100)
```
error getting L1 Info tree merkle proof for GER: <GER_HASH> and root: <ROOT_HASH>. Error: ...
```

Location: [aggsender/query/l1info_tree_data_query.go:143](https://github.com/agglayer/aggkit/blob/develop/aggsender/query/l1info_tree_data_query.go#L143)
```
error getting info by global exit root: ...
```

**2. Certificate validation/sending failure**

Location: [aggsender/aggsender.go:460](https://github.com/agglayer/aggkit/blob/develop/aggsender/aggsender.go#L460)
```
error sending certificate: ...
```

Location: [aggsender/validator/local_validator.go:93-94](https://github.com/agglayer/aggkit/blob/develop/aggsender/validator/local_validator.go#L93-L94)
```
certificate validation failed: <ERROR_DETAILS>. Cert: <CERT_BRIEF>
```

These errors occur when aggsender attempts to build a certificate that includes an invalid GER. The certificate cannot be sent to agglayer because the merkle proof for the GER cannot be generated (the GER doesn't exist in the L1 info tree).

#### L2 GER Sync

Location: [l2gersync/evm_downloader_sovereign.go:147-182](https://github.com/agglayer/aggkit/blob/develop/l2gersync/evm_downloader_sovereign.go#L147-L182)

The l2gersync component detects invalid GERs during synchronization when it tries to fetch the L1 info tree data for a GER injected into L2. Look for these error patterns:

**1. Failed to fetch L1 info tree for GER**
```
failed to fetch l1 info tree for global exit root <GER_HASH>: ...
```

**2. GER not found in L1 contract**
```
GER <GER_HASH> not found in L1 contract globalExitRootMap
```

**3. GER lookup failed in L1 contract**
```
GER lookup for <GER_HASH> failed in L1 contract: ...
```

These errors are logged on **every retry** while an invalid GER is present — the direct `globalExitRootMap` lookup against the L1 contract is an informational-only log line from here on; it does not gate the recovery decision, which is entirely L2-side — see "Blocking and automatic recovery" below.

#### Blocking and automatic recovery

Once l2gersync hits an invalid GER (an `UpdateHashChainValue` insert event whose GER cannot be found in the local L1 info tree), **it blocks**: it does not skip the block, does not crash on its own, and retries the same block, logging the error patterns above on every attempt. This goes through the same `RetryHandler` as any other appender error: with `MaxRetryAttemptsAfterError` set to `-1` (the default), it retries indefinitely; with a positive value configured, l2gersync will `Fatal` once that many consecutive attempts are exhausted, same as it would for any other sync error. It is safe to leave l2gersync running in this state while performing the "Remove GER" step below — no restart or config change is required, as long as the removal happens before the configured retry budget (if any) runs out.

Once the operator calls `removeGlobalExitRoots` (step 2 of the Reaction section), **l2gersync recovers automatically** on its next retry of the blocked insert: it re-checks two L2-only signals, both read at the current L2 head, and only skips the stale insert when BOTH agree the GER was actually removed:

- a durable `UpdateRemovalHashChainValue` removal event for that GER, scanned from the insert block onward, exists (≥ 1 match); AND
- the L2 `globalExitRootMap` entry for that GER currently reads `0`.

Requiring both guards against two false-unstick scenarios: a transient/stale zero-map read with no real removal event (first condition fails), and a removal that gets reversed by re-injection before the retry runs (second condition fails). Once both agree, l2gersync skips the stale insert and resumes normal processing from the removal-event block onward.

**Verifying recovery:** poll `GET /bridge/v1/sync-status` on the L2 bridgeservice and watch `l2_ger_info.last_processed_block` (see [Bridge service component](./bridge_service.md#sync-status)). While blocked it stays pinned at (or just below) the invalid insert's block even as the L2 chain head keeps advancing; once recovery kicks in it advances past the `removeGlobalExitRoots` transaction's block, confirming l2gersync is unstuck and caught back up.

#### Preventive detection

WIP, TBD, code not ready

### Claims

This section provides instructions on how to identify claims that have been made using an invalid GER. Once you've detected an invalid GER using the methods described in the "Detection" section, you can use these queries to find all associated claims.

#### Using the remove-ger scan command

If you do not yet know which GERs were used by invalid claims, you can use the `remove_ger scan-invalid-claims` command to scan claim logs directly from L2 RPC and validate each claim GER against L1:

```bash
./remove_ger scan-invalid-claims --cfg aggkit-config.toml --from-block <START_BLOCK>
```

The command:

- reads claim logs from the L2 bridge contract using the L2 RPC
- computes or extracts the GER used by each claim
- checks whether that GER exists in the L1 `globalExitRootMap`
- prints the GERs that were used by invalid claims, together with claim counts and tx hashes

Use this when:

- you know the approximate block range where the invalid claims happened, but not the GER
- bridge-service indexing is unavailable or you want to validate directly from chain RPC
- you want a fast first pass before running SQL queries or manual classification

#### Query claims by Global Exit Root (GER)

Claims are stored in the `bridgesync` database (typically at the path configured in `L2BridgeSyncStoragePath`). Each claim record includes the `global_exit_root` field, which indicates which GER was used when the claim was executed.

**Direct SQL Query:**

```sql
-- Find all claims that used a specific GER
SELECT
    block_num,
    block_pos,
    tx_hash,
    global_index,
    origin_network,
    origin_address,
    destination_address,
    amount,
    global_exit_root,
    mainnet_exit_root,
    rollup_exit_root,
    destination_network,
    metadata,
    is_message,
    block_timestamp,
    type
FROM claim
WHERE global_exit_root = '<INVALID_GER_HASH>'
ORDER BY block_num ASC, block_pos ASC;
```

Replace `<INVALID_GER_HASH>` with the hash of the invalid GER you detected (e.g., `0x1234...abcd`).

**Using sqlite3 CLI:**

```bash
# Connect to the bridgesync database
sqlite3 /path/to/bridgesync.db

# Query claims for the invalid GER
SELECT * FROM claim WHERE global_exit_root = '0x<INVALID_GER_HASH>';

# Get count of affected claims
SELECT COUNT(*) FROM claim WHERE global_exit_root = '0x<INVALID_GER_HASH>';
```

**Important Notes:**

1. **GER Computation**: The `global_exit_root` is computed as `keccak256(mainnet_exit_root, rollup_exit_root)`. You can verify the GER by checking these component roots in the claim record.

2. **Claim Compaction**: The bridgesync database uses claim compaction logic. Multiple claim events with the same `global_index` may be compacted into a single record. If you need to see all historical claim events (including unset/set operations), also query the `unset_claim` and `set_claim` tables:

```sql
-- Check if this claim was ever unset
SELECT * FROM unset_claim WHERE global_index = '<CLAIM_GLOBAL_INDEX>';

-- Check if this claim was explicitly set
SELECT * FROM set_claim WHERE global_index = '<CLAIM_GLOBAL_INDEX>';
```

3. **Block Range Query**: If you know the block range where the invalid GER was active, you can narrow your search:

```sql
SELECT * FROM claim
WHERE global_exit_root = '0x<INVALID_GER_HASH>'
  AND block_num >= <FROM_BLOCK>
  AND block_num <= <TO_BLOCK>;
```

4. **Cross-reference with L2 GER Sync**: You can verify which GERs were injected into L2 during a specific period by querying the l2gersync database:

```sql
-- Connect to l2gersync database
sqlite3 /path/to/l2gersync.db

-- Get all injected GERs in the affected block range
SELECT
    global_exit_root,
    l1_info_tree_index,
    block_num,
    block_pos
FROM imported_global_exit_root
WHERE block_num >= <FROM_BLOCK>
  AND block_num <= <TO_BLOCK>
ORDER BY block_num ASC;
```

#### Claim classification

Once we've detected the conflictive claims (if any), it's important to classify each claim under one of the following categories. This classification determines the recovery strategy needed.

##### A

**Claims that used the invalid GER and resulted in asset under-collateralization.**

This occurs when:
- The bridge doesn't exist on L1 at the claimed deposit_count, OR
- The bridge exists but with different content (different asset, amount, or destination)

**Impact**: L2 bridge is under-collateralized because assets were released based on a bridge that never happened or was different on L1.

##### B.1

**Claims with the same indexes but different GER (GER changed, indexes remain the same).**

This occurs when:
- The bridge exists on L1 with identical content
- The deposit_count (and thus global_index) is the same
- But the GER components (mainnet_exit_root or rollup_exit_root) are different

**Impact**: The bridge is balanced, but the claim data references an incorrect GER. Need to update claim metadata.

##### B.2

**Claims with different indexes and different GER (both GER and indexes changed).**

This occurs when:
- The bridge content exists on L1 but at a different deposit_count
- Both the global_index and GER are different
- The actual bridge leaf (asset, amount, destination) is the same

**Impact**: The bridge is balanced, but both the index and GER need correction.

##### Steps to identify category of the claim

**Step 1: Decode the Claim's Global Index**

Each claim has a `global_index` that encodes three components:
- `mainnetFlag`: Whether the origin network is mainnet (true) or a rollup (false)
- `rollupIndex`: If rollup, this is `origin_network - 1`
- `depositCount`: The local deposit index on the origin network

You can decode this using a Python script or cast:

```python
# Python script to decode global_index
def decode_global_index(global_index_hex):
    # Convert hex to int
    global_index = int(global_index_hex, 16)

    # Check mainnet flag (bit 64)
    mainnet_flag = (global_index >> 64) & 1 == 1

    # Extract deposit_count (last 4 bytes)
    deposit_count = global_index & 0xFFFFFFFF

    # Extract rollup_index (middle 4 bytes)
    rollup_index = (global_index >> 32) & 0xFFFFFFFF

    if mainnet_flag:
        origin_network = 0
    else:
        origin_network = rollup_index + 1

    return {
        'origin_network': origin_network,
        'deposit_count': deposit_count,
        'mainnet_flag': mainnet_flag,
        'rollup_index': rollup_index
    }

# Example usage
claim_global_index = "0x0100000000000003"  # Replace with actual value
result = decode_global_index(claim_global_index)
print(f"Origin Network: {result['origin_network']}")
print(f"Deposit Count: {result['deposit_count']}")
```

---

**Step 2: Query the Origin Network Bridge**

Once you have the `origin_network` and `deposit_count`, you need to query the bridge on the origin network. The approach depends on whether the origin is L1 or another L2.

**Case A: Origin Network is L1 (origin_network = 0)**

Query the L1 bridge database (l1bridgesync):

```sql
-- Connect to L1 bridgesync database
sqlite3 /path/to/l1bridgesync.db

-- Query the bridge with the decoded deposit_count
SELECT
    deposit_count,
    leaf_type,
    origin_network,
    origin_address,
    destination_network,
    destination_address,
    amount,
    metadata,
    block_num
FROM bridge
WHERE deposit_count = <DEPOSIT_COUNT>
  AND origin_network = 0;  -- L1 is always 0
```

If this query returns **no results**, the bridge doesn't exist on L1 → **Category A**

**Case B: Origin Network is Another L2 (origin_network > 0)**

When the origin is another L2, you don't have access to that L2's bridgesync database. You must use cast commands to query that L2's RPC:

```bash
# Set variables for the origin L2
ORIGIN_L2_RPC="<ORIGIN_L2_RPC_URL>"
ORIGIN_L2_BRIDGE_ADDR="<ORIGIN_L2_BRIDGE_CONTRACT_ADDRESS>"
DEPOSIT_COUNT=<DEPOSIT_COUNT>

# Query the current deposit count on origin L2
cast call $ORIGIN_L2_BRIDGE_ADDR \
  "depositCount()(uint256)" \
  --rpc-url $ORIGIN_L2_RPC

# If the returned deposit count is less than or equal to DEPOSIT_COUNT,
# the bridge doesn't exist yet → Category A
```

**Important Note for Another L2 Origin:**

Without access to the origin L2's bridgesync database or specialized query endpoints, you **cannot directly verify the bridge content** (leaf_type, addresses, amount, metadata). The bridge contracts don't expose a function to query individual bridges by deposit_count.

**Practical Options:**
1. **Use Bridge Service API**: If the origin L2 is running the aggkit bridge service, you can query bridge data via its API endpoints:
   ```bash
   # Query bridges from origin L2 bridge service
   ORIGIN_L2_BRIDGE_API="<ORIGIN_L2_BRIDGE_SERVICE_URL>"
   ORIGIN_NETWORK_ID="<ORIGIN_L2_NETWORK_ID>"
   DEPOSIT_COUNT="<DEPOSIT_COUNT>"

   # Get bridge details by deposit count
   curl "$ORIGIN_L2_BRIDGE_API/bridges/v1/bridges?networkId=$ORIGIN_NETWORK_ID&limit=1&offset=$DEPOSIT_COUNT"
   ```

   The response will include bridge details (origin_address, destination_network, destination_address, amount, metadata, leaf_type) that you can compare with the L2 claim data. See the [Bridge Service API documentation](https://github.com/agglayer/aggkit/blob/develop/docs/bridge_service.md) for more details.

2. **Request database access**: Contact the origin L2 operator for read access to their bridgesync database

3. **Use block explorer**: If available, use the origin L2's block explorer to find the BridgeEvent at the specific deposit_count

4. **Assume Category A**: If verification is not possible and GER is invalid, treat as Category A (most conservative approach)

For the remainder of these instructions, we'll focus on **Case A (L1 origin)**, as that's the scenario where you have database access. If origin is another L2, skip to Step 4 for GER verification.

---

**Step 3: Compare Bridge Content (for Categories A vs B) - L1 Origin Only**

If the bridge exists on L1, compare the leaf content between L2 claim and L1 bridge:

```sql
-- L2 Claim (from l2bridgesync.db)
SELECT
    origin_network,
    origin_address,
    destination_address,
    amount,
    metadata,
    is_message  -- determines leaf_type: false=Asset(0), true=Message(1)
FROM claim
WHERE global_index = '<CLAIM_GLOBAL_INDEX>';

-- L1 Bridge (from l1bridgesync.db) - already queried above
```

**Compare these fields:**
1. `leaf_type` (L1) vs `is_message` (L2): `leaf_type` should be 0 if `is_message=false`, 1 if `is_message=true`
2. `origin_network`
3. `origin_address`
4. `destination_network`
5. `destination_address`
6. `amount`
7. `metadata`

**If any field differs** → **Category A** (bridge content mismatch, under-collateralization)

**If all fields match** → Proceed to Step 4

---

**Step 4: Compare GER Components (for Categories B.1 vs B.2)**

Query the L1InfoTree to get the correct GER for the bridge's block:

```sql
-- Connect to l1infotreesync database
sqlite3 /path/to/l1infotreesync.db

-- Get L1InfoTree leaves around the time the L1 bridge happened
SELECT
    position as l1_info_tree_index,
    block_num,
    mainnet_exit_root,
    rollup_exit_root,
    global_exit_root,
    timestamp
FROM l1info_leaf
WHERE block_num >= <L1_BRIDGE_BLOCK_NUM>
ORDER BY block_num ASC, block_pos ASC
LIMIT 10;
```

Now compare with the L2 claim's GER:

```sql
-- L2 Claim GER components (from l2bridgesync.db)
SELECT
    mainnet_exit_root,
    rollup_exit_root,
    global_exit_root,
    block_num as l2_claim_block_num
FROM claim
WHERE global_index = '<CLAIM_GLOBAL_INDEX>';
```

**Classification:**

1. **If `global_index` matches (same deposit_count) AND bridge content matches:**
   - Check if `mainnet_exit_root` and `rollup_exit_root` match between L1 and L2
   - **If GERs differ** → **Category B.1**
   - **If GERs match** → No issue (should not happen with invalid GER)

2. **If bridge content matches but `deposit_count` is different:**
   - The same bridge leaf exists on L1 but at a different deposit_count
   - This means the global_index is different
   - **Category B.2**

---

**Step 5: Use cast to verify GER on L1 (Works for All Origin Networks)**

You can verify the GER directly from L1 contracts regardless of the origin network:

```bash
# Get the GER from L1 GlobalExitRoot contract
L1_GER_ADDR="<GLOBAL_EXIT_ROOT_L1_ADDR>"
L1_RPC="<L1_RPC_URL>"
GER_HASH="<GER_FROM_L2_CLAIM>"

cast call $L1_GER_ADDR \
  "globalExitRootMap(bytes32)(uint256)" \
  $GER_HASH \
  --rpc-url $L1_RPC

# If result is 0, the GER doesn't exist on L1
# If result is non-zero, it's the timestamp when the GER was created on L1
```

**Alternative: Query Other L2's GER contract**

If the origin is another L2, you can also check their local GER contract:

```bash
# Query origin L2's GER contract
ORIGIN_L2_GER_ADDR="<ORIGIN_L2_GER_CONTRACT_ADDRESS>"
ORIGIN_L2_RPC="<ORIGIN_L2_RPC_URL>"

cast call $ORIGIN_L2_GER_ADDR \
  "globalExitRootMap(bytes32)(uint256)" \
  $GER_HASH \
  --rpc-url $ORIGIN_L2_RPC
```

---

**Step 6: Verify using L1InfoTree Index**

For claims that reference specific L1InfoTree indices, you can cross-reference:

```sql
-- Check if the L1InfoTree index from L2 claim exists on L1
-- L2 Claim references this index via the proof
SELECT
    position as l1_info_tree_index,
    global_exit_root,
    mainnet_exit_root,
    rollup_exit_root,
    block_num
FROM l1info_leaf
WHERE global_exit_root = '<L2_CLAIM_GER>';

-- If no results, the GER doesn't exist on L1 at all
```

---

**Summary Decision Tree:**

**For L1 Origin (origin_network = 0):**
```
1. Does bridge exist on L1 at deposit_count?
   NO → Category A (bridge doesn't exist)
   YES → Go to 2

2. Does bridge content match (all fields)?
   NO → Category A (bridge content mismatch)
   YES → Go to 3

3. Does global_index match (same deposit_count)?
   YES → Go to 4
   NO → Category B.2 (different index, same content)

4. Do GER components match?
   NO → Category B.1 (same index, different GER)
   YES → Not an invalid claim (shouldn't happen)
```

**For Another L2 Origin (origin_network > 0):**
```
1. Can you verify bridge exists on origin L2?
   NO (no access to origin L2 data) → Assume Category A (most conservative)
   YES → Go to 2

2. Can you verify bridge content matches?
   NO (no database access) → Assume Category A
   YES (have database/explorer access) → Follow L1 decision tree above

3. As fallback: Verify GER exists on L1
   NO → Definitely invalid, likely Category A
   YES → Check with origin L2 operator for classification
```

**Practical Recommendation for Another L2 Origin:**
- If you cannot access the origin L2's bridgesync database, treat all claims with invalid GERs as **Category A**
- This is the safest approach as it assumes under-collateralization until proven otherwise
- Work with the origin L2 operator to get proper classification and coordinate recovery

## Reaction

This section provides step-by-step instructions to recover from invalid GER injection. The recovery process varies depending on the claim classification (A, B.1, or B.2). All steps must be executed on the L2 network.

> **All of those steps can be run programatically using the [remove GER tool](../tools/remove_ger/README.md)**

**Important Notes:**
- All transactions happen on **L2** (not L1)
- Private keys must have specific roles on the L2 smart contracts
- Each step has prerequisites that must be verified before proceeding

---

### Recovery Flow by Claim Category

**Category A** (Under-collateralization):
1. Freeze the bridge → 2. Remove GER → 3. Unset claims → 4. Restore bridge

**Category B.1** (GER mismatch only):
1. Freeze the bridge → Remove GER → 2. Force emit corrected claim events → 3. Restore bridge

**Category B.2** (Index and GER mismatch):
1. Freeze the bridge → 2. Remove GER → 3. Unset claims → 4. Set claims → 5. Force emit corrected claim events → 6. Restore bridge

**No problematic claims:**
1. Freeze the bridge → 2. Remove GER → 3. Restore bridge

---

### 1. Freeze the bridge

**When to use:** Always.

**Purpose:** Prevent new claims while recovery is in progress.

**Contract:** `BridgeL2SovereignChain` (L2 Bridge Contract)

**Function:** `activateEmergencyState()`

**Required Role:** `emergencyBridgePauser`

This is typically the **sovereign admin** private key. In test environments, this is referred to as `l2_sovereign_admin_private_key`.

**Prerequisites:**
- Verify the invalid GER(s) have been identified
- Verify claims associated with invalid GER(s) have been catalogued
- Ensure you have the sovereign admin private key with `emergencyBridgePauser` role

**Cast Command:**

```bash
# Set environment variables
export L2_RPC_URL="<YOUR_L2_RPC_URL>"
export L2_BRIDGE_ADDR="<L2_BRIDGE_CONTRACT_ADDRESS>"
export SOVEREIGN_ADMIN_PRIVATE_KEY="<SOVEREIGN_ADMIN_PRIVATE_KEY>"

# Activate emergency state
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "activateEmergencyState()"
```

**Expected Outcome:**
- Transaction succeeds with a transaction hash
- Bridge enters emergency state (all bridge operations are paused)
- Event `EmergencyStateActivated()` is emitted

**Verification:**

```bash
# Check if emergency state is active (should return true)
cast call $L2_BRIDGE_ADDR \
  "isEmergencyState()(bool)" \
  --rpc-url $L2_RPC_URL
```

**Reference:**
- Contract: [BridgeL2SovereignChain.sol#L872-L878](https://github.com/agglayer/agglayer-contracts/blob/main/contracts/v2/sovereignChains/BridgeL2SovereignChain.sol#L872-L878)
- Test: [latest-n-injected-ger.bats](https://github.com/agglayer/e2e/blob/main/tests/aggkit/latest-n-injected-ger.bats) (implied in recovery flow)

---

### 2. Remove GER

**When to use:** Always.

**Purpose:** Remove the invalid GER(s) from the L2 GER Manager contract.

**Contract:** `GlobalExitRootManagerL2SovereignChain` (L2 GER Manager Contract)

**Function:** `removeGlobalExitRoots(bytes32[] calldata gersToRemove)`

**Required Role:** `globalExitRootRemover`

This role is queried from the `GlobalExitRootManagerL2SovereignChain` contract. In test environments, this is typically the **sovereign admin** (`l2_sovereign_admin_private_key`).

**Prerequisites:**
- Invalid GER(s) have been identified (from Detection section)
- Bridge must be in emergency state (Step 1 completed)
- Ensure you have the private key with `globalExitRootRemover` role

**Cast Command:**

```bash
# Set environment variables
export L2_RPC_URL="<YOUR_L2_RPC_URL>"
export L2_GER_ADDR="<L2_GER_MANAGER_CONTRACT_ADDRESS>"
export SOVEREIGN_ADMIN_PRIVATE_KEY="<SOVEREIGN_ADMIN_PRIVATE_KEY>"
export INVALID_GER="<INVALID_GER_HASH>"

# Remove single GER
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_GER_ADDR \
  "removeGlobalExitRoots(bytes32[])" \
  "[$INVALID_GER]"

# Remove multiple GERs (if multiple invalid GERs exist)
export INVALID_GER_1="<FIRST_INVALID_GER_HASH>"
export INVALID_GER_2="<SECOND_INVALID_GER_HASH>"

cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_GER_ADDR \
  "removeGlobalExitRoots(bytes32[])" \
  "[$INVALID_GER_1,$INVALID_GER_2]"
```

**Expected Outcome:**
- Transaction succeeds with a transaction hash
- Invalid GER(s) are removed from the contract
- Event `UpdateRemovalHashChainValue(bytes32 indexed removedGlobalExitRoot, bytes32 indexed newRemovalHashChainValue)` is emitted for each removed GER

**Verification:**

```bash
# Check if GER still exists (should return 0 for removed GER)
cast call $L2_GER_ADDR \
  "globalExitRootMap(bytes32)(uint256)" \
  $INVALID_GER \
  --rpc-url $L2_RPC_URL

# Expected output: 0 (GER doesn't exist / timestamp is 0)
```

**Reference:**
- Contract: [GlobalExitRootManagerL2SovereignChain.sol#L191-L220](https://github.com/agglayer/agglayer-contracts/blob/main/contracts/v2/sovereignChains/GlobalExitRootManagerL2SovereignChain.sol#L191-L220)
- Test: [bridge-sovereign-chain-e2e.bats#L288](https://github.com/agglayer/e2e/blob/main/tests/aggkit/bridge-sovereign-chain-e2e.bats#L288) and [latest-n-injected-ger.bats](https://github.com/agglayer/e2e/blob/main/tests/aggkit/latest-n-injected-ger.bats)

---

### 3. Unset claims (optional)

**When to use:** Required for **Category A** and **Category B.2**. Not needed for **Category B.1**.

**Purpose:** Mark claims as "unclaimed" in the bridge contract so they can be either re-claimed with correct data (B.2) or prevented from double-claiming (A).

**Contract:** `BridgeL2SovereignChain` (L2 Bridge Contract)

**Function:** `unsetMultipleClaims(uint256[] memory globalIndexes)`

**Required Role:** `globalExitRootRemover`

This role is queried from the `GlobalExitRootManagerL2SovereignChain` contract (same as Step 2). In test environments, this is the **sovereign admin** (`l2_sovereign_admin_private_key`).

**Prerequisites:**
- Step 2 (Remove GER) must be completed
- Bridge must be in emergency state (Step 1 completed for Category A/B.2)
- Claims to unset have been identified (from Claims section of Detection)
- Have the `global_index` values for each claim to unset

**Cast Command:**

```bash
# Set environment variables
export L2_RPC_URL="<YOUR_L2_RPC_URL>"
export L2_BRIDGE_ADDR="<L2_BRIDGE_CONTRACT_ADDRESS>"
export SOVEREIGN_ADMIN_PRIVATE_KEY="<SOVEREIGN_ADMIN_PRIVATE_KEY>"
export CLAIM_GLOBAL_INDEX_1="<FIRST_CLAIM_GLOBAL_INDEX>"
export CLAIM_GLOBAL_INDEX_2="<SECOND_CLAIM_GLOBAL_INDEX>"

# Unset single claim
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "unsetMultipleClaims(uint256[])" \
  "[$CLAIM_GLOBAL_INDEX_1]"

# Unset multiple claims
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "unsetMultipleClaims(uint256[])" \
  "[$CLAIM_GLOBAL_INDEX_1,$CLAIM_GLOBAL_INDEX_2]"
```

**Expected Outcome:**
- Transaction succeeds with a transaction hash
- Claims are marked as "unclaimed" in the contract
- Event `UnsetClaim(uint256 indexed globalIndex, uint32 indexed originNetwork, uint32 indexed depositCount)` is emitted for each unset claim
- The aggkit bridgesync component will sync these `UnsetClaim` events

**Verification:**

```bash
# Decode global_index to get origin_network and deposit_count
# Use the Python script from "Claim classification" section

# Check if claim is still marked as claimed (should return false after unset)
# Note: You need origin_network and deposit_count from decoded global_index
export ORIGIN_NETWORK="<ORIGIN_NETWORK>"
export DEPOSIT_COUNT="<DEPOSIT_COUNT>"

cast call $L2_BRIDGE_ADDR \
  "isClaimed(uint32,uint32)(bool)" \
  $DEPOSIT_COUNT \
  $ORIGIN_NETWORK \
  --rpc-url $L2_RPC_URL

# Expected output: false
```

**Reference:**
- Contract: [BridgeL2SovereignChain.sol#L553-L594](https://github.com/agglayer/agglayer-contracts/blob/main/contracts/v2/sovereignChains/BridgeL2SovereignChain.sol#L553-L594)
- Test: [latest-n-injected-ger.bats#L230-L247](https://github.com/agglayer/e2e/blob/main/tests/aggkit/latest-n-injected-ger.bats#L230-L247)

---

### 3b. Set claims (optional, Category B.2 only)

**When to use:** Only for **Category B.2** after unsetting claims.

**Purpose:** For Category B.2, the correct bridge exists on L1 but at a different index. After unsetting the incorrect claims, you may need to set the claims with the correct global indexes to maintain the proper claim state.

**Contract:** `BridgeL2SovereignChain` (L2 Bridge Contract)

**Function:** `setMultipleClaims(uint256[] memory globalIndexes)`

**Required Role:** `globalExitRootRemover`

**Prerequisites:**
- Step 3 (Unset claims) must be completed
- Correct global indexes for the valid bridges on L1 have been identified
- Bridge must still be in emergency state

**Cast Command:**

```bash
# Set environment variables
export L2_RPC_URL="<YOUR_L2_RPC_URL>"
export L2_BRIDGE_ADDR="<L2_BRIDGE_CONTRACT_ADDRESS>"
export SOVEREIGN_ADMIN_PRIVATE_KEY="<SOVEREIGN_ADMIN_PRIVATE_KEY>"
export CORRECT_GLOBAL_INDEX_1="<CORRECT_GLOBAL_INDEX_FOR_BRIDGE_1>"
export CORRECT_GLOBAL_INDEX_2="<CORRECT_GLOBAL_INDEX_FOR_BRIDGE_2>"

# Set multiple claims with correct indexes
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "setMultipleClaims(uint256[])" \
  "[$CORRECT_GLOBAL_INDEX_1,$CORRECT_GLOBAL_INDEX_2]"
```

**Expected Outcome:**
- Transaction succeeds
- Claims are marked as claimed with the correct indexes
- Event `SetClaim(uint256 indexed globalIndex, uint32 indexed originNetwork, uint32 indexed depositCount)` is emitted

**Verification:**

```bash
# Verify the new claim is now marked as claimed
cast call $L2_BRIDGE_ADDR \
  "isClaimed(uint32,uint32)(bool)" \
  $CORRECT_DEPOSIT_COUNT \
  $ORIGIN_NETWORK \
  --rpc-url $L2_RPC_URL

# Expected output: true
```

**Reference:**
- Test: [latest-n-injected-ger.bats#L252-L289](https://github.com/agglayer/e2e/blob/main/tests/aggkit/latest-n-injected-ger.bats#L252-L289)

---

### 4a. Force emit detailed claim events (Category B.1 and B.2)

**When to use:** Required for **Category B.1** and **Category B.2**. Not needed for **Category A**.

**Purpose:** Re-emit claim events with the correct GER and index data to update the aggkit bridgesync database with accurate information.

**Contract:** `BridgeL2SovereignChain` (L2 Bridge Contract)

**Function:** `forceEmitDetailedClaimEvent((bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint8,uint32,address,uint32,address,uint256,bytes)[])`

**Required Role:** `globalExitRootRemover`

This is typically the **sovereign admin** private key.

**Prerequisites:**
- Step 2 (Remove GER) must be completed
- For B.2: Step 3 (Unset claims) and Step 3b (Set claims) must be completed
- Have extracted correct claim parameters from the valid L1 bridge transactions
- Claim data includes: proofs, global_index, mainnet_exit_root, rollup_exit_root, origin/destination networks, addresses, amount, metadata

**Extracting Correct Claim Parameters:**

You need to extract the correct claim parameters from the valid L1 bridge transactions. The test files show using a helper function `extract_claim_parameters_json`, but you can also query directly:

```bash
# Get bridge event from L1 transaction
export L1_BRIDGE_TX_HASH="<VALID_L1_BRIDGE_TX_HASH>"
export L1_RPC_URL="<L1_RPC_URL>"

# Get transaction receipt to find the BridgeEvent
cast receipt $L1_BRIDGE_TX_HASH --rpc-url $L1_RPC_URL

# Extract parameters from the BridgeEvent logs:
# - deposit_count (becomes part of global_index)
# - leaf_type
# - origin_network
# - origin_address
# - destination_network
# - destination_address
# - amount
# - metadata

# Query L1InfoTree to get correct mainnet_exit_root and rollup_exit_root
# (from l1infotreesync database or L1 contract)
```

**Cast Command:**

```bash
# Set environment variables
export L2_RPC_URL="<YOUR_L2_RPC_URL>"
export L2_BRIDGE_ADDR="<L2_BRIDGE_CONTRACT_ADDRESS>"
export SOVEREIGN_ADMIN_PRIVATE_KEY="<SOVEREIGN_ADMIN_PRIVATE_KEY>"

# Claim parameters (extracted from valid L1 bridge)
export PROOF_LER="<PROOF_LOCAL_EXIT_ROOT_ARRAY>"  # 32-element array
export PROOF_RER="<PROOF_ROLLUP_EXIT_ROOT_ARRAY>"  # 32-element array
export GLOBAL_INDEX="<CORRECT_GLOBAL_INDEX>"
export MAINNET_EXIT_ROOT="<CORRECT_MAINNET_EXIT_ROOT>"
export ROLLUP_EXIT_ROOT="<CORRECT_ROLLUP_EXIT_ROOT>"
export LEAF_TYPE="0"  # 0 for asset, 1 for message
export ORIGIN_NETWORK="<ORIGIN_NETWORK>"
export ORIGIN_ADDRESS="<ORIGIN_ADDRESS>"
export DESTINATION_NETWORK="<DESTINATION_NETWORK>"
export DESTINATION_ADDRESS="<DESTINATION_ADDRESS>"
export AMOUNT="<AMOUNT>"
export METADATA="<METADATA>"

# Single claim
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "forceEmitDetailedClaimEvent((bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint8,uint32,address,uint32,address,uint256,bytes)[])" \
  "[($PROOF_LER,$PROOF_RER,$GLOBAL_INDEX,$MAINNET_EXIT_ROOT,$ROLLUP_EXIT_ROOT,$LEAF_TYPE,$ORIGIN_NETWORK,$ORIGIN_ADDRESS,$DESTINATION_NETWORK,$DESTINATION_ADDRESS,$AMOUNT,$METADATA)]"

# Multiple claims (Category B.2)
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "forceEmitDetailedClaimEvent((bytes32[32],bytes32[32],uint256,bytes32,bytes32,uint8,uint32,address,uint32,address,uint256,bytes)[])" \
  "[($PROOF_LER_1,$PROOF_RER_1,$GLOBAL_INDEX_1,$MAINNET_EXIT_ROOT_1,$ROLLUP_EXIT_ROOT_1,$LEAF_TYPE,$ORIGIN_NETWORK_1,$ORIGIN_ADDRESS_1,$DESTINATION_NETWORK_1,$DESTINATION_ADDRESS_1,$AMOUNT_1,$METADATA_1),($PROOF_LER_2,$PROOF_RER_2,$GLOBAL_INDEX_2,$MAINNET_EXIT_ROOT_2,$ROLLUP_EXIT_ROOT_2,$LEAF_TYPE,$ORIGIN_NETWORK_2,$ORIGIN_ADDRESS_2,$DESTINATION_NETWORK_2,$DESTINATION_ADDRESS_2,$AMOUNT_2,$METADATA_2)]"
```

**Expected Outcome:**
- Transaction succeeds with a transaction hash
- Event `DetailedClaimEvent` is emitted with correct claim data
- Aggkit bridgesync component will sync these events and update its database
- The aggsender can now generate valid certificates using the corrected claim data

**Verification:**

```bash
# Query the aggkit bridge API to verify the corrected claim is indexed
# This requires the aggkit bridge service to be running
export AGGKIT_BRIDGE_URL="<AGGKIT_BRIDGE_API_URL>"
export L2_NETWORK_ID="<L2_NETWORK_ID>"

# Wait a few moments for sync, then query
curl "$AGGKIT_BRIDGE_URL/bridges/v1/merkle-proof?networkId=$L2_NETWORK_ID&depositCount=<DEPOSIT_COUNT>&originNetworkId=<ORIGIN_NETWORK>"
```

**Reference:**
- Test: [bridge-sovereign-chain-e2e.bats#L295-L302](https://github.com/agglayer/e2e/blob/main/tests/aggkit/bridge-sovereign-chain-e2e.bats#L295-L302) and [latest-n-injected-ger.bats#L355-L362](https://github.com/agglayer/e2e/blob/main/tests/aggkit/latest-n-injected-ger.bats#L355-L362)
- Function signature: [bridge-sovereign-chain-e2e.bats#L22](https://github.com/agglayer/e2e/blob/main/tests/aggkit/bridge-sovereign-chain-e2e.bats#L22)

---

### 4b. Restore bridge

**When to use:** Always.

**Purpose:** Deactivate emergency state and resume normal bridge operations.

**Contract:** `BridgeL2SovereignChain` (L2 Bridge Contract)

**Function:** `deactivateEmergencyState()`

**Required Role:** `emergencyBridgeUnpauser`

This is typically the **sovereign admin** private key. In test environments, this is `l2_sovereign_admin_private_key`.

**Prerequisites:**
- All previous recovery steps for your category have been completed:
  - **Category A**: Steps 1, 2, 3 completed
  - **Category B.2**: Steps 1, 2, 3, 3b, 4a completed
- Verify that invalid GER(s) have been removed
- Verify that claims have been properly unset (Category A/B.2)
- For B.2: Verify that corrected claim events have been emitted (Step 4a)
- Verify that aggsender can now generate certificates without errors

**Cast Command:**

```bash
# Set environment variables
export L2_RPC_URL="<YOUR_L2_RPC_URL>"
export L2_BRIDGE_ADDR="<L2_BRIDGE_CONTRACT_ADDRESS>"
export SOVEREIGN_ADMIN_PRIVATE_KEY="<SOVEREIGN_ADMIN_PRIVATE_KEY>"

# Deactivate emergency state
cast send --legacy \
  --private-key $SOVEREIGN_ADMIN_PRIVATE_KEY \
  --rpc-url $L2_RPC_URL \
  $L2_BRIDGE_ADDR \
  "deactivateEmergencyState()"
```

**Expected Outcome:**
- Transaction succeeds with a transaction hash
- Bridge exits emergency state (normal operations resume)
- Event `EmergencyStateDeactivated()` is emitted
- Users can now bridge assets normally
- Aggsender can send certificates to agglayer

**Verification:**

```bash
# Check if emergency state is inactive (should return false)
cast call $L2_BRIDGE_ADDR \
  "isEmergencyState()(bool)" \
  --rpc-url $L2_RPC_URL

# Expected output: false

# Verify aggsender is operating normally (check logs for successful certificate generation)
```

**Reference:**
- Contract: [BridgeL2SovereignChain.sol#L880-L886](https://github.com/agglayer/agglayer-contracts/blob/main/contracts/v2/sovereignChains/BridgeL2SovereignChain.sol#L880-L886)

---

### Post-Recovery Verification

After completing the recovery steps for your category, perform these final checks:

1. **Verify GER removal:**
   ```bash
   cast call $L2_GER_ADDR "globalExitRootMap(bytes32)(uint256)" $INVALID_GER --rpc-url $L2_RPC_URL
   # Should return: 0
   ```

2. **Verify bridge state (Category A/B.2):**
   ```bash
   cast call $L2_BRIDGE_ADDR "isEmergencyState()(bool)" --rpc-url $L2_RPC_URL
   # Should return: false
   ```

3. **Check aggsender logs:**
   - No more errors about invalid GERs or merkle proof failures
   - Certificates are being generated and sent successfully
   - No "certificate validation failed" errors

4. **Verify l2gersync unstuck:**
   ```bash
   curl "$AGGKIT_BRIDGE_URL/bridge/v1/sync-status" | jq .l2_ger_info
   ```
   `last_processed_block` should now be advancing past the `removeGlobalExitRoots` transaction's
   block (see "Blocking and automatic recovery" above). If it is still pinned below that block, the
   removal has not yet been observed by l2gersync — wait for its next retry and check again.

5. **Verify claim states (if applicable):**
   - Category A: Unset claims remain unclaimed
   - Category B.2: New claims with correct indexes are properly recorded
   - Aggkit bridgesync database has correct claim records

6. **Monitor certificate settlement:**
   - Check that new certificates are being settled by agglayer
   - Verify L1InfoTree sync is operating normally
   - Confirm no new invalid GER detection

**Category A Additional Considerations:**

For Category A (under-collateralization), after recovery you may need to:
- Coordinate with affected users about the invalid claims
- Consider using `setLocalBalanceTree` to correct L2 bridge balance if needed
- Document the incident for audit purposes

## Relevant links

- [A, B.1, B.2 deeper explanation](https://github.com/agglayer/ADRs/issues/28)
- [E2E testing instructions on prod networks](https://hackmd.io/@rachit77/S1ms6PYM-x)
- [CI E2E tests](https://github.com/agglayer/e2e/blob/main/tests/aggkit/latest-n-injected-ger.bats#L825)
