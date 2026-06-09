package claimer

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/log"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// LocalExitTreeReader is the subset of LocalExitTree the claimer depends on. *LocalExitTree
// satisfies it; tests can supply a fake.
type LocalExitTreeReader interface {
	DepositCount(leafHash common.Hash) (uint32, bool)
	Proof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (treetypes.Proof, error)
}

// ErrLocalExitRootNotSettled is returned when the certificate's NewLocalExitRoot cannot be found in
// the rollup exit tree of the selected L1 info tree leaf — i.e. the exit certificate has not been
// settled on L1 yet (or the chosen leaf predates settlement).
var ErrLocalExitRootNotSettled = errors.New("certificate new local exit root not settled in L1 info tree")

// Claimer assembles claimAsset parameters for the bridge exits of a settled exit certificate,
// combining the signed certificate, the L2 local exit tree, and the L1 Info Tree.
type Claimer struct {
	logger    *log.Logger
	networkID uint32
	cert      *Certificate
	localTree LocalExitTreeReader
	l1        L1InfoTreeQuerier
}

// NewClaimer wires the three data sources. networkID defaults to the certificate's network when 0.
func NewClaimer(
	logger *log.Logger,
	cert *Certificate,
	localTree LocalExitTreeReader,
	l1 L1InfoTreeQuerier,
	networkID uint32,
) *Claimer {
	if networkID == 0 {
		networkID = cert.NetworkID
	}
	return &Claimer{
		logger:    logger,
		networkID: networkID,
		cert:      cert,
		localTree: localTree,
		l1:        l1,
	}
}

// NetworkID returns the source network the claimer serves.
func (c *Claimer) NetworkID() uint32 { return c.networkID }

// ListBridges returns the certificate bridge exits destined to destAddr, enriched with the deposit
// count resolved from the local exit tree.
func (c *Claimer) ListBridges(destAddr common.Address) ([]BridgeExitView, error) {
	views := make([]BridgeExitView, 0)
	for i := range c.cert.Leaves {
		leaf := c.cert.Leaves[i]
		if leaf.DestinationAddress != destAddr {
			continue
		}
		depositCount, ok := c.localTree.DepositCount(leaf.Hash())
		if !ok {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) not found in local exit tree",
				i, leaf.Hash().Hex())
		}
		views = append(views, leaf.view(depositCount))
	}
	return views, nil
}

// BuildClaimParams returns the set of claimAsset arguments for the bridge exits destined to
// destAddr. The proofs are anchored to the latest indexed L1 info tree leaf. When depositCount is
// non-nil only the exit with that deposit count is returned (an address may have more than one
// pending deposit); when nil every matching exit is returned.
func (c *Claimer) BuildClaimParams(
	ctx context.Context, destAddr common.Address, depositCount *uint32,
) ([]ClaimAssetParams, error) {
	leaf, err := c.l1.GetLatestL1InfoLeaf(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading latest L1 info tree leaf: %w", err)
	}

	// Verify the certificate's new local exit root is the one settled for this network in the
	// selected L1 info tree leaf's rollup exit tree.
	settledLER, err := c.l1.GetLocalExitRoot(ctx, c.networkID, leaf.RollupExitRoot)
	if err != nil {
		return nil, fmt.Errorf("reading local exit root for network %d at L1 info leaf %d: %w",
			c.networkID, leaf.L1InfoTreeIndex, err)
	}
	if settledLER != c.cert.NewLocalExitRoot {
		return nil, fmt.Errorf("%w: network %d L1 info leaf %d has local exit root %s, certificate has %s",
			ErrLocalExitRootNotSettled, c.networkID, leaf.L1InfoTreeIndex,
			settledLER.Hex(), c.cert.NewLocalExitRoot.Hex())
	}

	rollupProof, err := c.l1.GetRollupExitTreeMerkleProof(ctx, c.networkID, leaf.RollupExitRoot)
	if err != nil {
		return nil, fmt.Errorf("building rollup exit tree proof for network %d: %w", c.networkID, err)
	}

	claims := make([]ClaimAssetParams, 0)
	for i := range c.cert.Leaves {
		certLeaf := c.cert.Leaves[i]
		if certLeaf.DestinationAddress != destAddr {
			continue
		}

		dc, ok := c.localTree.DepositCount(certLeaf.Hash())
		if !ok {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) not found in local exit tree",
				i, certLeaf.Hash().Hex())
		}

		if depositCount != nil && dc != *depositCount {
			continue
		}

		localProof, err := c.localTree.Proof(ctx, dc, c.cert.NewLocalExitRoot)
		if err != nil {
			return nil, fmt.Errorf("building local exit tree proof for deposit %d: %w", dc, err)
		}

		globalIndex := bridgesync.GenerateGlobalIndexForNetworkID(c.networkID, dc)

		claims = append(claims, ClaimAssetParams{
			SmtProofLocalExitRoot:  proofToHex(localProof),
			SmtProofRollupExitRoot: proofToHex(rollupProof),
			GlobalIndex:            globalIndex.String(),
			MainnetExitRoot:        leaf.MainnetExitRoot.Hex(),
			RollupExitRoot:         leaf.RollupExitRoot.Hex(),
			OriginNetwork:          certLeaf.OriginNetwork,
			OriginTokenAddress:     addrHex(certLeaf.OriginTokenAddress),
			DestinationNetwork:     certLeaf.DestinationNetwork,
			DestinationAddress:     addrHex(certLeaf.DestinationAddress),
			Amount:                 bigToString(certLeaf.Amount),
			Metadata:               metadataHex(certLeaf.Metadata),
			LeafType:               certLeaf.LeafType,
			DepositCount:           dc,
			L1InfoTreeIndex:        leaf.L1InfoTreeIndex,
		})
	}

	return claims, nil
}
