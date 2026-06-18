// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

contract RollupManagerMock {
    struct RollupDataReturn {
        address rollupContract;
        uint64 chainID;
        address verifier;
        uint64 forkID;
        bytes32 lastLocalExitRoot;
        uint64 lastBatchSequenced;
        uint64 lastVerifiedBatch;
        uint64 legacyLastPendingState;
        uint64 legacyLastPendingStateConsolidated;
        uint64 lastVerifiedBatchBeforeUpgrade;
        uint64 rollupTypeID;
        uint8 rollupVerifierType;
    }

    uint32 public rollupCount;
    mapping(uint32 => RollupDataReturn) public rollupIDToRollupData;

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

    event CreateNewRollup(
        uint32 indexed rollupID,
        uint32 rollupTypeID,
        address rollupAddress,
        uint64 chainID,
        address gasTokenAddress
    );

    event CreateNewAggchain(
        uint32 indexed rollupID,
        uint32 rollupTypeID,
        address rollupAddress,
        uint64 chainID,
        uint8 rollupVerifierType,
        bytes initializeBytesAggchain
    );

    function addExistingRollup(
        address rollupAddress,
        address verifier,
        uint64 forkID,
        uint64 chainID,
        bytes32 initRoot,
        uint8 rollupVerifierType,
        bytes32 programVKey,
        bytes32 initPessimisticRoot
    ) external {
        rollupCount += 1;
        rollupIDToRollupData[rollupCount] = RollupDataReturn({
            rollupContract: rollupAddress,
            chainID: chainID,
            verifier: verifier,
            forkID: forkID,
            lastLocalExitRoot: initRoot,
            lastBatchSequenced: 0,
            lastVerifiedBatch: 0,
            legacyLastPendingState: 0,
            legacyLastPendingStateConsolidated: 0,
            lastVerifiedBatchBeforeUpgrade: 0,
            rollupTypeID: 0,
            rollupVerifierType: rollupVerifierType
        });

        emit AddExistingRollup(
            rollupCount,
            forkID,
            rollupAddress,
            chainID,
            rollupVerifierType,
            0,
            programVKey,
            initPessimisticRoot
        );
    }
}
