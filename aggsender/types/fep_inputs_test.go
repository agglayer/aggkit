package types

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var aggregationProofPublicValuesTestExample = &AggregationProofPublicValues{
	L1Head:              common.HexToHash("0x502cbcfe9aa2a7c4fbd1fcf81ce71be6f1a79a904b31a2b1cf27e5179f970890"),
	L2PreRoot:           common.HexToHash("0xb744b55eba3192d84812aa068e6db062cdccce9364d77515dee1ac3ac9e4a175"),
	ClaimRoot:           common.HexToHash("0x98280091281a3d554b53537892f86cbb3a38ff83528c39ac0cf52be251269a7d"),
	L2BlockNumber:       126697,
	RollupConfigHash:    common.HexToHash("0xfd94d7ab6f4376bbb317864bd08cd240bff6f99dbec0755db1aa8e5ef0705a4a"),
	MultiBlockVKey:      common.HexToHash("0x35882a76205af8c12eaeea7551ff8dbc392dc2a95b0f7f31660a5468237d4434"),
	TrustedSigner:       common.HexToAddress("0x4ce23a785114db45ac6351e02f0de440845351af"),
	AggregationVKeyHash: common.HexToHash("0x0000000000000000000000000000000000000000000000000000000000000001"),
}

func TestAggregationProofPublicValues_Hash(t *testing.T) {
	aggHash, err := aggregationProofPublicValuesTestExample.Hash()
	require.NoError(t, err, "Hashing should not return an error")
	expectedHash := common.HexToHash("0x8a357a4700f590c977d5b3c82448239d2a883fe51165abcab301ecbed9e2730b")
	require.Equal(t, expectedHash, aggHash, "Hash should match the expected value")
}

func TestAggregationProofPublicValues_String(t *testing.T) {
	expectedStr := `AggregationProofPublicValues{l1Head: 0x502cbcfe9aa2a7c4fbd1fcf81ce71be6f1a79a904b31a2b1cf27e5179f970890, l2PreRoot: 0xb744b55eba3192d84812aa068e6db062cdccce9364d77515dee1ac3ac9e4a175, claimRoot: 0x98280091281a3d554b53537892f86cbb3a38ff83528c39ac0cf52be251269a7d, l2BlockNumber: 126697, rollupConfigHash: 0xfd94d7ab6f4376bbb317864bd08cd240bff6f99dbec0755db1aa8e5ef0705a4a, multiBlockVKey: 0x35882a76205af8c12eaeea7551ff8dbc392dc2a95b0f7f31660a5468237d4434, trustedSignerAddress: 0x4ce23A785114Db45aC6351E02F0dE440845351Af, aggregationVKeyHash: 0x0000000000000000000000000000000000000000000000000000000000000001}`
	require.Equal(t, expectedStr, aggregationProofPublicValuesTestExample.String())
}

func TestAggchainParams_Hash(t *testing.T) {
	// this expected hash value was calculated in a kurtois-cdk setup on aggkit-prover
	// using real data provided in the aggchainParams variable below
	// [aggkit-prover-001] {"timestamp":"2025-10-14T12:27:14.246560Z","level":"INFO","fields":{"message":"Aggchain-params unrolled values: AggchainParamsValues
	// { l2_pre_root: 0x28f97583aa1d73ecd3838c2c1007e26f81aa522ab49d1b3731b48a4ba2ed918a,
	// claim_root: 0xc6bc97efacff37a2a05adbe03c8a6f0fb0e322538744c4b7f53d784fff4abc3c,
	// claim_block_num: 61, rollup_config_hash: 0xc6e1bc6dad7ad983e435b14924dd450024119cda78eebca10429019d1cb55fd3,
	// optimistic_mode: false,
	// trusted_sequencer: 0x5b06837a43bdc3dd9f114558daf4b26ed49842ed,
	// range_vkey_commitment: 0x416d710344b6b6fa2a0b1a1445f3d6ba4fdd5ab43f0e863b1c522db20f28ad9b,
	// aggregation_vkey_hash: 0x00afb45d8064ae10aa6a1793b8f39a24c27268efae2917b5c02950b2377fbf00 };
	// Aggchain-params keccak-hashed: 0xba25eb4d0a1e9f9637b3c43568a72d1ecfc084609ba85127697f5440730e5d2e"},
	// "target":"aggchain_proof_builder","span":{"name":"generate_aggchain_proof"},"spans":[{"name":"generate_aggchain_proof"}]}]

	aggchainParams := &AggchainParams{
		AggregationProofPublicValues: AggregationProofPublicValues{
			L2PreRoot:           common.HexToHash("0x28f97583aa1d73ecd3838c2c1007e26f81aa522ab49d1b3731b48a4ba2ed918a"),
			ClaimRoot:           common.HexToHash("0xc6bc97efacff37a2a05adbe03c8a6f0fb0e322538744c4b7f53d784fff4abc3c"),
			L2BlockNumber:       61,
			RollupConfigHash:    common.HexToHash("0xc6e1bc6dad7ad983e435b14924dd450024119cda78eebca10429019d1cb55fd3"),
			TrustedSigner:       common.HexToAddress("0x5b06837A43bdC3dD9F114558DAf4B26ed49842Ed"),
			MultiBlockVKey:      common.HexToHash("0x416d710344b6b6fa2a0b1a1445f3d6ba4fdd5ab43f0e863b1c522db20f28ad9b"),
			AggregationVKeyHash: common.HexToHash("0x00afb45d8064ae10aa6a1793b8f39a24c27268efae2917b5c02950b2377fbf00"),
		},
		OptimisticMode: false,
	}

	hash, err := aggchainParams.Hash()
	require.NoError(t, err)

	expectedHash := common.HexToHash("0xba25eb4d0a1e9f9637b3c43568a72d1ecfc084609ba85127697f5440730e5d2e")
	require.Equal(t, expectedHash, hash)
}
