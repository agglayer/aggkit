// Package aggchainqbft contains a minimal Go binding for the read-only surface of the AggchainQBFT
// L1 contract (docs/design/interfaces.md §8 of prover-besu-qbft) that aggkit needs.
//
// TODO: this binding is hand-maintained until the AggchainQBFT contract is merged and published;
// at that point it should be replaced by the generated one in github.com/0xPolygon/cdk-contracts-tooling.
package aggchainqbft

//go:generate sh -c "abigen --abi aggchainqbft.abi --pkg aggchainqbft --type Aggchainqbft --out aggchainqbft.go"
