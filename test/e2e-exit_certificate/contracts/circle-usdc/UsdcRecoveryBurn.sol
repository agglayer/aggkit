// SPDX-License-Identifier: AGPL-3.0
pragma solidity 0.8.28;

/// @title UsdcRecoveryBurn
/// @notice Temporary implementation for the zkEVM USDC.e FiatToken proxy
/// 0x37eAA0eF3549a5Bb7D431be78a3D99BD360d19e5 (issue 819). The proxy admin (the zkEVM Safe
/// 0x2be7…9c9f) sets it, ZkMinterBurner burns each holder's balance through it, and the same Safe
/// transaction restores the original implementation right after.
///
/// recoveryBurn(from, amount) has the same effect and events as the original
/// ZkMinterBurner.bridgeToken flow: transferFrom(holder → ZkMinterBurner) then burn(amount) by the
/// minter. Only ZkMinterBurner can call it. Storage: FiatToken balances mapping at slot 9,
/// totalSupply_ at slot 11 (checked on zkEVM at block 33391890).
contract UsdcRecoveryBurn {
    address public constant ZK_MINTER_BURNER = 0xBDa0B27f93B2FD3f076725b89cf02e48609bC189;
    uint256 internal constant _BALANCES_SLOT = 9;
    uint256 internal constant _TOTAL_SUPPLY_SLOT = 11;

    event Transfer(address indexed from, address indexed to, uint256 value);
    event Burn(address indexed burner, uint256 amount);

    error NotMinterBurner();
    error BadBalance();

    function recoveryBurn(address from, uint256 amount) external {
        if (msg.sender != ZK_MINTER_BURNER) revert NotMinterBurner();
        bytes32 key = keccak256(abi.encode(from, _BALANCES_SLOT));
        uint256 bal;
        uint256 supply;
        assembly {
            bal := sload(key)
            supply := sload(_TOTAL_SUPPLY_SLOT)
        }
        // The top bit is FiatToken v2.2's blacklist flag: a blacklisted holder is refused.
        if (amount == 0 || bal >> 255 != 0 || bal < amount || supply < amount) revert BadBalance();
        assembly {
            sstore(key, sub(bal, amount))
            sstore(_TOTAL_SUPPLY_SLOT, sub(supply, amount))
        }
        emit Transfer(from, ZK_MINTER_BURNER, amount);
        emit Burn(ZK_MINTER_BURNER, amount);
        emit Transfer(ZK_MINTER_BURNER, address(0), amount);
    }
}
