package claimer

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/agglayer/aggkit/bridgesync"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/agglayer/aggkit/tree"
	treetypes "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
)

// LocalExitTreeReader is the subset of LocalExitTree the claimer depends on. *LocalExitTree
// satisfies it; tests can supply a fake. Leaves are addressed by deposit count (their position in
// the tree), never by hash: identical bridges share a leaf hash but not a deposit count.
type LocalExitTreeReader interface {
	// LeafCount returns the number of leaves of the tree.
	LeafCount() uint32
	// LeafHash returns the hash of the leaf at depositCount.
	LeafHash(depositCount uint32) (common.Hash, bool)
	// Metadata returns the raw bridge metadata of the leaf at depositCount.
	Metadata(depositCount uint32) ([]byte, bool)
	Proof(ctx context.Context, depositCount uint32, localExitRoot common.Hash) (treetypes.Proof, error)
}

// ErrLocalExitRootNotSettled is returned when the certificate's NewLocalExitRoot cannot be found in
// the rollup exit tree of the selected L1 info tree leaf — i.e. the exit certificate has not been
// settled on L1 yet (or the chosen leaf predates settlement).
var ErrLocalExitRootNotSettled = errors.New("certificate new local exit root not settled in L1 info tree")

// Claimer assembles claimAsset / claimMessage parameters for the bridge exits of a settled exit
// certificate, combining the signed certificate, the L2 local exit tree, and the L1 Info Tree.
type Claimer struct {
	logger     *log.Logger
	networkID  uint32
	cert       *Certificate
	localTree  LocalExitTreeReader
	l1         L1InfoTreeQuerier
	waitResult *exitcertificate.StepWaitResult
	// pending holds the unclaimed L2→L1 bridges emitted before the certificate (see
	// exitcertificate.RunStepPending). They are not in cert.Leaves but are in the local exit tree.
	pending []exitcertificate.PendingBridge
	// routes are the configured message routes: how API v2 decodes the beneficiary of a Message exit.
	routes messageRoutes
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

// WithPendingBridges makes the claimer also serve the given pre-certificate unclaimed bridges.
func (c *Claimer) WithPendingBridges(pending []exitcertificate.PendingBridge) *Claimer {
	c.pending = pending
	return c
}

// WithMessageRoutes sets the message routes API v2 uses to decode the beneficiary of Message exits.
// It fails when a route is not valid (see MessageRoute).
func (c *Claimer) WithMessageRoutes(routes []MessageRoute) (*Claimer, error) {
	indexed, err := indexMessageRoutes(routes)
	if err != nil {
		return nil, err
	}
	c.routes = indexed
	return c, nil
}

// NetworkID returns the source network the claimer serves.
func (c *Claimer) NetworkID() uint32 { return c.networkID }

// SettlementWaitResult returns the exit_certificate WAIT step output recording where on L1 the
// certificate settled (the VerifyBatchesTrustedAggregator event and the accompanying L1 Info Tree
// update). It may be nil if no wait result was loaded.
func (c *Claimer) SettlementWaitResult() *exitcertificate.StepWaitResult { return c.waitResult }

// exitSelection says which exits a request is after.
type exitSelection struct {
	// address is the address the exits are looked up by.
	address common.Address
	// depositCount, when set, keeps only the exit with that deposit count.
	depositCount *uint32
}

// selectedExit is a bridge exit (of the certificate or pending) matched by a request.
type selectedExit struct {
	leafType           uint8
	originNetwork      uint32
	originAddress      common.Address
	destinationNetwork uint32
	destinationAddress common.Address
	amount             *big.Int
	depositCount       uint32
	// rawMetadata is the raw bridge metadata, the one a claim needs.
	rawMetadata []byte
	// pending is non-nil for a pre-certificate pending bridge.
	pending *exitcertificate.PendingBridge
	// decoded is non-nil for a Message exit decoded through a configured message route.
	decoded *decodedMessage
}

// certificateFirstDepositCount returns the deposit count of the certificate's first bridge exit. The
// certificate's exits are the last leaves of the local exit tree, in the certificate's order, so exit
// i is the leaf at (leaves of the tree − exits of the certificate) + i.
func (c *Claimer) certificateFirstDepositCount() (uint32, error) {
	leafCount := uint64(c.localTree.LeafCount())
	exits := uint64(len(c.cert.Leaves))
	if exits > leafCount {
		return 0, fmt.Errorf("the certificate has %d bridge exits but the local exit tree has only %d leaves",
			exits, leafCount)
	}
	return uint32(leafCount - exits), nil
}

// matches reports whether an exit belongs to the selection's address: it is its destination address
// or the beneficiary of the decoded message.
func (sel *exitSelection) matches(destinationAddress common.Address, decoded *decodedMessage) bool {
	if destinationAddress == sel.address {
		return true
	}
	return decoded != nil && decoded.beneficiary == sel.address
}

// selectExits returns the certificate's exits matching the selection, in certificate order, followed
// by the matching pre-certificate pending bridges.
//
// A certificate exit is matched to its leaf by position (see certificateFirstDepositCount), not by
// leaf hash, so identical exits (or an exit identical to an older leaf) each keep their own deposit
// count. The leaf hash is only a cross-check: the exit's hash must be the hash of the leaf at its
// position.
func (c *Claimer) selectExits(sel exitSelection) ([]selectedExit, error) {
	first, err := c.certificateFirstDepositCount()
	if err != nil {
		return nil, err
	}

	selected := make([]selectedExit, 0)
	for i := range c.cert.Leaves {
		leaf := &c.cert.Leaves[i]
		depositCount := first + uint32(i)
		if sel.depositCount != nil && *sel.depositCount != depositCount {
			continue
		}
		var decoded *decodedMessage
		if leaf.LeafType == leafTypeMessage {
			if rawMetadata, ok := c.localTree.Metadata(depositCount); ok {
				decoded, _ = c.routes.decode(leaf.LeafType, leaf.OriginNetwork, leaf.OriginTokenAddress,
					leaf.DestinationNetwork, leaf.DestinationAddress, rawMetadata)
			}
		}
		if !sel.matches(leaf.DestinationAddress, decoded) {
			continue
		}

		leafHash := leaf.Hash()
		treeHash, ok := c.localTree.LeafHash(depositCount)
		if !ok {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) not found in local exit tree at deposit count %d",
				i, leafHash.Hex(), depositCount)
		}
		if treeHash != leafHash {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) does not match the local exit tree leaf at "+
				"deposit count %d (%s)", i, leafHash.Hex(), depositCount, treeHash.Hex())
		}
		// The claim needs the raw metadata (the bridge contract hashes it itself); the certificate
		// only carries its hash, so take the on-chain bytes recorded in the local exit tree.
		rawMetadata, ok := c.localTree.Metadata(depositCount)
		if !ok {
			return nil, fmt.Errorf("bridge exit %d (leaf %s) has no metadata in local exit tree",
				i, leafHash.Hex())
		}
		selected = append(selected, selectedExit{
			leafType:           leaf.LeafType,
			originNetwork:      leaf.OriginNetwork,
			originAddress:      leaf.OriginTokenAddress,
			destinationNetwork: leaf.DestinationNetwork,
			destinationAddress: leaf.DestinationAddress,
			amount:             leaf.Amount,
			depositCount:       depositCount,
			rawMetadata:        rawMetadata,
			decoded:            decoded,
		})
	}

	for i := range c.pending {
		p := &c.pending[i]
		// A pending entry whose deposit count the certificate already serves is not listed twice.
		if p.DepositCount >= first {
			continue
		}
		if sel.depositCount != nil && *sel.depositCount != p.DepositCount {
			continue
		}
		decoded, _ := c.routes.decode(p.LeafType, p.OriginNetwork, p.OriginAddress,
			p.DestinationNetwork, p.DestinationAddress, p.Metadata)
		if !sel.matches(p.DestinationAddress, decoded) {
			continue
		}
		selected = append(selected, selectedExit{
			leafType:           p.LeafType,
			originNetwork:      p.OriginNetwork,
			originAddress:      p.OriginAddress,
			destinationNetwork: p.DestinationNetwork,
			destinationAddress: p.DestinationAddress,
			amount:             p.Amount,
			depositCount:       p.DepositCount,
			rawMetadata:        p.Metadata,
			pending:            p,
			decoded:            decoded,
		})
	}
	return selected, nil
}

// viewV2 renders the exit for /bridges (API v2): metadata is the raw bridge metadata, with its hash
// next to it.
func (e *selectedExit) viewV2() BridgeExitViewV2 {
	v := BridgeExitViewV2{
		BridgeExitView: BridgeExitView{
			LeafType:           e.leafType,
			OriginNetwork:      e.originNetwork,
			OriginTokenAddress: addrHex(e.originAddress),
			DestinationNetwork: e.destinationNetwork,
			DestinationAddress: addrHex(e.destinationAddress),
			Amount:             bigToString(e.amount),
			Metadata:           metadataHex(e.rawMetadata),
			DepositCount:       e.depositCount,
		},
		MetadataHash: metadataHashHex(e.rawMetadata),
		ClaimMethod:  claimMethod(e.leafType),
	}
	if e.decoded != nil {
		token := e.decoded.token
		v.Beneficiary = addrHex(e.decoded.beneficiary)
		v.DecodedAmount = bigToString(e.decoded.amount)
		v.DisplayToken = &token
	}
	return v
}

// ListBridgesV2 returns, for API v2, the exits of address: the Transfer and Message exits destined to
// it, and the Message exits whose beneficiary (decoded through a configured message route) it is.
// Certificate exits come first, then the pre-certificate pending bridges.
func (c *Claimer) ListBridgesV2(address common.Address) ([]BridgeExitViewV2, error) {
	exits, err := c.selectExits(exitSelection{address: address})
	if err != nil {
		return nil, err
	}
	views := make([]BridgeExitViewV2, 0, len(exits))
	for i := range exits {
		views = append(views, exits[i].viewV2())
	}
	return views, nil
}

// BuildClaimParamsV2 returns, for API v2, the claim arguments of the exits of address (same selection
// as ListBridgesV2), each with the AgglayerBridge method to call: claimAsset or claimMessage.
func (c *Claimer) BuildClaimParamsV2(
	ctx context.Context, address common.Address, depositCount *uint32,
) ([]ClaimParamsV2, error) {
	return c.buildClaims(ctx, exitSelection{address: address, depositCount: depositCount})
}

// buildClaims returns the claim arguments of the selected exits. The proofs are anchored to the L1
// info tree leaf the certificate settled at (the leaf carrying the GER recorded by the WAIT step),
// not the latest leaf: the certificate's NewLocalExitRoot is only present in the rollup exit tree of
// that settlement leaf, and a later leaf would carry a newer rollup exit root that no longer contains
// it. When the selection has a deposit count only that exit is returned (an address may have more
// than one pending deposit); otherwise every matching exit is returned.
func (c *Claimer) buildClaims(ctx context.Context, sel exitSelection) ([]ClaimParamsV2, error) {
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

	exits, err := c.selectExits(sel)
	if err != nil {
		return nil, err
	}

	claims := make([]ClaimParamsV2, 0, len(exits))
	for i := range exits {
		e := &exits[i]
		localProof, err := c.localTree.Proof(ctx, e.depositCount, c.cert.NewLocalExitRoot)
		if err != nil {
			return nil, fmt.Errorf("building local exit tree proof for deposit %d: %w", e.depositCount, err)
		}
		if e.pending != nil {
			if err := c.verifyPending(e.pending, localProof); err != nil {
				return nil, err
			}
		}
		claims = append(claims, ClaimParamsV2{
			ClaimAssetParams: c.anchorClaim(ClaimAssetParams{
				OriginNetwork:      e.originNetwork,
				OriginTokenAddress: addrHex(e.originAddress),
				DestinationNetwork: e.destinationNetwork,
				DestinationAddress: addrHex(e.destinationAddress),
				Amount:             bigToString(e.amount),
				Metadata:           metadataHex(e.rawMetadata),
				LeafType:           e.leafType,
				DepositCount:       e.depositCount,
			}, leaf, localProof, rollupProof),
			MetadataHash: metadataHashHex(e.rawMetadata),
			ClaimMethod:  claimMethod(e.leafType),
		})
	}
	return claims, nil
}

// verifyPending checks a pre-certificate pending bridge against the local exit tree: its content
// comes from a file, so the leaf it hashes to must be the one proven at its deposit count against the
// certificate's new local exit root.
func (c *Claimer) verifyPending(p *exitcertificate.PendingBridge, localProof treetypes.Proof) error {
	leafHash := (&bridgesyncerlite.BridgeLeaf{
		LeafType:           p.LeafType,
		OriginNetwork:      p.OriginNetwork,
		OriginAddress:      p.OriginAddress,
		DestinationNetwork: p.DestinationNetwork,
		DestinationAddress: p.DestinationAddress,
		Amount:             p.Amount,
		Metadata:           p.Metadata,
	}).Hash()
	if err := tree.VerifyProof(leafHash, localProof, p.DepositCount, c.cert.NewLocalExitRoot); err != nil {
		return fmt.Errorf("pending deposit %d does not match the local exit tree: %w", p.DepositCount, err)
	}
	return nil
}

// anchorClaim completes a claim with the proofs, the global index and the L1 info tree leaf it is
// anchored to.
func (c *Claimer) anchorClaim(
	claim ClaimAssetParams, leaf *l1infotreesync.L1InfoTreeLeaf, localProof, rollupProof treetypes.Proof,
) ClaimAssetParams {
	claim.SmtProofLocalExitRoot = proofToHex(localProof)
	claim.SmtProofRollupExitRoot = proofToHex(rollupProof)
	claim.GlobalIndex = bridgesync.GenerateGlobalIndexForNetworkID(c.networkID, claim.DepositCount).String()
	claim.MainnetExitRoot = leaf.MainnetExitRoot.Hex()
	claim.RollupExitRoot = leaf.RollupExitRoot.Hex()
	claim.L1InfoTreeIndex = leaf.L1InfoTreeIndex
	return claim
}

// settlementLeaf returns the L1 info tree leaf the certificate settled at — the leaf carrying the
// Global Exit Root recorded by the WAIT step (keccak256(mainnetExitRoot, rollupExitRoot)). Claim
// proofs must be anchored to this leaf rather than the latest one (see buildClaims).
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

	// Every certificate exit must be the leaf at its position in the local exit tree.
	if err := c.checkCertificateLeaves(); err != nil {
		return err
	}

	c.logger.Infof("✅ settlement check OK: network %d new local exit root %s settled in rollup exit root %s",
		c.networkID, c.cert.NewLocalExitRoot.Hex(), rollupExitRoot.Hex())
	return nil
}

// checkCertificateLeaves verifies that the certificate's bridge exits are the last leaves of the local
// exit tree, in order: the hash of exit i must be the hash of the leaf at its position. The claimer
// derives every deposit count (and so every global index) from that position.
func (c *Claimer) checkCertificateLeaves() error {
	first, err := c.certificateFirstDepositCount()
	if err != nil {
		return err
	}
	for i := range c.cert.Leaves {
		depositCount := first + uint32(i)
		leafHash := c.cert.Leaves[i].Hash()
		treeHash, ok := c.localTree.LeafHash(depositCount)
		if !ok || treeHash != leafHash {
			return fmt.Errorf("bridge exit %d (leaf %s) does not match the local exit tree leaf at deposit count %d (%s)",
				i, leafHash.Hex(), depositCount, treeHash.Hex())
		}
	}
	return nil
}
