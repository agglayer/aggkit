// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

/// @title RollupManagerMock
/// @notice Minimal, test-only stand-in for the real AgglayerManager rollup manager contract.
/// It exposes only the two view functions the bridgeservicefinder package needs
/// (`rollupCount` and `rollupIDToRollupData`) with EXACTLY the same function
/// selectors/return-tuple layout as the real contract, so the real
/// `agglayermanager` Go binding can be pointed at an instance of this mock and
/// correctly decode the results. Every other field of the returned struct is
/// zero-valued; only `rollupContract` is settable, since that's the only field
/// bridgeservicefinder reads.
contract RollupManagerMock {
    /// @dev Rollup lifecycle events, matched EXACTLY (name + param types + indexed-ness) against the
    /// real AgglayerManager binding so the bridgeservicefinder listener can parse them with the real
    /// agglayermanager filterer to discover rollups attached after startup.
    // solhint-disable-next-line event-name-camelcase
    event CreateNewRollup(
        uint32 indexed rollupID,
        uint32 rollupTypeID,
        address rollupAddress,
        uint64 chainID,
        address gasTokenAddress
    );
    // solhint-disable-next-line event-name-camelcase
    event CreateNewAggchain(
        uint32 indexed rollupID,
        uint32 rollupTypeID,
        address rollupAddress,
        uint64 chainID,
        uint8 rollupVerifierType,
        bytes initializeBytesAggchain
    );
    // solhint-disable-next-line event-name-camelcase
    event AddExistingRollup(
        uint32 indexed rollupID,
        uint64 forkID,
        address rollupAddress,
        uint64 chainID,
        uint8 rollupVerifierType,
        uint64 lastVerifiedBatchBeforeUpgrade,
        bytes32 programVKey,
        bytes32 initPessimisticRoot
    );

    /// @dev Mirrors AgglayerManager.RollupDataReturn exactly (field names, order and types),
    /// so the tuple layout returned by `rollupIDToRollupData` matches the real contract.
    struct RollupDataReturn {
        address rollupContract;
        uint64 chainID;
        address verifier;
        uint64 forkID;
        bytes32 lastLocalExitRoot;
        uint64 lastBatchSequenced;
        uint64 lastVerifiedBatch;
        uint64 _legacyLastPendingState;
        uint64 _legacyLastPendingStateConsolidated;
        uint64 lastVerifiedBatchBeforeUpgrade;
        uint64 rollupTypeID;
        uint8 rollupVerifierType;
    }

    uint32 public rollupCount;

    mapping(uint32 => RollupDataReturn) internal _rollupIDToRollupData;

    /// @notice Test-only setter: registers (or overwrites) the rollup contract address for a
    /// given rollupID, and grows `rollupCount` if needed so that rollupID is enumerable.
    function setRollupContract(uint32 rollupID, address rollupContractAddr) external {
        if (rollupID > rollupCount) {
            rollupCount = rollupID;
        }
        _rollupIDToRollupData[rollupID].rollupContract = rollupContractAddr;
    }

    /// @notice Test-only setter: directly sets rollupCount, independent of setRollupContract.
    function setRollupCount(uint32 newRollupCount) external {
        rollupCount = newRollupCount;
    }

    /// @notice Test-only: registers a rollup (like setRollupContract) and emits CreateNewRollup, as
    /// the real manager does when a brand-new rollup is created from a rollup type. The extra event
    /// fields are zero-valued since bridgeservicefinder only reads rollupID and rollupAddress.
    function emitCreateNewRollup(uint32 rollupID, address rollupContractAddr) external {
        if (rollupID > rollupCount) {
            rollupCount = rollupID;
        }
        _rollupIDToRollupData[rollupID].rollupContract = rollupContractAddr;
        emit CreateNewRollup(rollupID, 0, rollupContractAddr, 0, address(0));
    }

    /// @notice Test-only: registers a rollup and emits CreateNewAggchain, as the real manager does
    /// when a new aggchain-type rollup is created.
    function emitCreateNewAggchain(uint32 rollupID, address rollupContractAddr) external {
        if (rollupID > rollupCount) {
            rollupCount = rollupID;
        }
        _rollupIDToRollupData[rollupID].rollupContract = rollupContractAddr;
        emit CreateNewAggchain(rollupID, 0, rollupContractAddr, 0, 0, "");
    }

    /// @notice Test-only: registers a rollup and emits AddExistingRollup, as the real manager does
    /// when an already-deployed rollup contract is attached.
    function emitAddExistingRollup(uint32 rollupID, address rollupContractAddr) external {
        if (rollupID > rollupCount) {
            rollupCount = rollupID;
        }
        _rollupIDToRollupData[rollupID].rollupContract = rollupContractAddr;
        emit AddExistingRollup(rollupID, 0, rollupContractAddr, 0, 0, 0, bytes32(0), bytes32(0));
    }

    /// @notice Matches AgglayerManager.rollupIDToRollupData(uint32) exactly.
    function rollupIDToRollupData(uint32 rollupID) external view returns (RollupDataReturn memory rollupData) {
        return _rollupIDToRollupData[rollupID];
    }
}
