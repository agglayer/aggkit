// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

contract LogEmitter {
    // Simple event
    event Ping(address indexed from, uint256 indexed id, string message);

    // Event with arbitrary data
    event Data(address indexed from, bytes32 indexed topic, bytes data);

    uint256 public counter;

    constructor(string memory bootMessage) {
        // Emits a log on deployment
        emit Ping(msg.sender, 0, bootMessage);
    }

    // Emits an event and increments a counter
    function emitPing(uint256 id, string calldata message) external {
        counter += 1;
        emit Ping(msg.sender, id, message);
    }

    // Emits an event with arbitrary bytes (useful for tests)
    function emitData(bytes32 topic, bytes calldata data) external {
        emit Data(msg.sender, topic, data);
    }
}