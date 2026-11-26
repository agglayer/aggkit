package claimer

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/bridgesync"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func depositCountsV2(bridges []BridgeExitViewV2) []uint32 {
	out := make([]uint32, 0, len(bridges))
	for _, b := range bridges {
		out = append(out, b.DepositCount)
	}
	return out
}

func TestCircleFixtureCheck(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	require.NoError(t, f.claimer.Check(context.Background()))
}

// A holder finds its Message exits by its own address, which is only inside the metadata.
func TestListBridgesV2ByBeneficiary(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)

	bridges, err := f.claimer.ListBridgesV2(f.holder)
	require.NoError(t, err)
	// Certificate exits first (in certificate order), then the pending ones.
	require.Equal(t,
		[]uint32{circleDCTransfer, circleDCMessage, circleDCPendingMessage, circleDCPendingTransfer},
		depositCountsV2(bridges))

	transfer := bridges[0]
	require.Equal(t, ClaimMethodAsset, transfer.ClaimMethod)
	require.Equal(t, leafTypeAsset, transfer.LeafType)
	require.Equal(t, f.holder.Hex(), transfer.DestinationAddress)
	require.Equal(t, "2000000000000000000", transfer.Amount)
	require.Empty(t, transfer.Beneficiary)
	require.Empty(t, transfer.DecodedAmount)
	require.Nil(t, transfer.DisplayToken)

	message := bridges[1]
	require.Equal(t, ClaimMethodMessage, message.ClaimMethod)
	require.Equal(t, leafTypeMessage, message.LeafType)
	require.Equal(t, circleNetworkID, message.OriginNetwork)
	require.Equal(t, circleZkMinterBurner.Hex(), message.OriginTokenAddress)
	require.Equal(t, circleL1Escrow.Hex(), message.DestinationAddress)
	require.Equal(t, "0", message.Amount)
	require.Equal(t, f.holder.Hex(), message.Beneficiary)
	require.Equal(t, "1234560000", message.DecodedAmount)
	require.Equal(t, &DisplayToken{Address: circleL1USDC.Hex(), Symbol: "USDC", Decimals: 6}, message.DisplayToken)

	pendingMessage := bridges[2]
	require.Equal(t, ClaimMethodMessage, pendingMessage.ClaimMethod)
	require.Equal(t, f.holder.Hex(), pendingMessage.Beneficiary)
	require.Equal(t, "250500000", pendingMessage.DecodedAmount)
	require.NotNil(t, pendingMessage.DisplayToken)

	require.Equal(t, ClaimMethodAsset, bridges[3].ClaimMethod)

	// The destination contract sees every message addressed to it, decoded.
	escrow, err := f.claimer.ListBridgesV2(circleL1Escrow)
	require.NoError(t, err)
	require.Equal(t, []uint32{circleDCMessage, circleDCDuplicateMessage, circleDCPendingMessage}, depositCountsV2(escrow))
	require.Equal(t, f.holder2.Hex(), escrow[1].Beneficiary)

	nobody, err := f.claimer.ListBridgesV2(common.HexToAddress("0xee"))
	require.NoError(t, err)
	require.Empty(t, nobody)
}

// A message whose route is not configured is matched by its destination address only and is not
// decoded, even when its metadata looks like (address, uint256).
func TestListBridgesV2UnknownRoute(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)

	bridges, err := f.claimer.ListBridgesV2(f.unknownDest)
	require.NoError(t, err)
	require.Equal(t, []uint32{circleDCUnknownRoute}, depositCountsV2(bridges))
	require.Equal(t, ClaimMethodMessage, bridges[0].ClaimMethod)
	require.Empty(t, bridges[0].Beneficiary)
	require.Empty(t, bridges[0].DecodedAmount)
	require.Nil(t, bridges[0].DisplayToken)

	// The address inside its metadata (holder) does not get it: see TestListBridgesV2ByBeneficiary.

	// Without routes nothing is decoded: the holder only sees what is addressed to it.
	_, err = f.claimer.WithMessageRoutes(nil)
	require.NoError(t, err)
	bridges, err = f.claimer.ListBridgesV2(f.holder)
	require.NoError(t, err)
	require.Equal(t, []uint32{circleDCTransfer, circleDCPendingTransfer}, depositCountsV2(bridges))
}

// A pending message whose metadata does not decode stays reachable by destination address.
func TestListBridgesV2UndecodableMetadata(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	bad := f.claimer.pending[0]
	bad.Metadata = bad.Metadata[:40]
	f.claimer.WithPendingBridges([]exitcertificate.PendingBridge{bad})

	bridges, err := f.claimer.ListBridgesV2(f.holder)
	require.NoError(t, err)
	require.Equal(t, []uint32{circleDCTransfer, circleDCMessage}, depositCountsV2(bridges))

	bridges, err = f.claimer.ListBridgesV2(circleL1Escrow)
	require.NoError(t, err)
	require.Equal(t, []uint32{circleDCMessage, circleDCDuplicateMessage, circleDCPendingMessage}, depositCountsV2(bridges))
	require.Empty(t, bridges[2].Beneficiary)
}

// The certificate exit that has the same leaf hash as an older leaf gets its own deposit count (its
// position), not the older leaf's, and its proof verifies at that position.
func TestDuplicateLeafHashMatchedByPosition(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	ctx := context.Background()

	bridges, err := f.claimer.ListBridgesV2(f.holder2)
	require.NoError(t, err)
	// The historical leaf is not in the certificate nor pending: only the certificate exit is served.
	require.Equal(t, []uint32{circleDCDuplicateMessage}, depositCountsV2(bridges))
	require.Equal(t, "3000000000", bridges[0].DecodedAmount)

	claims, err := f.claimer.BuildClaimParamsV2(ctx, f.holder2, nil)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	claim := claims[0]
	require.Equal(t, circleDCDuplicateMessage, claim.DepositCount)
	require.Equal(t,
		bridgesync.GenerateGlobalIndexForNetworkID(circleNetworkID, circleDCDuplicateMessage).String(), claim.GlobalIndex)

	leafHash := f.leaves[circleDCDuplicateMessage].Hash()
	proof, err := f.claimer.localTree.Proof(ctx, circleDCDuplicateMessage, f.root)
	require.NoError(t, err)
	require.Equal(t, proofToHex(proof), claim.SmtProofLocalExitRoot)
	require.NoError(t, tree.VerifyProof(leafHash, proof, circleDCDuplicateMessage, f.root))
	// The same leaf hash does not verify at the older leaf's position with this proof.
	require.Error(t, tree.VerifyProof(leafHash, proof, circleDCHistoricalMessage, f.root))

	// Asking for the older leaf's deposit count returns nothing: it is not an exit of the certificate.
	historical := circleDCHistoricalMessage
	claims, err = f.claimer.BuildClaimParamsV2(ctx, f.holder2, &historical)
	require.NoError(t, err)
	require.Empty(t, claims)
}

func TestBuildClaimParamsV2(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	ctx := context.Background()

	claims, err := f.claimer.BuildClaimParamsV2(ctx, f.holder, nil)
	require.NoError(t, err)
	require.Len(t, claims, 4)

	wantMethods := map[uint32]string{
		circleDCTransfer: ClaimMethodAsset, circleDCMessage: ClaimMethodMessage,
		circleDCPendingMessage: ClaimMethodMessage, circleDCPendingTransfer: ClaimMethodAsset,
	}
	for _, claim := range claims {
		dc := claim.DepositCount
		leaf := f.leaves[dc]
		require.Equal(t, wantMethods[dc], claim.ClaimMethod, "deposit %d", dc)
		require.Equal(t, leaf.LeafType, claim.LeafType)
		// The claim carries the leaf's own fields: for a message the destination is the contract, the
		// amount is 0 and the metadata is the raw (address, uint256) payload.
		require.Equal(t, leaf.OriginNetwork, claim.OriginNetwork)
		require.Equal(t, leaf.OriginAddress.Hex(), claim.OriginTokenAddress)
		require.Equal(t, leaf.DestinationAddress.Hex(), claim.DestinationAddress)
		require.Equal(t, leaf.Amount.String(), claim.Amount)
		require.Equal(t, metadataHex(leaf.Metadata), claim.Metadata)
		require.Equal(t, bridgesync.GenerateGlobalIndexForNetworkID(circleNetworkID, dc).String(), claim.GlobalIndex)
		require.Equal(t, f.l1.leaf.MainnetExitRoot.Hex(), claim.MainnetExitRoot)
		require.Equal(t, f.l1.leaf.RollupExitRoot.Hex(), claim.RollupExitRoot)
		require.Equal(t, proofToHex(f.l1.rollupProof), claim.SmtProofRollupExitRoot)

		// The local proof is the one of the leaf at that deposit count against the certificate's root.
		proof, err := f.claimer.localTree.Proof(ctx, dc, f.root)
		require.NoError(t, err)
		require.Equal(t, proofToHex(proof), claim.SmtProofLocalExitRoot)
		require.NoError(t, tree.VerifyProof(leaf.Hash(), proof, dc, f.root))
	}

	// One exit by deposit count, looked up by beneficiary.
	dc := circleDCPendingMessage
	claims, err = f.claimer.BuildClaimParamsV2(ctx, f.holder, &dc)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, circleDCPendingMessage, claims[0].DepositCount)
	require.Equal(t, ClaimMethodMessage, claims[0].ClaimMethod)

	// A deposit count that belongs to someone else is not returned.
	dc = circleDCDuplicateMessage
	claims, err = f.claimer.BuildClaimParamsV2(ctx, f.holder, &dc)
	require.NoError(t, err)
	require.Empty(t, claims)
}

func TestBuildClaimParamsV2NotSettled(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	f.l1.localRoot = common.HexToHash("0xdeadbeef")
	_, err := f.claimer.BuildClaimParamsV2(context.Background(), f.holder, nil)
	require.ErrorIs(t, err, ErrLocalExitRootNotSettled)
}

// A pending message whose content is not the tree's leaf must not produce a claim.
func TestBuildClaimParamsV2PendingMessageMismatch(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	bad := f.claimer.pending[0]
	bad.Metadata = encodeAddressUint256(f.holder, usdc(999_999_000_000))
	f.claimer.WithPendingBridges([]exitcertificate.PendingBridge{bad})

	_, err := f.claimer.BuildClaimParamsV2(context.Background(), f.holder, nil)
	require.ErrorContains(t, err, "does not match the local exit tree")
}

func TestWithMessageRoutesInvalid(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	bad := circleRoute()
	bad.MetadataLayout = "bytes"
	_, err := f.claimer.WithMessageRoutes([]MessageRoute{bad})
	require.ErrorContains(t, err, "unsupported metadataLayout")
}

// In API v2 metadata is always the raw bridge metadata and metadata_hash its keccak256, for
// certificate exits and pending bridges alike, in /bridges and in /claim-params. API v1 keeps showing
// the certificate's metadata hash as the metadata of a certificate exit in /bridges.
func TestV2MetadataIsRawWithHash(t *testing.T) {
	t.Parallel()
	f := newCircleFixture(t)
	ctx := context.Background()

	bridges, err := f.claimer.ListBridgesV2(f.holder)
	require.NoError(t, err)
	claims, err := f.claimer.BuildClaimParamsV2(ctx, f.holder, nil)
	require.NoError(t, err)
	require.Len(t, bridges, 4)
	require.Len(t, claims, 4)

	for i := range bridges {
		leaf := f.leaves[bridges[i].DepositCount]
		wantRaw := metadataHex(leaf.Metadata)
		wantHash := crypto.Keccak256Hash(leaf.Metadata).Hex()

		require.Equal(t, wantRaw, bridges[i].Metadata, "bridge %d", bridges[i].DepositCount)
		require.Equal(t, wantHash, bridges[i].MetadataHash, "bridge %d", bridges[i].DepositCount)

		require.Equal(t, bridges[i].DepositCount, claims[i].DepositCount)
		require.Equal(t, wantRaw, claims[i].Metadata, "claim %d", claims[i].DepositCount)
		require.Equal(t, wantHash, claims[i].MetadataHash, "claim %d", claims[i].DepositCount)
	}
	// Empty metadata: "0x" and the hash of the empty string.
	require.Equal(t, "0x", bridges[0].Metadata)
	require.Equal(t, "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470", bridges[0].MetadataHash)

	// A certificate exit with non-empty metadata shows the raw bytes and their hash.
	other := common.HexToAddress("0xB0B0000000000000000000000000000000000002")
	v2, err := f.claimer.ListBridgesV2(other)
	require.NoError(t, err)
	rawERC20 := f.leaves[circleDCOtherTransfer].Metadata
	require.NotEmpty(t, rawERC20)
	require.Equal(t, metadataHex(rawERC20), v2[0].Metadata)
	require.Equal(t, crypto.Keccak256Hash(rawERC20).Hex(), v2[0].MetadataHash)
}
