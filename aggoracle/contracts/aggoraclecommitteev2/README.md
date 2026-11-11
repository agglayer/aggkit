# AggOracleCommittee v2 bindings

Go bindings for the `AggOracleCommittee` **v2.0.0** contract
([`contracts/sovereignChains/AggOracleCommittee.sol`](https://github.com/agglayer/agglayer-contracts/blob/feature/agg-oracle-committee/contracts/sovereignChains/AggOracleCommittee.sol)
in `agglayer-contracts`, branch `feature/agg-oracle-committee`).

They live here and not in `github.com/0xPolygon/cdk-contracts-tooling` because the tooling release used by
aggkit only ships the **v1** committee (`aggoraclecommittee` package: `proposeGlobalExitRoot`, quorum...),
and v2 replaces v1 in the same Solidity file with the same contract name. The package name is different
(`aggoraclecommitteev2`) so both can be imported side by side.

The files are generated from a pinned commit of `agglayer-contracts` (see `PINNED_COMMIT` in
[`generate.sh`](generate.sh)):

```bash
git clone --branch feature/agg-oracle-committee git@github.com:agglayer/agglayer-contracts.git /tmp/agglayer-contracts
git -C /tmp/agglayer-contracts checkout <PINNED_COMMIT>
./generate.sh /tmp/agglayer-contracts
```

Do not edit the `.abi`, `.bin` and `.go` files by hand. Remove this package and use the one from
`cdk-contracts-tooling` once a release includes the v2 contract.

`aggoraclecommitteev2_test.go` checks that the ABI exposes the functions and errors aggkit relies on,
with the expected selectors.
