package claimer

import (
	"context"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// LocalExitTreeReader is the subset of LocalExitTree the claimer depends on. *LocalExitTree
// satisfies it; tests can supply a fake.
type LocalExitTreeReader interface {
	DepositCount(leafHash common.Hash) (uint32, bool)
	Metadata(leafHash common.Hash) ([]byte, bool)
	Proof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (treetypes.Proof, error)
}

// ErrLocalExitRootNotSettled is returned when the certificate's NewLocalExitRoot cannot be found in
// the rollup exit tree of the selected L1 info tree leaf — i.e. the exit certificate has not been
// settled on L1 yet (or the chosen leaf predates settlement).
var ErrLocalExitRootNotSettled = errors.New("certificate new local exit root not settled in L1 info tree")

// Claimer assembles claimAsset parameters for the bridge exits of a settled exit certificate,
// combining the signed certificate, the L2 local exit tree, and the L1 Info Tree.
type Claimer struct {
	logger     *log.Logger
	networkID  uint32
	cert       *Certificate
	localTree  LocalExitTreeReader
	l1         L1InfoTreeQuerier
	waitResult *exitcertificate.StepWaitResult
}

// NewClaimer wires the data sources. networkID defaults to the certificate's network when 0.
// waitResult is the exit_certificate WAIT step output recording the certificate's L1 settlement.
func NewClaimer(
	logger *log.Logger,
	cert *Certificate,
	localTree LocalExitTreeReader,
	l1 L1InfoTreeQuerier,
	networkID uint32,
	waitResult *exitcertificate.StepWaitResult,
) *Claimer {
	if networkID == 0 {
		networkID = cert.NetworkID
	}
	return &Claimer{
		logger:     logger,
		networkID:  networkID,
		cert:       cert,
		localTree:  localTree,
		l1:         l1,
		waitResult: waitResult,
	}
}

// NetworkID returns the source network the claimer serves.
func (c *Claimer) NetworkID() uint32 { return c.networkID }

// SettlementWaitResult returns the exit_certificate WAIT step output recording where on L1 the
// certificate settled (the VerifyBatchesTrustedAggregator event and the accompanying L1 Info Tree
// update). It may be nil if no wait result was loaded.
func (c *Claimer) SettlementWaitResult() *exitcertificate.StepWaitResult { return c.waitResult }

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
			return nil, fmt.Errorf("error: bridge exit[%d]  (leaf %s) not found in local exit tree",
				i, leaf.Hash().Hex())
		}
		views = append(views, leaf.view(depositCount))
	}
	return views, nil
}

// BuildClaimParams returns the set of claimAsset arguments for the bridge exits destined to
// destAddr. The proofs are anchored to the L1 info tree leaf the certificate settled at (the leaf
// carrying the GER recorded by the WAIT step), not the latest leaf: the certificate's
// NewLocalExitRoot is only present in the rollup exit tree of that settlement leaf, and a later leaf
// would carry a newer rollup exit root that no longer contains it. When depositCount is non-nil only
// the exit with that deposit count is returned (an address may have more than one pending deposit);
// when nil every matching exit is returned.
func (c *Claimer) BuildClaimParams(
	ctx context.Context, destAddr common.Address, depositCount *uint32,
) ([]ClaimAssetParams, error) {
	leaf, err := c.settlementLeaf()
	if err != nil {
		return nil, err
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

		leafHash := certLeaf.Hash()
		dc, ok := c.localTree.DepositCount(leafHash)
		if !ok {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) not found in local exit tree",
				i, leafHash.Hex())
		}

		if depositCount != nil && dc != *depositCount {
			continue
		}

		// The claim needs the raw metadata (the bridge contract hashes it itself); the certificate
		// only carries its hash, so take the on-chain bytes recorded in the local exit tree.
		rawMetadata, ok := c.localTree.Metadata(leafHash)
		if !ok {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) has no metadata in local exit tree",
				i, leafHash.Hex())
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
			Metadata:               metadataHex(rawMetadata),
			LeafType:               certLeaf.LeafType,
			DepositCount:           dc,
			L1InfoTreeIndex:        leaf.L1InfoTreeIndex,
		})
	}

	return claims, nil
}

// settlementLeaf returns the L1 info tree leaf the certificate settled at — the leaf carrying the
// Global Exit Root recorded by the WAIT step (keccak256(mainnetExitRoot, rollupExitRoot)). Claim
// proofs must be anchored to this leaf rather than the latest one (see BuildClaimParams).
func (c *Claimer) settlementLeaf() (*l1infotreesync.L1InfoTreeLeaf, error) {
	if c.waitResult == nil {
		return nil, errors.New("wait result is nil; cannot locate the settlement L1 info tree leaf")
	}
	ger, err := SettlementGER(c.waitResult)
	if err != nil {
		return nil, err
	}
	leaf, err := c.l1.GetInfoByGlobalExitRoot(ger)
	if err != nil {
		return nil, fmt.Errorf("reading settlement L1 info tree leaf for GER %s: %w", ger.Hex(), err)
	}
	return leaf, nil
}

// Check verifies the claimer's data sources are consistent with the certificate's recorded L1
// settlement. It looks up, in the L1 info tree, the local exit root settled for this network in the
// rollup exit tree captured by the WAIT step (waitResult.UpdateL1InfoTree.RollupExitRoot) and
// confirms it equals the certificate's NewLocalExitRoot. It returns ErrLocalExitRootNotSettled when
// they differ — the certificate has not been settled on L1 (or the recorded settlement is stale).
func (c *Claimer) Check(ctx context.Context) error {
	if c.waitResult == nil || c.waitResult.UpdateL1InfoTree == nil {
		return errors.New("wait result has no updateL1InfoTree event; cannot verify L1 settlement")
	}
	rollupExitRoot := c.waitResult.UpdateL1InfoTree.RollupExitRoot

	settledLER, err := c.l1.GetLocalExitRoot(ctx, c.networkID, rollupExitRoot)
	if err != nil {
		return fmt.Errorf("reading local exit root for network %d at settlement rollup exit root %s: %w",
			c.networkID, rollupExitRoot.Hex(), err)
	}
	if settledLER != c.cert.NewLocalExitRoot {
		return fmt.Errorf("%w: network %d settlement rollup exit root %s has local exit root %s, certificate has %s",
			ErrLocalExitRootNotSettled, c.networkID, rollupExitRoot.Hex(),
			settledLER.Hex(), c.cert.NewLocalExitRoot.Hex())
	}

	c.logger.Infof("✅ settlement check OK: network %d new local exit root %s settled in rollup exit root %s",
		c.networkID, c.cert.NewLocalExitRoot.Hex(), rollupExitRoot.Hex())
	return nil
}
