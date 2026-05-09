package backward_forward_let

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridgel2"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	"github.com/agglayer/aggkit/bridgesync"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	gethTypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

const (
	decimalBase         = 10
	merkleZeroHashCount = 33 // 32 tree levels + 1 leaf level
)

// BridgeExitToLeafData converts an agglayer BridgeExit to a bridgesync.LeafData.
func BridgeExitToLeafData(be *agglayertypes.BridgeExit) bridgesync.LeafData {
	amount := be.Amount
	if amount == nil {
		amount = big.NewInt(0)
	}
	var originAddr common.Address
	var originNetwork uint32
	if be.TokenInfo != nil {
		originAddr = be.TokenInfo.OriginTokenAddress
		originNetwork = be.TokenInfo.OriginNetwork
	}
	return bridgesync.LeafData{
		LeafType:           be.LeafType.Uint8(),
		OriginNetwork:      originNetwork,
		OriginAddress:      originAddr,
		DestinationNetwork: be.DestinationNetwork,
		DestinationAddress: be.DestinationAddress,
		Amount:             amount,
		Metadata:           be.Metadata,
	}
}

// BridgeResponseToLeafData converts a bridge service BridgeResponse to a bridgesync.LeafData.
func BridgeResponseToLeafData(br *bridgeservicetypes.BridgeResponse) bridgesync.LeafData {
	amount := parseAmount(string(br.Amount))
	return bridgesync.LeafData{
		LeafType:           br.LeafType,
		OriginNetwork:      br.OriginNetwork,
		OriginAddress:      common.HexToAddress(string(br.OriginAddress)),
		DestinationNetwork: br.DestinationNetwork,
		DestinationAddress: common.HexToAddress(string(br.DestinationAddress)),
		Amount:             amount,
		Metadata:           decodeMetadata(br.Metadata),
	}
}

// BridgeExitLeafHash returns the leaf hash for a BridgeExit using BridgeExit.Hash().
func BridgeExitLeafHash(be *agglayertypes.BridgeExit) common.Hash {
	return be.Hash()
}

// BridgeResponseLeafHash computes the leaf hash for a BridgeResponse using the same
// algorithm as BridgeExit.Hash().
// The bridge service stores raw metadata (from the BridgeEvent). The contract's getLeafValue
// takes keccak256(rawMetadata), so we hash it here — matching convertBridgeMetadata in aggsender.
func BridgeResponseLeafHash(br *bridgeservicetypes.BridgeResponse) common.Hash {
	amount := parseAmount(string(br.Amount))
	metadata := decodeMetadata(br.Metadata)

	return crypto.Keccak256Hash(
		[]byte{br.LeafType},
		aggkitcommon.Uint32ToBigEndianBytes(br.OriginNetwork),
		common.HexToAddress(string(br.OriginAddress)).Bytes(),
		aggkitcommon.Uint32ToBigEndianBytes(br.DestinationNetwork),
		common.HexToAddress(string(br.DestinationAddress)).Bytes(),
		common.BigToHash(amount).Bytes(),
		crypto.Keccak256(metadata),
	)
}

// parseAmount parses a decimal string (possibly empty) to a *big.Int. Returns 0 on failure.
func parseAmount(s string) *big.Int {
	if s == "" {
		return big.NewInt(0)
	}
	n, ok := new(big.Int).SetString(s, decimalBase)
	if !ok {
		return big.NewInt(0)
	}
	return n
}

// decodeMetadata decodes a "0x..."-prefixed hex string to raw bytes. Returns nil for empty/invalid input.
func decodeMetadata(s string) []byte {
	s = strings.TrimPrefix(s, "0x")
	if s == "" {
		return nil
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil
	}
	return b
}

// makeZeroHashes returns 33 pre-computed zero hashes for a 32-level Merkle tree.
// Index 0 is the empty hash; index h = Keccak256(zeroHashes[h-1], zeroHashes[h-1]) for h >= 1.
func makeZeroHashes() []common.Hash {
	zeros := make([]common.Hash, merkleZeroHashCount)
	zeros[0] = common.Hash{}
	for i := 1; i <= 32; i++ {
		zeros[i] = crypto.Keccak256Hash(zeros[i-1].Bytes(), zeros[i-1].Bytes())
	}
	return zeros
}

// computeFrontier simulates an append-only Merkle tree for leaf indices 0..targetIndex-1
// and returns the resulting frontier (lastLeftCache). The frontier[h] holds the left sibling
// at height h, ready to pair with the next right child.
//
// The returned frontier uses bytes32(0) (literal zero bytes) for positions that have not been
// set by any leaf insertion. This matches the contract's initial storage state and is required
// by _checkValidSubtreeFrontier, which rejects non-zero values in unused positions.
func computeFrontier(leafHashes []common.Hash, targetIndex uint32) ([32]common.Hash, error) {
	target := int(targetIndex)
	if len(leafHashes) < target {
		return [32]common.Hash{}, fmt.Errorf(
			"insufficient leaf hashes: need %d, got %d", targetIndex, len(leafHashes),
		)
	}

	zeros := makeZeroHashes()
	// frontier is zero-initialized by Go (all common.Hash{} = bytes32(0)), matching the
	// contract's initial _branch storage state before any leaves are inserted.
	var frontier [32]common.Hash

	for i := 0; i < target; i++ {
		node := leafHashes[i] //nolint:gosec // i is bounded by len(leafHashes) through target.
		leafIndex := uint32(i)
		for h := range 32 {
			if (leafIndex>>h)&1 == 0 {
				// Left child: cache node at this height, propagate up with zero sibling.
				frontier[h] = node
				node = crypto.Keccak256Hash(node.Bytes(), zeros[h].Bytes())
			} else {
				// Right child: pair with cached left sibling, propagate up.
				node = crypto.Keccak256Hash(frontier[h].Bytes(), node.Bytes())
			}
		}
	}

	// Zero out positions where bit h of targetIndex is 0. These are stale values
	// from earlier leaf insertions and must be zero for the contract's
	// _checkValidSubtreeFrontier, which rejects non-zero values in inactive positions.
	for h := range 32 {
		if (targetIndex>>h)&1 == 0 {
			frontier[h] = common.Hash{}
		}
	}

	return frontier, nil
}

// computeMerkleProof computes a Merkle proof for the leaf at targetIndex in a tree built
// from allLeafHashes. The proof can be verified with tree.CalculateRoot(leaf, proof, targetIndex).
func computeMerkleProof(allLeafHashes []common.Hash, targetIndex uint32) ([32]common.Hash, error) {
	if uint32(len(allLeafHashes)) <= targetIndex {
		return [32]common.Hash{}, fmt.Errorf(
			"targetIndex %d out of range (len=%d)", targetIndex, len(allLeafHashes),
		)
	}

	zeros := makeZeroHashes()
	var proof [32]common.Hash

	currentLevel := make([]common.Hash, len(allLeafHashes))
	copy(currentLevel, allLeafHashes)

	idx := targetIndex
	for h := range 32 {
		sibling := idx ^ 1
		if sibling < uint32(len(currentLevel)) {
			proof[h] = currentLevel[sibling]
		} else {
			proof[h] = zeros[h]
		}

		// Build next level by pairing consecutive nodes.
		nextLen := (len(currentLevel) + 1) / 2 //nolint:mnd // binary tree pairing
		nextLevel := make([]common.Hash, nextLen)
		for j := range nextLen {
			left := currentLevel[2*j]
			var right common.Hash
			if 2*j+1 < len(currentLevel) {
				right = currentLevel[2*j+1]
			} else {
				right = zeros[h]
			}
			nextLevel[j] = crypto.Keccak256Hash(left.Bytes(), right.Bytes())
		}

		currentLevel = nextLevel
		idx >>= 1
	}

	return proof, nil
}

// ComputeBackwardLETParams computes the three parameters required for a BackwardLET call:
//   - frontier: the append-only tree frontier after inserting leaves 0..targetIndex-1
//   - nextLeaf: the hash of the leaf at targetIndex (the leaf being "rolled back from")
//   - proof: a Merkle proof that nextLeaf is at targetIndex in the full tree
func ComputeBackwardLETParams(
	allLeafHashes []common.Hash,
	targetIndex uint32,
) (frontier [32]common.Hash, nextLeaf common.Hash, proof [32]common.Hash, err error) {
	if uint32(len(allLeafHashes)) <= targetIndex {
		err = fmt.Errorf("targetIndex %d out of range (len=%d)", targetIndex, len(allLeafHashes))
		return
	}

	frontier, err = computeFrontier(allLeafHashes, targetIndex)
	if err != nil {
		return
	}

	nextLeaf = allLeafHashes[targetIndex]

	proof, err = computeMerkleProof(allLeafHashes, targetIndex)
	return
}

// computeRootFromFrontier continues the append-only tree simulation starting from a given
// frontier and existingCount, inserting newLeafHashes. It returns the Merkle root after all
// new leaves have been inserted.
func computeRootFromFrontier(
	frontier [32]common.Hash,
	existingCount uint32,
	newLeafHashes []common.Hash,
) (common.Hash, error) {
	if len(newLeafHashes) == 0 {
		return common.Hash{}, fmt.Errorf("newLeafHashes must not be empty")
	}

	zeros := makeZeroHashes()

	// Work on a local copy of the frontier so callers are not affected.
	f := frontier
	var root common.Hash

	for j, leafHash := range newLeafHashes {
		actualIndex := existingCount + uint32(j)
		node := leafHash
		for h := range 32 {
			if (actualIndex>>h)&1 == 0 {
				f[h] = node
				node = crypto.Keccak256Hash(node.Bytes(), zeros[h].Bytes())
			} else {
				node = crypto.Keccak256Hash(f[h].Bytes(), node.Bytes())
			}
		}
		root = node
	}

	return root, nil
}

// ComputeLERForNewLeaves computes the LET Merkle root after appending newLeafHashes
// to an existing tree described by existingLeafHashes.
func ComputeLERForNewLeaves(existingLeafHashes []common.Hash, newLeafHashes []common.Hash) (common.Hash, error) {
	n := uint32(len(existingLeafHashes))
	frontier, err := computeFrontier(existingLeafHashes, n)
	if err != nil {
		return common.Hash{}, err
	}
	return computeRootFromFrontier(frontier, n, newLeafHashes)
}

// leafDataLeafHash computes the Merkle leaf hash for a bridgesync.LeafData using the same
// algorithm as BridgeExit.Hash().
// LeafData.Metadata contains raw bytes (from the bridge event). The contract hashes it with
// keccak256 before computing the leaf hash, so we do the same here.
func leafDataLeafHash(ld bridgesync.LeafData) common.Hash {
	amount := ld.Amount
	if amount == nil {
		amount = big.NewInt(0)
	}
	return crypto.Keccak256Hash(
		[]byte{ld.LeafType},
		aggkitcommon.Uint32ToBigEndianBytes(ld.OriginNetwork),
		ld.OriginAddress.Bytes(),
		aggkitcommon.Uint32ToBigEndianBytes(ld.DestinationNetwork),
		ld.DestinationAddress.Bytes(),
		common.BigToHash(amount).Bytes(),
		crypto.Keccak256(ld.Metadata),
	)
}

// fetchL2LeafHashesUpTo fetches L2 bridge leaf hashes for deposit counts 0..endDC-1 from the
// bridge service and returns them in order.
func fetchL2LeafHashesUpTo(ctx context.Context, env *Env, endDC uint32) ([]common.Hash, error) {
	hashes := make([]common.Hash, 0, endDC)
	for dc := uint32(0); dc < endDC; dc++ {
		br, err := env.BridgeService.GetBridgeByDepositCount(ctx, env.L2NetworkID, dc)
		if err != nil {
			return nil, fmt.Errorf("get L2 bridge at DC=%d: %w", dc, err)
		}
		hashes = append(hashes, BridgeResponseLeafHash(br))
	}
	return hashes, nil
}

// buildTransactOpts creates a bind.TransactOpts for the given signer config and L2 chain ID.
func buildTransactOpts(
	ctx context.Context,
	cfg signertypes.SignerConfig,
	l2ChainID *big.Int,
	name string,
) (*bind.TransactOpts, error) {
	s, err := signer.NewSigner(ctx, l2ChainID.Uint64(), cfg, name, log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("load %s signer: %w", name, err)
	}
	if err := s.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize %s signer: %w", name, err)
	}
	opts := &bind.TransactOpts{
		From: s.PublicAddress(),
		Signer: func(_ common.Address, tx *gethTypes.Transaction) (*gethTypes.Transaction, error) {
			return s.SignTx(ctx, tx)
		},
	}
	return opts, nil
}

// waitForReceipt waits for the transaction to be mined and returns its receipt.
func waitForReceipt(
	ctx context.Context, client *ethclient.Client, tx *gethTypes.Transaction,
) (*gethTypes.Receipt, error) {
	return bind.WaitMined(ctx, client, tx)
}

// bridgeExitToContractLeaf converts an agglayer BridgeExit to the contract leaf type.
func bridgeExitToContractLeaf(be *agglayertypes.BridgeExit) agglayerbridgel2.AgglayerBridgeL2LeafData {
	var originNetwork uint32
	var originAddr common.Address
	if be.TokenInfo != nil {
		originNetwork = be.TokenInfo.OriginNetwork
		originAddr = be.TokenInfo.OriginTokenAddress
	}
	amount := be.Amount
	if amount == nil {
		amount = big.NewInt(0)
	}
	return agglayerbridgel2.AgglayerBridgeL2LeafData{
		LeafType:           be.LeafType.Uint8(),
		OriginNetwork:      originNetwork,
		OriginAddress:      originAddr,
		DestinationNetwork: be.DestinationNetwork,
		DestinationAddress: be.DestinationAddress,
		Amount:             amount,
		Metadata:           be.Metadata,
	}
}

// leafDataToContractLeaf converts a bridgesync.LeafData to the contract leaf type.
func leafDataToContractLeaf(ld bridgesync.LeafData) agglayerbridgel2.AgglayerBridgeL2LeafData {
	amount := ld.Amount
	if amount == nil {
		amount = big.NewInt(0)
	}
	return agglayerbridgel2.AgglayerBridgeL2LeafData{
		LeafType:           ld.LeafType,
		OriginNetwork:      ld.OriginNetwork,
		OriginAddress:      ld.OriginAddress,
		DestinationNetwork: ld.DestinationNetwork,
		DestinationAddress: ld.DestinationAddress,
		Amount:             amount,
		Metadata:           ld.Metadata,
	}
}
