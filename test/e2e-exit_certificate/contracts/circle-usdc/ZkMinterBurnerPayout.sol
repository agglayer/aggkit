// SPDX-License-Identifier: AGPL-3.0
pragma solidity 0.8.28;

interface IPolygonZkEVMBridge {
    function bridgeMessage(
        uint32 destinationNetwork,
        address destinationAddress,
        bool forceUpdateGlobalExitRoot,
        bytes calldata metadata
    ) external payable;
}

interface IUsdcRecoveryBurn {
    function recoveryBurn(address from, uint256 amount) external;
}

/// @title ZkMinterBurnerPayout
/// @notice Replacement implementation for the Circle USDC (USDC.e) ZkMinterBurner UUPS proxy
/// 0xBDa0B27f93B2FD3f076725b89cf02e48609bC189 on the halted Polygon zkEVM (issue 819).
///
/// The proxy owner (the zkEVM Safe 0x2be7…9c9f) calls, on the old implementation,
/// `upgradeToAndCall(<this>, payout())` (inside a MultiSend that also swaps the USDC.e implementation).
/// `payout()` then runs in the proxy's context and calls
/// `bridgeMessage(0, L1_ESCROW, _, abi.encode(holder, amount))` once per holder: exactly the leaf
/// the old `bridgeToken(holder, amount, true)` emitted (origin network 1 / the proxy, destination
/// network 0 / L1Escrow, amount 0), after burning the holder's USDC.e through the temporary
/// USDC.e implementation UsdcRecoveryBurn (set and removed by the same Safe transaction). On L1, `claimMessage` of each leaf calls
/// `L1Escrow.onMessageReceived`, which pays `amount` L1 USDC to `holder`. No holder has to act.
///
/// The payout list is fixed at deployment: it is read from data contracts (code = 0x00 ‖ entries,
/// each entry 20-byte holder ‖ 6-byte amount), whose addresses, the entry count and the total are
/// set in the constructor and never change. `payout()` runs once, from the owner only.
contract ZkMinterBurnerPayout {
    /// @dev EIP-1967 implementation slot.
    bytes32 internal constant _IMPLEMENTATION_SLOT = 0x360894a13ba1a3210667c828492db98dca3e2076cc3735a920a3ca505d382bbc;
    /// @dev OwnableUpgradeable._owner of the old implementation (OpenZeppelin 4.x: slot 51).
    uint256 internal constant _OWNER_SLOT = 0x33;
    /// @dev Set once payout() ran (proxy storage, outside the old layout).
    bytes32 internal constant _PAID_SLOT = keccak256("zkevm.circle-usdc.payout.paid");

    IPolygonZkEVMBridge public constant BRIDGE = IPolygonZkEVMBridge(0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe);
    /// @notice USDC.e; during payout() its implementation is UsdcRecoveryBurn.
    IUsdcRecoveryBurn public constant USDCE = IUsdcRecoveryBurn(0x37eAA0eF3549a5Bb7D431be78a3D99BD360d19e5);
    address public constant L1_ESCROW = 0x70E70e58ed7B1Cec0D8ef7464072ED8A52d755eB;
    uint32 public constant MAINNET_NETWORK_ID = 0;
    uint256 internal constant _ENTRY_SIZE = 26;

    address private immutable _self;
    uint256 public immutable payoutCount;
    uint256 public immutable payoutTotal;
    /// @notice The data contracts holding the payout list, in order (storage of this implementation,
    /// written only by the constructor).
    address[] public chunks;

    error NotOwner();
    error AlreadyPaid();
    error NotThroughProxy();
    error BadList();

    constructor(address[] memory chunks_, uint256 count_, uint256 total_) {
        _self = address(this);
        chunks = chunks_;
        payoutCount = count_;
        payoutTotal = total_;
        // The list must be exactly count_ entries summing to total_.
        (uint256 n, uint256 sum) = _walk(chunks_, false);
        if (n != count_ || sum != total_) revert BadList();
    }

    function owner() public view returns (address o) {
        assembly {
            o := sload(_OWNER_SLOT)
        }
    }

    function chunkCount() external view returns (uint256) {
        return chunks.length;
    }

    /// @notice ERC-1822, checked by the old implementation's upgradeToAndCall.
    function proxiableUUID() external view returns (bytes32) {
        if (address(this) != _self) revert NotThroughProxy();
        return _IMPLEMENTATION_SLOT;
    }

    /// @notice One bridgeMessage per holder to L1Escrow. Owner only, once, through the proxy.
    function payout() external {
        if (address(this) == _self) revert NotThroughProxy();
        if (msg.sender != owner()) revert NotOwner();
        bytes32 paidSlot = _PAID_SLOT;
        bool paid;
        assembly {
            paid := sload(paidSlot)
        }
        if (paid) revert AlreadyPaid();
        assembly {
            sstore(paidSlot, 1)
        }
        _walk(ZkMinterBurnerPayout(_self).getChunks(), true);
    }

    /// @notice Lets the owner move on to another implementation afterwards.
    function upgradeTo(address newImplementation) external {
        if (address(this) == _self) revert NotThroughProxy();
        if (msg.sender != owner()) revert NotOwner();
        if (newImplementation.code.length == 0) revert BadList();
        bytes32 slot = _IMPLEMENTATION_SLOT;
        assembly {
            sstore(slot, newImplementation)
        }
    }

    /// @dev Reads every entry; with send = true, bridges each one (the last one updates the GER).
    function _walk(address[] memory list, bool send) internal returns (uint256 n, uint256 sum) {
        uint256 count = payoutCount;
        for (uint256 c = 0; c < list.length; c++) {
            address ptr = list[c];
            uint256 len = ptr.code.length;
            if (len == 0 || (len - 1) % _ENTRY_SIZE != 0) revert BadList();
            bytes memory data = new bytes(len - 1);
            assembly {
                extcodecopy(ptr, add(data, 32), 1, sub(len, 1))
            }
            for (uint256 off = 0; off < data.length; off += _ENTRY_SIZE) {
                address holder;
                uint256 amount;
                assembly {
                    let w := mload(add(add(data, 32), off))
                    holder := shr(96, w)
                    amount := shr(208, shl(160, w))
                }
                if (holder == address(0) || amount == 0) revert BadList();
                n++;
                sum += amount;
                if (send) {
                    // Same as the old bridgeToken(holder, amount, _): burn the holder's USDC.e, then
                    // send the message.
                    USDCE.recoveryBurn(holder, amount);
                    BRIDGE.bridgeMessage(MAINNET_NETWORK_ID, L1_ESCROW, n == count, abi.encode(holder, amount));
                }
            }
        }
    }

    function getChunks() external view returns (address[] memory) {
        return chunks;
    }
}
