# End-to-end tests

This document enumerates and summarizes the e2e tests. The tests are implemented using [Bats framework](https://bats-core.readthedocs.io/en/stable/) and are assuming there is a running cluster to run them against. They are placed in the `test/bats` folder and divided into two major categories:
- the ones that involve single L2 (pessimistic proof) and L1 network. They are found in the `test/bats/pp` folder.
- the ones that involve two L2 (pessimistic proof) and single L1 network. They are found in the `test/bats/pp-multi` folder.
Reusable helper functions are placed in the `test/bats/helpers` folder and they consist of sending and claiming bridge transactions, fetching proofs, sending transactions, querying contracts etc. Most of the functions rely on the cast command from [Foundry](https://book.getfoundry.sh/cast/).

## Single L2 network

It involves single L2 network (and single L1 network), that are attached to the same agglayer.

### Transfer message

Bridges message from L1 to L2, by invoking `bridgeMessage` function on the bridge contract and then claiming once the global exit root is injected to the destination L2 network.

### Native gas token deposit to WETH

Bridges and claims native token from L1 to L2, that is mapped to the WETH token on L2.

### Test Bridge APIs workflow

Bridges the native token from L1 to L2 and then invokes the aggkit bridge service enpoints to verify they are working as expected: `bridge_getBridges`, `bridge_l1InfoTreeIndexForBridge`, `bridge_injectedInfoAfterIndex` and `bridge_claimProof`.

### Custom gas token deposit L1 -> L2

Bridges custom gas token, that pre-exists on L1 and is mapped to a native token on L2, claims it on the L2 and asserts that the native token balance has increased when settled on L2.

### Custom gas token withdrawal L2 -> L1

Bridges and claims native token on L2 network, that is pre-deployed and mapped to custom gas token on an L1 network and asserts that the gas token balance for the receiver address has increased after it got claimed on L1 network.

### ERC20 token deposit L1 -> L2

It deploys the ERC20 token on the L1 and bridges and claims it to the L2. In this process of claiming the bridge, a token representation of given ERC20 token is automatically deployed on the L2.

## Two L2 networks

It involves two L2 networks (and single L1 network), that are attached to the same agglayer.

### Test L2 to L2 bridge

It bridges native tokens from L1 to both L2 networks and claims them. Afterwards, it bridges from L2 (PP2) to L2 (PP1) network and claims it on the destination network.