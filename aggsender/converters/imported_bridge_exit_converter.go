package converters

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
)

// ConvertToImportedBridgeExitWithoutClaimData converts a bridgesync.Claim into an
// agglayertypes.ImportedBridgeExit without including claim-specific data. It determines
// the leaf type based on whether the claim is a message, converts the claim metadata,
// and constructs a BridgeExit object. The function also decodes the claim's global index
// into its mainnet flag, rollup index, and leaf index components. Returns the constructed
// ImportedBridgeExit or an error if the global index cannot be decoded.
func ConvertToImportedBridgeExitWithoutClaimData(
	claim bridgesync.Claim) (*agglayertypes.ImportedBridgeExit, error) {
	bridgeExit := ConvertBridgeExitFromClaim(claim)

	mainnetFlag, rollupIndex, leafIndex, err := bridgesync.DecodeGlobalIndex(claim.GlobalIndex)
	if err != nil {
		return nil, fmt.Errorf("error decoding global index: %w", err)
	}

	return &agglayertypes.ImportedBridgeExit{
		BridgeExit: bridgeExit,
		GlobalIndex: &agglayertypes.GlobalIndex{
			MainnetFlag: mainnetFlag,
			RollupIndex: rollupIndex,
			LeafIndex:   leafIndex,
		},
	}, nil
}

// ConvertBridgeExitFromClaim converts a bridgesync.Claim into an agglayertypes.BridgeExit.
// It extracts the core bridge exit information from the claim, including the leaf type
// (asset or message), token information, destination network and address, transfer amount,
// and metadata. This is a simplified conversion that does not include global index data
// or claim-specific proofs, making it suitable for scenarios where only the bridge exit
// data is needed without the full imported bridge exit context.
//
// Parameters:
//   - claim: bridgesync.Claim containing the claim data to convert.
//
// Returns:
//   - *agglayertypes.BridgeExit: The constructed bridge exit object with core claim data.
func ConvertBridgeExitFromClaim(claim bridgesync.Claim) *agglayertypes.BridgeExit {
	leafType := bridgetypes.LeafTypeAsset
	if claim.IsMessage {
		leafType = bridgetypes.LeafTypeMessage
	}
	metaData := convertBridgeMetadata(claim.Metadata)

	return &agglayertypes.BridgeExit{
		LeafType: leafType,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      claim.OriginNetwork,
			OriginTokenAddress: claim.OriginAddress,
		},
		DestinationNetwork: claim.DestinationNetwork,
		DestinationAddress: claim.DestinationAddress,
		Amount:             claim.Amount,
		Metadata:           metaData,
	}
}

// ConvertToImportedBridgeExit converts a bridgesync.Claim into an agglayertypes.ImportedBridgeExit.
// It determines the leaf type based on the claim, constructs the bridge exit data, decodes the global index,
// and retrieves the necessary Merkle proofs for the claim. Depending on whether the claim is from mainnet or rollup,
// it populates the appropriate claim data structure with Merkle proofs and L1 info tree leaf data.
//
// Parameters:
//   - ctx: context.Context for controlling cancellation and deadlines.
//   - convertClaimData: boolean indicating whether to convert claim data.
//   - claim: bridgesync.Claim containing the claim data to convert.
//   - rootFromWhichToProve: common.Hash representing the root used for Merkle proof generation.
//
// Returns:
//   - *agglayertypes.ImportedBridgeExit: The constructed imported bridge exit object.
//   - error: An error if any step in the conversion or proof retrieval fails.
func ConvertToImportedBridgeExit(
	ctx context.Context,
	claim bridgesync.Claim,
	rootFromWhichToProve common.Hash,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier) (*agglayertypes.ImportedBridgeExit, error) {
	ibe, err := ConvertToImportedBridgeExitWithoutClaimData(claim)
	if err != nil {
		return nil, fmt.Errorf("error converting claim to imported bridge exit without claim data: %w", err)
	}

	l1Info, gerToL1Proof, err := l1InfoTreeQuerier.GetProofForGER(ctx,
		claim.GlobalExitRoot, rootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf(
			"error getting L1 Info tree merkle proof for GER: %s and root: %s. Error: %w",
			claim.GlobalExitRoot, rootFromWhichToProve, err,
		)
	}

	if ibe.GlobalIndex.MainnetFlag {
		ibe.ClaimData = &agglayertypes.ClaimFromMainnet{
			L1Leaf: &agglayertypes.L1InfoTreeLeaf{
				L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
				RollupExitRoot:  claim.RollupExitRoot,
				MainnetExitRoot: claim.MainnetExitRoot,
				Inner: &agglayertypes.L1InfoTreeLeafInner{
					GlobalExitRoot: l1Info.GlobalExitRoot,
					Timestamp:      l1Info.Timestamp,
					BlockHash:      l1Info.PreviousBlockHash,
				},
			},
			ProofLeafMER: &agglayertypes.MerkleProof{
				Root:  claim.MainnetExitRoot,
				Proof: claim.ProofLocalExitRoot,
			},
			ProofGERToL1Root: &agglayertypes.MerkleProof{
				Root:  rootFromWhichToProve,
				Proof: gerToL1Proof,
			},
		}
	} else {
		ibe.ClaimData = &agglayertypes.ClaimFromRollup{
			L1Leaf: &agglayertypes.L1InfoTreeLeaf{
				L1InfoTreeIndex: l1Info.L1InfoTreeIndex,
				RollupExitRoot:  claim.RollupExitRoot,
				MainnetExitRoot: claim.MainnetExitRoot,
				Inner: &agglayertypes.L1InfoTreeLeafInner{
					GlobalExitRoot: l1Info.GlobalExitRoot,
					Timestamp:      l1Info.Timestamp,
					BlockHash:      l1Info.PreviousBlockHash,
				},
			},
			ProofLeafLER: &agglayertypes.MerkleProof{
				Root: tree.CalculateRoot(ibe.BridgeExit.Hash(),
					claim.ProofLocalExitRoot, ibe.GlobalIndex.LeafIndex),
				Proof: claim.ProofLocalExitRoot,
			},
			ProofLERToRER: &agglayertypes.MerkleProof{
				Root:  claim.RollupExitRoot,
				Proof: claim.ProofRollupExitRoot,
			},
			ProofGERToL1Root: &agglayertypes.MerkleProof{
				Root:  rootFromWhichToProve,
				Proof: gerToL1Proof,
			},
		}
	}

	return ibe, nil
}

// ConvertToImportedBridgeExits converts a slice of bridgesync.Claim objects into a slice of
// agglayertypes.ImportedBridgeExit objects. It iterates over each claim, logging relevant
// information and converting each claim using the ConvertToImportedBridgeExit method.
// If any conversion fails, it returns an error. The function returns the resulting slice
// of ImportedBridgeExit objects or an empty slice if no claims are provided.
//
// Parameters:
//   - ctx: context.Context for controlling cancellation and deadlines.
//   - claims: a slice of bridgesync.Claim objects to be converted.
//   - rootFromWhichToProve: a common.Hash representing the root used for proof.
//   - l1InfoTreeQuerier: a types.L1InfoTreeDataQuerier for retrieving L1 info tree data.
//
// Returns:
//   - A slice of pointers to agglayertypes.ImportedBridgeExit objects.
//   - An error if any claim fails to convert.
func ConvertToImportedBridgeExits(
	ctx context.Context,
	claims []bridgesync.Claim,
	rootFromWhichToProve common.Hash,
	l1InfoTreeQuerier types.L1InfoTreeDataQuerier,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	if len(claims) == 0 {
		// no claims to convert
		return []*agglayertypes.ImportedBridgeExit{}, nil
	}

	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExit, 0, len(claims))

	for _, claim := range claims {
		ibe, err := ConvertToImportedBridgeExit(ctx, claim, rootFromWhichToProve, l1InfoTreeQuerier)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit: %w", err)
		}

		importedBridgeExits = append(importedBridgeExits, ibe)
	}

	return importedBridgeExits, nil
}

// ConvertToImportedBridgeExitsWithoutClaimData converts a slice of bridgesync.Claim
// objects to a slice of agglayertypes.ImportedBridgeExit objects without claim data.
// It returns an empty slice if no claims are provided, and returns an error if any
// individual claim conversion fails.
// This is useful when the claim data is not needed, such as when we don't want to
// use the debug endpoint to retrieve the claim data.
//
// Parameters:
//   - claims: A slice of bridgesync.Claim objects to be converted
//
// Returns:
//   - A slice of *agglayertypes.ImportedBridgeExit objects on success
//   - An error if any claim conversion fails
func ConvertToImportedBridgeExitsWithoutClaimData(
	claims []bridgesync.Claim,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	if len(claims) == 0 {
		// no claims to convert
		return []*agglayertypes.ImportedBridgeExit{}, nil
	}

	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExit, 0, len(claims))

	for _, claim := range claims {
		ibe, err := ConvertToImportedBridgeExitWithoutClaimData(claim)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit without claim data: %w", err)
		}

		importedBridgeExits = append(importedBridgeExits, ibe)
	}

	return importedBridgeExits, nil
}
