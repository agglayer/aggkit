// SPDX-License-Identifier: MIT
pragma solidity 0.8.24;

// Stand-in for a "locked" smart contract in the delta e2e (run_e2e_test.sh --delta): it holds ETH that
// only its owner (the deployer) can move, through withdraw(). That is what makes the funds recoverable
// with the zkevm-proof-of-ownership-kit: an EOA that signs can release them to a destination it controls.
// Compiled bytecode: Vault.bin (see "Regenerating Vault.bin" in ../README.md).
contract Vault {
    address public owner;

    constructor() {
        owner = msg.sender;
    }

    receive() external payable {}

    function withdraw(address payable to, uint256 amount) external {
        require(msg.sender == owner, "not owner");
        (bool ok,) = to.call{value: amount}("");
        require(ok, "transfer failed");
    }
}
