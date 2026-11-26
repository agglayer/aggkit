// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

interface IBridgeAsset {
    function bridgeAsset(
        uint32 destinationNetwork,
        address destinationAddress,
        uint256 amount,
        address token,
        bool forceUpdateGlobalExitRoot,
        bytes calldata permitData
    ) external payable;
}

// MultiBridgeRouter is e2e test tooling for bridgetracker's per-event tracking:
// one call bridges the native token twice through the canonical bridge,
// so a single tx emits two canonical BridgeEvents with consecutive deposit counts, as a router
// or multicall would.
//
// TEST-ONLY: not part of the aggkit runtime.
contract MultiBridgeRouter {
    // bridgeTwice performs two bridgeAsset calls of the native token in this one tx: amountA to
    // (destinationNetworkA, destinationAddressA) first, then amountB to
    // (destinationNetworkB, destinationAddressB). msg.value must be amountA + amountB.
    function bridgeTwice(
        address bridge,
        uint32 destinationNetworkA,
        address destinationAddressA,
        uint256 amountA,
        uint32 destinationNetworkB,
        address destinationAddressB,
        uint256 amountB
    ) external payable {
        require(msg.value == amountA + amountB, "MultiBridgeRouter: wrong value");
        IBridgeAsset(bridge).bridgeAsset{value: amountA}(
            destinationNetworkA, destinationAddressA, amountA, address(0), true, ""
        );
        IBridgeAsset(bridge).bridgeAsset{value: amountB}(
            destinationNetworkB, destinationAddressB, amountB, address(0), true, ""
        );
    }
}
