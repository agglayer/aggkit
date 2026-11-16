package chaingersender

import (
	"testing"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggoraclecommittee"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// TestCommitteeV2ABISelectors checks that the binding of the AggOracleCommittee v2 (cdk-contracts-tooling) exposes
// the functions and errors that aggkit relies on with the expected selectors. If the contract changes (and the
// binding is regenerated) and any of them changes, aggkit must be adapted: the mode detector probes some of these
// selectors. It also catches a version of cdk-contracts-tooling whose aggoraclecommittee is still the v1.
func TestCommitteeV2ABISelectors(t *testing.T) {
	parsed, err := aggoraclecommittee.AggoraclecommitteeMetaData.GetAbi()
	require.NoError(t, err)

	functions := map[string]string{
		"version":           "0x54fd4d50",
		"threshold":         "0x42cde4e8",
		"proposer":          "0xa8e4fb90",
		"agglayerGERL2":     "0x56eb7637",
		"getValidatorsInfo": "",
		"injectedGERs":      "",
		"verifyGER":         "",
		"injectGER":         "",
	}
	signatures := map[string]string{
		"version":           "version()",
		"threshold":         "threshold()",
		"proposer":          "proposer()",
		"agglayerGERL2":     "agglayerGERL2()",
		"getValidatorsInfo": "getValidatorsInfo()",
		"injectedGERs":      "injectedGERs(bytes32)",
		"verifyGER":         "verifyGER(bytes32,bytes)",
		"injectGER":         "injectGER(bytes32,bytes)",
	}
	for name, expectedSelector := range functions {
		method, ok := parsed.Methods[name]
		require.True(t, ok, "method %s not found in the ABI", name)
		require.Equal(t, signatures[name], method.Sig, "unexpected signature for %s", name)
		require.Equal(t, crypto.Keccak256([]byte(method.Sig))[:4], method.ID, "selector of %s", name)
		if expectedSelector != "" {
			require.Equal(t, expectedSelector, hexutil.Encode(method.ID), "selector of %s", name)
		}
	}

	errorSelectors := map[string]string{
		"GERAlreadyInjected":           "0x7bde2647",
		"InvalidSignaturesLength":      "0xfb6c7304",
		"ValidatorDoesNotExist":        "0xe51315d2",
		"ValidatorsNotOrdered":         "0xe211dd1e",
		"OnlyAggOracleProposerCanCall": "0x85d8f64f",
	}
	for name, expectedSelector := range errorSelectors {
		abiErr, ok := parsed.Errors[name]
		require.True(t, ok, "error %s not found in the ABI", name)
		require.Equal(t, expectedSelector, hexutil.Encode(abiErr.ID[:4]), "selector of error %s", name)
	}

	// The v1 functions are gone: it is the v2 and not the old committee
	for _, v1Function := range []string{"proposeGlobalExitRoot", "quorum", "getAggOracleMemberIndex"} {
		_, ok := parsed.Methods[v1Function]
		require.False(t, ok, "%s is a function of the AggOracleCommittee v1", v1Function)
	}
}

func TestCommitteeV2ABIInjectGERPacking(t *testing.T) {
	parsed, err := aggoraclecommittee.AggoraclecommitteeMetaData.GetAbi()
	require.NoError(t, err)

	ger := [32]byte{0x01}
	signatures := make([]byte, 65*2)
	data, err := parsed.Pack("injectGER", ger, signatures)
	require.NoError(t, err)
	require.Equal(t, parsed.Methods["injectGER"].ID, data[:4])

	args, err := parsed.Methods["injectGER"].Inputs.Unpack(data[4:])
	require.NoError(t, err)
	require.Equal(t, ger, args[0])
	require.Equal(t, signatures, args[1])
}
