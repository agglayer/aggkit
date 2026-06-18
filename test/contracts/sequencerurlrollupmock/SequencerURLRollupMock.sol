// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

contract SequencerURLRollupMock {
    string public trustedSequencerURL;

    event SetTrustedSequencerURL(string newTrustedSequencerURL);

    constructor(string memory initialSequencerURL) {
        trustedSequencerURL = initialSequencerURL;
    }

    function setTrustedSequencerURL(string calldata newTrustedSequencerURL) external {
        trustedSequencerURL = newTrustedSequencerURL;
        emit SetTrustedSequencerURL(newTrustedSequencerURL);
    }
}
