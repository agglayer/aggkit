package claimtx

import (
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)

func TestPackClaimCalldataForAsset(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	proof := makeProof()

	data, err := PackClaim(request, proof)
	require.NoError(t, err)

	bridgeABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)
	require.Equal(t, bridgeABI.Methods[claimAssetMethod].ID, data[:4])
	inputs, err := bridgeABI.Methods[claimAssetMethod].Inputs.Unpack(data[4:])
	require.NoError(t, err)
	requireClaimInputs(t, inputs, request, proof)
}

func TestPackClaimCalldataForMessage(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeMessage)
	proof := makeProof()

	data, err := PackClaim(request, proof)
	require.NoError(t, err)

	bridgeABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	require.NoError(t, err)
	require.Equal(t, bridgeABI.Methods[claimMessageMethod].ID, data[:4])
	inputs, err := bridgeABI.Methods[claimMessageMethod].Inputs.Unpack(data[4:])
	require.NoError(t, err)
	requireClaimInputs(t, inputs, request, proof)
}

func TestGlobalIndexPreservesPreEtrogRawIndex(t *testing.T) {
	request := makeRequest(bridgesynctypes.LeafTypeAsset)
	request.Bridge.DestinationNetwork = autoclaimtypes.LegacyZkEVMRollupNetwork
	request.Bridge.PreEtrog = true
	request.Bridge.GlobalIndex = new(big.Int).SetUint64(uint64(request.Bridge.DepositCount))
	request.GlobalIndex = new(big.Int).Set(request.Bridge.GlobalIndex)

	require.Zero(t, GlobalIndex(request).Cmp(request.Bridge.GlobalIndex))
}

func requireClaimInputs(
	t *testing.T,
	inputs []any,
	request autoclaimtypes.AutoClaimRequest,
	proof autoclaimtypes.ClaimProof,
) {
	t.Helper()

	require.Len(t, inputs, 11)
	require.Equal(t, [32][32]byte(proof.ABILocalExitRoot), inputs[0])
	require.Equal(t, [32][32]byte(proof.ABIRollupExitRoot), inputs[1])
	globalIndex, ok := inputs[2].(*big.Int)
	require.True(t, ok)
	require.Zero(t, GlobalIndex(request).Cmp(globalIndex))
	require.Equal(t, [32]byte(proof.MainnetExitRoot), inputs[3])
	require.Equal(t, [32]byte(proof.RollupExitRoot), inputs[4])
	require.Equal(t, request.Bridge.OriginNetwork, inputs[5])
	require.Equal(t, request.Bridge.OriginAddress, inputs[6])
	require.Equal(t, request.Bridge.DestinationNetwork, inputs[7])
	require.Equal(t, request.Bridge.DestinationAddress, inputs[8])
	amount, ok := inputs[9].(*big.Int)
	require.True(t, ok)
	require.Zero(t, request.Bridge.Amount.Cmp(amount))
	require.Equal(t, request.Bridge.Metadata, inputs[10])
}

func makeRequest(leafType bridgesynctypes.LeafType) autoclaimtypes.AutoClaimRequest {
	bridge := autoclaimtypes.BridgeExit{
		BlockNum:           10,
		BlockPos:           2,
		TxHash:             common.HexToHash("0x1111"),
		LeafType:           leafType,
		OriginNetwork:      autoclaimtypes.L1OriginNetwork,
		OriginAddress:      common.HexToAddress("0x1000000000000000000000000000000000000001"),
		DestinationNetwork: 20,
		DestinationAddress: common.HexToAddress("0x2000000000000000000000000000000000000002"),
		Amount:             big.NewInt(12345),
		Metadata:           []byte{0xde, 0xad, 0xbe, 0xef},
		DepositCount:       7,
		GlobalIndex:        autoclaimtypes.DeriveGlobalIndex(autoclaimtypes.L1OriginNetwork, 7),
	}

	return autoclaimtypes.AutoClaimRequest{
		Key:         autoclaimtypes.DeriveRequestKey(bridge.OriginNetwork, bridge.DestinationNetwork, 7),
		Status:      autoclaimtypes.RequestStatusQueued,
		Bridge:      bridge,
		GlobalIndex: new(big.Int).Set(bridge.GlobalIndex),
		RetryCount:  0,
		MaxRetries:  1,
		CreatedAt:   fixedNow,
		UpdatedAt:   fixedNow,
	}
}

func makeProof() autoclaimtypes.ClaimProof {
	proof := autoclaimtypes.ClaimProof{
		MainnetExitRoot: common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
		RollupExitRoot:  common.HexToHash("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"),
		GlobalExitRoot:  common.HexToHash("0xcccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"),
		PreparedAt:      fixedNow,
	}
	proof.ABILocalExitRoot[0] = common.HexToHash("0x01")
	proof.ABILocalExitRoot[31] = common.HexToHash("0x02")
	proof.ABIRollupExitRoot[0] = common.HexToHash("0x03")
	proof.ABIRollupExitRoot[31] = common.HexToHash("0x04")
	return proof
}
