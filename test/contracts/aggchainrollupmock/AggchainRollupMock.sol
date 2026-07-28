// SPDX-License-Identifier: AGPL-3.0

pragma solidity 0.8.18;

/// @title AggchainRollupMock
/// @notice Minimal, test-only stand-in for a rollup/aggchain consensus contract. It exposes the
/// getters and events bridgeservicefinder depends on:
///   - trustedSequencerURL() / SetTrustedSequencerURL(string) -- present on both
///     polygonrollupbaseetrog and aggchainbase in the real bindings.
///   - aggchainMetadata(string) / AggchainMetadataSet(string indexed, string) -- present only on
///     aggchainbase in the real bindings.
/// Function/event signatures are matched EXACTLY (name + param types + indexed-ness) against the
/// real cdk-contracts-tooling bindings so that agglayerbase.NewAggchainbase(...) and
/// polygonrollupbaseetrog.NewPolygonrollupbaseetrog(...) can be pointed at a deployed instance of
/// this mock and correctly encode calls / decode return values and events.
contract AggchainRollupMock {
    // solhint-disable-next-line event-name-camelcase
    event SetTrustedSequencerURL(string newTrustedSequencerURL);
    event AggchainMetadataSet(string indexed key, string value);

    string internal _trustedSequencerURL;
    mapping(string => string) internal _aggchainMetadata;

    /// @notice Matches polygonrollupbaseetrog/aggchainbase trustedSequencerURL() exactly.
    function trustedSequencerURL() external view returns (string memory) {
        return _trustedSequencerURL;
    }

    /// @notice Test-only setter that stores the new URL and emits SetTrustedSequencerURL, exactly
    /// as the real contracts do when the trusted sequencer URL is updated.
    function setTrustedSequencerURL(string calldata newTrustedSequencerURL) external {
        _trustedSequencerURL = newTrustedSequencerURL;
        emit SetTrustedSequencerURL(newTrustedSequencerURL);
    }

    /// @notice Matches aggchainbase aggchainMetadata(string) exactly.
    function aggchainMetadata(string calldata key) external view returns (string memory) {
        return _aggchainMetadata[key];
    }

    /// @notice Test-only setter that stores the value keyed by `key` and emits
    /// AggchainMetadataSet(string indexed key, string value), exactly as aggchainbase does.
    function setAggchainMetadata(string calldata key, string calldata value) external {
        _aggchainMetadata[key] = value;
        emit AggchainMetadataSet(key, value);
    }
}
