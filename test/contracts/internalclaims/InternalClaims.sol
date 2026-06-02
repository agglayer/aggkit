// SPDX-License-Identifier: AGPL-3.0

pragma solidity ^0.8.20;
import "./Interfaces.sol";

contract InternalClaims is IInternalClaims {
    uint256 internal constant _DEPOSIT_CONTRACT_TREE_DEPTH = 32;

    event MessageReceived(address destinationAddress);
    event UpdateParameters();

    IPolygonZkEVMBridgeV2 public immutable bridgeAddress;

    // First claim parameters
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofLocalExitRoot1;
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofRollupExitRoot1;
    uint256 globalIndex1;
    bytes32 mainnetExitRoot1;
    bytes32 rollupExitRoot1;
    uint32 originNetwork1;
    address originAddress1;
    uint32 destinationNetwork1;
    address destinationAddress1;
    uint256 amount1;
    bytes metadata1;

    // Second claim parameters
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofLocalExitRoot2;
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofRollupExitRoot2;
    uint256 globalIndex2;
    bytes32 mainnetExitRoot2;
    bytes32 rollupExitRoot2;
    uint32 originNetwork2;
    address originAddress2;
    uint32 destinationNetwork2;
    address destinationAddress2;
    uint256 amount2;
    bytes metadata2;

    // Third claim parameters
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofLocalExitRoot3;
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofRollupExitRoot3;
    uint256 globalIndex3;
    bytes32 mainnetExitRoot3;
    bytes32 rollupExitRoot3;
    uint32 originNetwork3;
    address originAddress3;
    uint32 destinationNetwork3;
    address destinationAddress3;
    uint256 amount3;
    bytes metadata3;

    // Fourth claim parameters
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofLocalExitRoot4;
    bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] smtProofRollupExitRoot4;
    uint256 globalIndex4;
    bytes32 mainnetExitRoot4;
    bytes32 rollupExitRoot4;
    uint32 originNetwork4;
    address originAddress4;
    uint32 destinationNetwork4;
    address destinationAddress4;
    uint256 amount4;
    bytes metadata4;

    bytes data;

    constructor(IPolygonZkEVMBridgeV2 _bridgeAddress) {
        bridgeAddress = _bridgeAddress;
    }

    function updateParameters(
        // First claim parameters
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofLocalExitRoot1,
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofRollupExitRoot1,
        uint256 mglobalIndex1,
        bytes32 mmainnetExitRoot1,
        bytes32 mrollupExitRoot1,
        uint32 moriginNetwork1,
        address moriginAddress1,
        uint32 mdestinationNetwork1,
        address mdestinationAddress1,
        uint256 mamount1,
        bytes calldata mmetadata1,
        // Second claim parameters
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofLocalExitRoot2,
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofRollupExitRoot2,
        uint256 mglobalIndex2,
        bytes32 mmainnetExitRoot2,
        bytes32 mrollupExitRoot2,
        uint32 moriginNetwork2,
        address moriginAddress2,
        uint32 mdestinationNetwork2,
        address mdestinationAddress2,
        uint256 mamount2,
        bytes calldata mmetadata2,
        // Third claim parameters
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofLocalExitRoot3,
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofRollupExitRoot3,
        uint256 mglobalIndex3,
        bytes32 mmainnetExitRoot3,
        bytes32 mrollupExitRoot3,
        uint32 moriginNetwork3,
        address moriginAddress3,
        uint32 mdestinationNetwork3,
        address mdestinationAddress3,
        uint256 mamount3,
        bytes calldata mmetadata3,
        // Fourth claim parameters
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofLocalExitRoot4,
        bytes32[_DEPOSIT_CONTRACT_TREE_DEPTH] calldata msmtProofRollupExitRoot4,
        uint256 mglobalIndex4,
        bytes32 mmainnetExitRoot4,
        bytes32 mrollupExitRoot4,
        uint32 moriginNetwork4,
        address moriginAddress4,
        uint32 mdestinationNetwork4,
        address mdestinationAddress4,
        uint256 mamount4,
        bytes calldata mmetadata4
    ) public {
        // Set first claim parameters
        smtProofLocalExitRoot1 = msmtProofLocalExitRoot1;
        smtProofRollupExitRoot1 = msmtProofRollupExitRoot1;
        globalIndex1 = mglobalIndex1;
        mainnetExitRoot1 = mmainnetExitRoot1;
        rollupExitRoot1 = mrollupExitRoot1;
        originNetwork1 = moriginNetwork1;
        originAddress1 = moriginAddress1;
        destinationNetwork1 = mdestinationNetwork1;
        destinationAddress1 = mdestinationAddress1;
        amount1 = mamount1;
        metadata1 = mmetadata1;

        // Set second claim parameters
        smtProofLocalExitRoot2 = msmtProofLocalExitRoot2;
        smtProofRollupExitRoot2 = msmtProofRollupExitRoot2;
        globalIndex2 = mglobalIndex2;
        mainnetExitRoot2 = mmainnetExitRoot2;
        rollupExitRoot2 = mrollupExitRoot2;
        originNetwork2 = moriginNetwork2;
        originAddress2 = moriginAddress2;
        destinationNetwork2 = mdestinationNetwork2;
        destinationAddress2 = mdestinationAddress2;
        amount2 = mamount2;
        metadata2 = mmetadata2;

        // Set third claim parameters
        smtProofLocalExitRoot3 = msmtProofLocalExitRoot3;
        smtProofRollupExitRoot3 = msmtProofRollupExitRoot3;
        globalIndex3 = mglobalIndex3;
        mainnetExitRoot3 = mmainnetExitRoot3;
        rollupExitRoot3 = mrollupExitRoot3;
        originNetwork3 = moriginNetwork3;
        originAddress3 = moriginAddress3;
        destinationNetwork3 = mdestinationNetwork3;
        destinationAddress3 = mdestinationAddress3;
        amount3 = mamount3;
        metadata3 = mmetadata3;

        // Set fourth claim parameters
        smtProofLocalExitRoot4 = msmtProofLocalExitRoot4;
        smtProofRollupExitRoot4 = msmtProofRollupExitRoot4;
        globalIndex4 = mglobalIndex4;
        mainnetExitRoot4 = mmainnetExitRoot4;
        rollupExitRoot4 = mrollupExitRoot4;
        originNetwork4 = moriginNetwork4;
        originAddress4 = moriginAddress4;
        destinationNetwork4 = mdestinationNetwork4;
        destinationAddress4 = mdestinationAddress4;
        amount4 = mamount4;
        metadata4 = mmetadata4;

        emit UpdateParameters();
    }

    /// @inheritdoc IInternalClaims
    function onMessageReceived(
        address originAddress1,
        uint32 originNetwork1,
        bytes memory data1
    ) external payable {
        data = data1;

        // First claim with first set of parameters
        try bridgeAddress.claimAsset(
            smtProofLocalExitRoot1,
            smtProofRollupExitRoot1,
            globalIndex1,
            mainnetExitRoot1,
            rollupExitRoot1,
            originNetwork1,
            originAddress1,
            destinationNetwork1,
            destinationAddress1,
            amount1,
            metadata1
        ) {
            // First claim succeeded
        } catch {
            // First claim failed, continue with next claims
        }

        // Second claim with second set of parameters
        try bridgeAddress.claimAsset(
            smtProofLocalExitRoot2,
            smtProofRollupExitRoot2,
            globalIndex2,
            mainnetExitRoot2,
            rollupExitRoot2,
            originNetwork2,
            originAddress2,
            destinationNetwork2,
            destinationAddress2,
            amount2,
            metadata2
        ) {
            // Second claim succeeded
        } catch {
            // Second claim failed, continue with next claims
        }

        // Third claim with third set of parameters
        try bridgeAddress.claimAsset(
            smtProofLocalExitRoot3,
            smtProofRollupExitRoot3,
            globalIndex3,
            mainnetExitRoot3,
            rollupExitRoot3,
            originNetwork3,
            originAddress3,
            destinationNetwork3,
            destinationAddress3,
            amount3,
            metadata3
        ) {
            // Third claim succeeded
        } catch {
            // Third claim failed, continue with next claims
        }

        // Fourth claim with fourth set of parameters
        try bridgeAddress.claimAsset(
            smtProofLocalExitRoot4,
            smtProofRollupExitRoot4,
            globalIndex4,
            mainnetExitRoot4,
            rollupExitRoot4,
            originNetwork4,
            originAddress4,
            destinationNetwork4,
            destinationAddress4,
            amount4,
            metadata4
        ) {
            // Fourth claim succeeded
        } catch {
            // Fourth claim failed, transaction continues
        }
    }
}
