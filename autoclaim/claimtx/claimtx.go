package claimtx

import (
	"fmt"
	"math/big"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	autoclaimtypes "github.com/agglayer/aggkit/autoclaim/types"
	bridgesynctypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/ethereum/go-ethereum/common"
)

const (
	claimAssetMethod   = "claimAsset"
	claimMessageMethod = "claimMessage"
)

// PackClaim packs bridge claim calldata for the request leaf type.
func PackClaim(request autoclaimtypes.AutoClaimRequest, proof autoclaimtypes.ClaimProof) ([]byte, error) {
	method := claimAssetMethod
	switch request.Bridge.LeafType {
	case bridgesynctypes.LeafTypeAsset:
	case bridgesynctypes.LeafTypeMessage:
		method = claimMessageMethod
	default:
		return nil, fmt.Errorf("unsupported bridge leaf type %d", request.Bridge.LeafType.Uint8())
	}

	bridgeABI, err := agglayerbridgel2.Agglayerbridgel2MetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("retrieve AgglayerBridgeL2 ABI: %w", err)
	}

	amount := request.Bridge.Amount
	if amount == nil {
		amount = common.Big0
	}

	data, err := bridgeABI.Pack(
		method,
		proof.ABILocalExitRoot,
		proof.ABIRollupExitRoot,
		GlobalIndex(request),
		proof.MainnetExitRoot,
		proof.RollupExitRoot,
		request.Bridge.OriginNetwork,
		request.Bridge.OriginAddress,
		request.Bridge.DestinationNetwork,
		request.Bridge.DestinationAddress,
		amount,
		request.Bridge.Metadata,
	)
	if err != nil {
		return nil, fmt.Errorf("pack %s claim calldata: %w", method, err)
	}

	return data, nil
}

// GlobalIndex returns the claim global index that must be submitted to the bridge.
func GlobalIndex(request autoclaimtypes.AutoClaimRequest) *big.Int {
	if request.GlobalIndex != nil {
		return new(big.Int).Set(request.GlobalIndex)
	}
	if request.Bridge.GlobalIndex != nil {
		return new(big.Int).Set(request.Bridge.GlobalIndex)
	}
	return autoclaimtypes.DeriveGlobalIndexForSource(request.Bridge.SourceNetwork, request.Bridge.DepositCount)
}
