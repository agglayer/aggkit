// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

// BridgeEventImpostor is throwaway e2e test tooling for bridgetracker's emitter/address check
// (issue #1751): it emits a log whose topic0 is byte-for-byte the same as the real bridge
// contract's BridgeEvent(uint8,uint32,address,uint32,address,uint256,bytes,uint32) -- the
// event signature below matches it exactly -- but from a contract address that is never the
// canonical bridge address BridgeEventSource.FindBridge resolves. It lets a test simulate "a tx
// whose only BridgeEvent-shaped log comes from a non-bridge contract" without needing a second
// deployment of the real (large) bridge contract.
//
// TEST-ONLY: this contract exists purely to be rejected by that check. It must never be
// deployed to a production or public network -- it deliberately emits bridge-shaped logs it
// has no authority to emit, and nothing here is part of the aggkit runtime.
contract BridgeEventImpostor {
    event BridgeEvent(
        uint8 leafType,
        uint32 originNetwork,
        address originAddress,
        uint32 destinationNetwork,
        address destinationAddress,
        uint256 amount,
        bytes metadata,
        uint32 depositCount
    );

    // emitFakeBridgeEvent emits a BridgeEvent-shaped log with caller-supplied fields, from this
    // (non-bridge) contract's own address.
    function emitFakeBridgeEvent(
        uint8 leafType,
        uint32 originNetwork,
        address originAddress,
        uint32 destinationNetwork,
        address destinationAddress,
        uint256 amount,
        bytes calldata metadata,
        uint32 depositCount
    ) external {
        emit BridgeEvent(
            leafType,
            originNetwork,
            originAddress,
            destinationNetwork,
            destinationAddress,
            amount,
            metadata,
            depositCount
        );
    }
}
