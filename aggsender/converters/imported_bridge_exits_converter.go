package converters

import (
	"context"
	"fmt"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
)

// ImportedBridgeExitConverter is responsible for converting imported bridge exit data.
// It utilizes a logger for logging operations and an L1InfoTreeDataQuerier to query
// Layer 1 information tree data required during the conversion process.
type ImportedBridgeExitConverter struct {
	log                   types.Logger
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier
}

// NewImportedBridgeExitConverter creates a new instance of ImportedBridgeExitConverter
func NewImportedBridgeExitConverter(
	log types.Logger,
	l1InfoTreeDataQuerier types.L1InfoTreeDataQuerier,
) *ImportedBridgeExitConverter {
	return &ImportedBridgeExitConverter{
		log:                   log,
		l1InfoTreeDataQuerier: l1InfoTreeDataQuerier,
	}
}

// ConvertToImportedBridgeExitWithoutClaimData converts a bridgesync.Claim into an
// agglayertypes.ImportedBridgeExit without including claim-specific data. It determines
// the leaf type based on whether the claim is a message, converts the claim metadata,
// and constructs a BridgeExit object. The function also decodes the claim's global index
// into its mainnet flag, rollup index, and leaf index components. Returns the constructed
// ImportedBridgeExit or an error if the global index cannot be decoded.
func (f *ImportedBridgeExitConverter) ConvertToImportedBridgeExitWithoutClaimData(
	claim bridgesync.Claim) (*agglayertypes.ImportedBridgeExit, error) {
	leafType := agglayertypes.LeafTypeAsset
	if claim.IsMessage {
		leafType = agglayertypes.LeafTypeMessage
	}
	metaData := convertBridgeMetadata(claim.Metadata)

	bridgeExit := &agglayertypes.BridgeExit{
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
func (f *ImportedBridgeExitConverter) ConvertToImportedBridgeExit(
	ctx context.Context,
	claim bridgesync.Claim,
	rootFromWhichToProve common.Hash) (*agglayertypes.ImportedBridgeExit, error) {
	ibe, err := f.ConvertToImportedBridgeExitWithoutClaimData(claim)
	if err != nil {
		return nil, fmt.Errorf("error converting claim to imported bridge exit without claim data: %w", err)
	}

	l1Info, gerToL1Proof, err := f.l1InfoTreeDataQuerier.GetProofForGER(ctx,
		claim.GlobalExitRoot, rootFromWhichToProve)
	if err != nil {
		return nil, fmt.Errorf(
			"error getting L1 Info tree merkle proof for GER: %s and root: %s. Error: %w",
			claim.GlobalExitRoot, rootFromWhichToProve, err,
		)
	}

	if ibe.GlobalIndex.MainnetFlag {
		ibe.ClaimData = &agglayertypes.ClaimFromMainnnet{
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
//
// Returns:
//   - A slice of pointers to agglayertypes.ImportedBridgeExit objects.
//   - An error if any claim fails to convert.
func (f *ImportedBridgeExitConverter) ConvertToImportedBridgeExits(
	ctx context.Context,
	claims []bridgesync.Claim,
	rootFromWhichToProve common.Hash,
) ([]*agglayertypes.ImportedBridgeExit, error) {
	if len(claims) == 0 {
		// no claims to convert
		return []*agglayertypes.ImportedBridgeExit{}, nil
	}

	importedBridgeExits := make([]*agglayertypes.ImportedBridgeExit, 0, len(claims))

	for i, claim := range claims {
		f.log.Debugf("claim[%d]: destAddr: %s GER: %s Block: %d Pos: %d GlobalIndex: 0x%x",
			i, claim.DestinationAddress.String(), claim.GlobalExitRoot.String(),
			claim.BlockNum, claim.BlockPos, claim.GlobalIndex)
		ibe, err := f.ConvertToImportedBridgeExit(ctx, claim, rootFromWhichToProve)
		if err != nil {
			return nil, fmt.Errorf("error converting claim to imported bridge exit: %w", err)
		}

		importedBridgeExits = append(importedBridgeExits, ibe)
	}

	return importedBridgeExits, nil
}
