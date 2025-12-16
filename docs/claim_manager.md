# Claim Manager Design Document

## Overview

The Claim Manager is a new component responsible for automatically performing claims from L1 to L2. It monitors bridges that occurred on L1 (with this aggkit node's L2 network as the destination), determines when they become eligible for claiming based on Global Exit Root (GER) injection status, and automatically executes claim transactions on L2.

## Purpose

- **Automate Claim Process**: Eliminate the need for users to manually claim their bridges from L1 to L2
- **Monitor Eligibility**: Continuously check which bridges are eligible for claiming based on GER injection status
- **Execute Claims**: Automatically submit claim transactions to the L2 network
- **Track Claims**: Maintain a record of all claims performed by this component

## Architecture

### Component Structure

```
claimmanager/
├── claimmanager.go          # Main component orchestrator
├── config.go                # Configuration structure
├── processor.go             # Core claim processing logic
├── eligibility_checker.go   # Bridge eligibility determination
├── claim_executor.go        # L2 claim transaction execution
├── migrations/              # Database migrations
│   ├── claimmanager0001.sql
│   └── migrations.go
└── types/
    └── types.go             # Internal types
```

### Dependencies

The Claim Manager depends on the following existing components:

1. **BridgeL1Sync** (`bridgesync`): Provides access to bridges that occurred on L1
   - Database: `bridgel1sync.sqlite`
   - Tables: `bridge` table containing L1 bridge events

2. **L2GERSync** (`l2gersync`): Tracks GERs injected into L2
   - Database: `l2gersync.sqlite`
   - Methods: `GetFirstGERAfterL1InfoTreeIndex()`, `GetInjectedGERsForRange()`

3. **L1InfoTreeSync** (`l1infotreesync`): Provides L1 info tree data for proof generation
   - Database: `l1infotreesync.sqlite`
   - Methods: `GetInfoByIndex()`, `GetProofForGER()`

4. **L2 Ethereum Client**: For submitting claim transactions to L2
   - Interface: `aggkittypes.BaseEthereumClienter`
   - Bridge Contract: L2 bridge contract address

5. **Ethereum Transaction Manager** (optional): For managing transaction lifecycle
   - Component: `ethtxmanager` (if available)

## Data Model

### Database Schema

The Claim Manager will maintain its own SQLite database (`claimmanager.sqlite`) with the following schema:

#### `claim_attempt` Table

Tracks all claim attempts made by the claim manager:

```sql
CREATE TABLE claim_attempt (
    id                      INTEGER PRIMARY KEY AUTOINCREMENT,
    bridge_block_num        INTEGER NOT NULL,
    bridge_block_pos        INTEGER NOT NULL,
    global_index            TEXT NOT NULL,
    origin_network          INTEGER NOT NULL,
    destination_network      INTEGER NOT NULL,
    l1_info_tree_index      INTEGER NOT NULL,
    ger_used                VARCHAR NOT NULL,
    tx_hash                 VARCHAR,
    status                  VARCHAR NOT NULL,  -- 'pending', 'submitted', 'confirmed', 'failed'
    error_message           TEXT,
    created_at              INTEGER NOT NULL,
    updated_at              INTEGER NOT NULL,
    confirmed_at            INTEGER,
    UNIQUE(bridge_block_num, bridge_block_pos)
);

CREATE INDEX idx_claim_attempt_status ON claim_attempt(status);
CREATE INDEX idx_claim_attempt_global_index ON claim_attempt(global_index);
CREATE INDEX idx_claim_attempt_l1_info_tree_index ON claim_attempt(l1_info_tree_index);
```

#### `last_processed_bridge` Table

Tracks the last bridge processed to avoid reprocessing:

```sql
CREATE TABLE last_processed_bridge (
    id                      INTEGER PRIMARY KEY,
    last_block_num          INTEGER NOT NULL,
    last_block_pos          INTEGER NOT NULL,
    last_processed_at       INTEGER NOT NULL
);
```

### Data Flow

1. **Bridge Data Source**: Read from `bridgel1sync` database's `bridge` table
2. **GER Status**: Query `l2gersync` database to check injected GERs
3. **Proof Generation**: Use `l1infotreesync` to generate merkle proofs
4. **Claim Tracking**: Store claim attempts in `claimmanager` database

## Workflow

### Main Processing Loop

The Claim Manager runs a periodic processing loop (every few seconds, configurable):

```
1. Get last processed bridge position from claimmanager DB
2. Query bridgel1sync DB for new bridges since last processed position
3. For each bridge:
   a. Get bridge's L1InfoTreeIndex (from deposit_count)
   b. Check if GER has been injected on L2 at or after that index
   c. If eligible:
      - Generate merkle proofs
      - Execute claim transaction on L2
      - Track claim attempt in claimmanager DB
   d. If not eligible:
      - Skip (will be checked in next iteration)
4. Update last processed bridge position
5. Sleep for configured interval
```

### Eligibility Determination

A bridge is eligible for claiming if:

1. **GER Injection Check**:
   - Get the bridge's L1InfoTreeIndex (derived from `deposit_count`)
   - Query L2GERSync: `GetFirstGERAfterL1InfoTreeIndex(l1InfoTreeIndex)`
   - If a GER is found, the bridge is eligible

2. **Not Already Claimed**:
   - Check if a successful claim exists in `claim_attempt` table for this bridge
   - Status should not be 'confirmed' or 'submitted' (if transaction is still pending)

3. **Destination Network Match**:
   - Bridge's `destination_network` must match this aggkit node's L2 network ID

### Claim Execution

For each eligible bridge:

1. **Proof Generation**:
   - Get L1InfoTreeLeaf for the GER's L1InfoTreeIndex
   - Generate merkle proof using L1InfoTreeSync
   - Construct proof_local_exit_root and proof_rollup_exit_root

2. **Transaction Construction**:
   - Build claim transaction data:
     - `globalIndex`: Bridge's global_index
     - `originNetwork`: Bridge's origin_network
     - `originAddress`: Bridge's origin_address
     - `destinationAddress`: Bridge's destination_address
     - `amount`: Bridge's amount
     - `metadata`: Bridge's metadata
     - `proofLocalExitRoot`: Generated merkle proof
     - `proofRollupExitRoot`: Generated merkle proof
   - Call L2 bridge contract's `claimBridge()` or `claimAsset()` method

3. **Transaction Submission**:
   - Submit transaction to L2 network
   - Store transaction hash in `claim_attempt` table
   - Update status to 'submitted'

4. **Confirmation Tracking** (optional):
   - Monitor transaction receipt
   - Update status to 'confirmed' on success
   - Update status to 'failed' on failure with error message

## Configuration

### Config Structure

```go
type Config struct {
    // DBPath path to the claim manager database
    DBPath string `mapstructure:"DBPath"`

    // ProcessingInterval time between claim processing cycles
    ProcessingInterval types.Duration `mapstructure:"ProcessingInterval"`

    // L2NetworkID the network ID of the L2 network (destination network)
    L2NetworkID uint32 `mapstructure:"L2NetworkID"`

    // L2BridgeAddr address of the bridge contract on L2
    L2BridgeAddr common.Address `mapstructure:"L2BridgeAddr"`

    // L2Client configuration for L2 Ethereum client
    L2Client etherman.Config `mapstructure:"L2Client"`

    // MaxConcurrentClaims maximum number of concurrent claim transactions
    MaxConcurrentClaims int `mapstructure:"MaxConcurrentClaims"`

    // RetryConfig configuration for retrying failed claims
    RetryConfig retry_config.RetryConfig `mapstructure:"RetryConfig"`

    // GasLimit gas limit for claim transactions
    GasLimit uint64 `mapstructure:"GasLimit"`

    // GasPriceMultiplier multiplier for gas price (e.g., 1.1 for 10% increase)
    GasPriceMultiplier float64 `mapstructure:"GasPriceMultiplier"`

    // EnableAutoClaim whether to automatically execute claims (default: true)
    EnableAutoClaim bool `mapstructure:"EnableAutoClaim"`

    // DBQueryTimeout timeout for database operations
    DBQueryTimeout types.Duration `mapstructure:"DBQueryTimeout"`
}
```

### Default Configuration

```toml
[ClaimManager]
DBPath = "{{PathRWData}}/claimmanager.sqlite"
ProcessingInterval = "5s"
L2NetworkID = 0  # Must be configured per deployment
L2BridgeAddr = "0x0000000000000000000000000000000000000000"  # Must be configured
MaxConcurrentClaims = 5
GasLimit = 500000
GasPriceMultiplier = 1.1
EnableAutoClaim = true
DBQueryTimeout = "30s"
```

## Component Interfaces

### ClaimManager Interface

```go
type ClaimManager interface {
    // Start begins the claim processing loop
    Start(ctx context.Context)

    // Stop gracefully stops the claim manager
    Stop(ctx context.Context) error

    // GetClaimAttempts returns claim attempts with optional filters
    GetClaimAttempts(ctx context.Context, filters ClaimAttemptFilters) ([]ClaimAttempt, error)

    // GetClaimAttemptByBridge returns claim attempt for a specific bridge
    GetClaimAttemptByBridge(ctx context.Context, blockNum, blockPos uint64) (*ClaimAttempt, error)

    // RetryFailedClaim retries a failed claim
    RetryFailedClaim(ctx context.Context, claimAttemptID int64) error
}
```

### Dependencies Interface

```go
type Dependencies struct {
    // BridgeL1Sync provides access to L1 bridge data
    BridgeL1Sync bridgesync.BridgeL1Querier

    // L2GERSync provides access to injected GER information
    L2GERSync l2gersync.L2GERSyncer

    // L1InfoTreeSync provides L1 info tree data and proofs
    L1InfoTreeSync l1infotreesync.L1InfoTreeQuerier

    // L2Client Ethereum client for L2 network
    L2Client aggkittypes.BaseEthereumClienter

    // Logger for logging
    Logger *log.Logger
}
```

## Error Handling

### Error Categories

1. **Temporary Errors** (should retry):
   - Network errors when querying databases
   - RPC errors when submitting transactions
   - Transaction pool full errors

2. **Permanent Errors** (should not retry):
   - Bridge already claimed
   - Invalid bridge data
   - Insufficient funds for gas
   - Contract revert (invalid proof, etc.)

### Retry Strategy

- Use exponential backoff for temporary errors
- Maximum retry attempts configurable
- Failed claims stored with error message for manual review
- Option to manually retry failed claims via API

## Metrics and Monitoring

### Prometheus Metrics

```go
// Claim processing metrics
claim_manager_bridges_checked_total{status="eligible|ineligible|error"}
claim_manager_claims_attempted_total{status="success|failed"}
claim_manager_claims_confirmed_total
claim_manager_claims_failed_total{error_type="..."}

// Timing metrics
claim_manager_processing_duration_seconds
claim_manager_claim_execution_duration_seconds

// Queue metrics
claim_manager_pending_claims_count
claim_manager_failed_claims_count
```

### Logging

- Log each bridge eligibility check
- Log claim transaction submission with tx hash
- Log claim confirmations and failures
- Log errors with sufficient context for debugging

## Integration Points

### With Existing Components

1. **BridgeL1Sync Integration**:
   - Read-only access to `bridgel1sync` database
   - Query bridges filtered by `destination_network = L2NetworkID`

2. **L2GERSync Integration**:
   - Read-only access to `l2gersync` database
   - Use `GetFirstGERAfterL1InfoTreeIndex()` to check eligibility

3. **L1InfoTreeSync Integration**:
   - Read-only access to `l1infotreesync` database
   - Use `GetInfoByIndex()` and `GetProofForGER()` for proof generation

4. **L2 Client Integration**:
   - Submit claim transactions to L2 network
   - Monitor transaction status

### Startup Sequence

1. Initialize claim manager database and run migrations
2. Verify dependencies (BridgeL1Sync, L2GERSync, L1InfoTreeSync are available)
3. Verify L2 client connectivity
4. Load last processed bridge position
5. Start processing loop

## Security Considerations

1. **Private Key Management**:
   - Claim transactions require a funded account
   - Private key should be stored securely (environment variable, key management service)
   - Use a dedicated account with limited funds

2. **Gas Management**:
   - Set appropriate gas limits to prevent DoS
   - Monitor gas prices to avoid overpaying
   - Implement gas price limits

3. **Rate Limiting**:
   - Limit concurrent claim transactions
   - Implement rate limiting for L2 RPC calls

4. **Validation**:
   - Validate all bridge data before processing
   - Verify proofs before submitting transactions
   - Check transaction status before marking as confirmed

## Future Enhancements

1. **Batch Claims**: Group multiple claims into a single transaction (if supported by contract)
2. **Priority Queue**: Prioritize claims based on age or amount
3. **Webhook Notifications**: Notify external systems of claim status changes
4. **Admin API**: REST API for monitoring and manual intervention
5. **Claim History**: Extended history and analytics for claims
6. **Multi-account Support**: Distribute claims across multiple accounts for rate limiting

## Testing Strategy

### Unit Tests

- Eligibility checker logic
- Proof generation
- Transaction construction
- Database operations

### Integration Tests

- End-to-end claim flow with test L1/L2 networks
- Error handling scenarios
- Concurrent claim processing

### E2E Tests

- Full claim lifecycle from bridge to confirmed claim
- Multiple bridges with different eligibility timings
- Failure and retry scenarios

## Migration Plan

1. **Phase 1**: Deploy Claim Manager in monitoring-only mode (no actual claims)
2. **Phase 2**: Enable auto-claiming for a subset of bridges (whitelist)
3. **Phase 3**: Gradually expand to all eligible bridges
4. **Phase 4**: Full production deployment with monitoring and alerting

## Open Questions

1. Should the Claim Manager support claiming bridges from L2 to L1 as well?
2. How should we handle bridges that were already claimed manually by users?
3. Should there be a maximum claim amount threshold?
4. How to handle gas price spikes (pause claiming or continue)?
5. Should failed claims be automatically retried or require manual intervention?


