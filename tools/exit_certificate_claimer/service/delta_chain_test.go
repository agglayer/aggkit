package claimer

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// TestClaimerServesOldAndNewExitsAfterDelta mirrors what the exit_certificate delta mode leaves on
// disk: a lite tree extended with the delta's leaves (AppendBridges) and a full certificate listing
// every exit. The claimer, unchanged, must resolve the same deposit count for the old exits as before
// the delta (so their global index does not change) and prove old and new exits alike against the
// latest local exit root.
func TestClaimerServesOldAndNewExitsAfterDelta(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "step-g-l2bridgesyncerlite.sqlite")

	token := common.HexToAddress("0xa1")
	dest := common.HexToAddress("0xd1")
	leaf := func(depositCount uint32, block uint64, amount int64, md []byte) bridgesyncerlite.BridgeLeaf {
		return bridgesyncerlite.BridgeLeaf{
			BlockNum: block, BlockPos: 0, LeafType: 0, OriginNetwork: 0, OriginAddress: token,
			DestinationNetwork: 0, DestinationAddress: dest, Amount: big.NewInt(amount), Metadata: md,
			DepositCount: depositCount,
		}
	}
	// 2 exits from the first certificate, then a delta of 2 more.
	oldLeaves := []bridgesyncerlite.BridgeLeaf{leaf(0, 10, 100, []byte("md0")), leaf(1, 10, 200, nil)}
	newLeaves := []bridgesyncerlite.BridgeLeaf{leaf(2, 11, 300, []byte("md2")), leaf(3, 11, 400, nil)}

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.StoreBridges(ctx, append([]bridgesyncerlite.BridgeLeaf(nil), oldLeaves...)))
	rootBeforeDelta, err := syncer.BuildTree(ctx)
	require.NoError(t, err)
	finalRoot, err := syncer.AppendBridges(ctx, newLeaves)
	require.NoError(t, err)
	require.NotEqual(t, rootBeforeDelta, finalRoot)
	require.NoError(t, syncer.Close())

	// The full certificate: every exit in leaf order, metadata hashed, latest root.
	all := append(append([]bridgesyncerlite.BridgeLeaf(nil), oldLeaves...), newLeaves...)
	exits := make([]*agglayertypes.BridgeExit, len(all))
	for i, l := range all {
		exits[i] = &agglayertypes.BridgeExit{
			LeafType:           bridgetypes.LeafTypeAsset,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: token},
			DestinationAddress: dest,
			Amount:             l.Amount,
			Metadata:           crypto.Keccak256(l.Metadata),
		}
	}
	certPath := filepath.Join(dir, "exit-certificate-full.json")
	raw, err := json.Marshal(&agglayertypes.Certificate{
		NetworkID: 7, Height: 1, NewLocalExitRoot: finalRoot, BridgeExits: exits,
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(certPath, raw, 0o600))

	cert, err := LoadCertificate(certPath)
	require.NoError(t, err)
	require.Equal(t, finalRoot, cert.NewLocalExitRoot)
	require.Len(t, cert.Leaves, 4)

	lt, err := OpenLocalExitTree(ctx, dbPath, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, lt.Close()) }()

	for i, l := range cert.Leaves {
		depositCount, ok := lt.DepositCount(l.Hash())
		require.True(t, ok, "exit %d not found in the tree", i)
		// Old exits keep the deposit count they had before the delta (same global index).
		require.Equal(t, uint32(i), depositCount)

		// Old and new exits are proven against the latest root.
		proof, err := lt.Proof(ctx, depositCount, finalRoot)
		require.NoError(t, err)
		require.NoError(t, tree.VerifyProof(l.Hash(), proof, depositCount, finalRoot), "exit %d", i)

		// The claim needs the raw metadata the leaf was built from.
		md, ok := lt.Metadata(l.Hash())
		require.True(t, ok)
		require.Equal(t, all[i].Metadata, md)
	}
}

func TestCertificateFileName(t *testing.T) {
	t.Parallel()
	regular := &exitcertificate.Config{}
	require.Equal(t, "exit-certificate-signed.json", certificateFileName(regular))

	delta := &exitcertificate.Config{Options: exitcertificate.Options{DeltaMode: true}}
	require.Equal(t, "exit-certificate-full.json", certificateFileName(delta))
}
