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
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/agglayer/aggkit/tree"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// A Message exit may carry ETH (a bridgeMessage with msg.value, e.g. a version-1 recovery output): the
// claimer serves it like any other message, with its amount as the claim's amount argument, for a
// certificate exit (deposit 1) and for a pending pre-certificate message (deposit 0) alike.
func TestMessageExitWithValue(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "step-g-l2bridgesyncerlite.sqlite")

	sender := common.HexToAddress("0x5e")
	dest := common.HexToAddress("0xd1")
	payload := []byte("message payload")
	message := func(depositCount uint32, amount int64) bridgesyncerlite.BridgeLeaf {
		return bridgesyncerlite.BridgeLeaf{
			BlockNum: 10, BlockPos: uint64(depositCount), LeafType: leafTypeMessage, OriginNetwork: 7,
			OriginAddress: sender, DestinationAddress: dest, Amount: big.NewInt(amount), Metadata: payload,
			DepositCount: depositCount,
		}
	}
	leaves := []bridgesyncerlite.BridgeLeaf{message(0, 7), message(1, 5)}

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.StoreBridges(ctx, leaves))
	root, err := syncer.BuildTree(ctx)
	require.NoError(t, err)
	require.NoError(t, syncer.Close())

	raw, err := json.Marshal(&agglayertypes.Certificate{
		NetworkID: 7, NewLocalExitRoot: root,
		BridgeExits: []*agglayertypes.BridgeExit{{
			LeafType:           bridgetypes.LeafTypeMessage,
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 7, OriginTokenAddress: sender},
			DestinationAddress: dest,
			Amount:             big.NewInt(5),
			Metadata:           crypto.Keccak256(payload),
		}},
	})
	require.NoError(t, err)
	certPath := filepath.Join(dir, "exit-certificate-signed.json")
	require.NoError(t, os.WriteFile(certPath, raw, 0o600))
	cert, err := LoadCertificate(certPath)
	require.NoError(t, err)

	lt, err := OpenLocalExitTree(ctx, dbPath, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, lt.Close()) })

	l1 := &fakeL1{
		leaf:      &l1infotreesync.L1InfoTreeLeaf{L1InfoTreeIndex: 9},
		localRoot: root,
	}
	waitResult := &exitcertificate.StepWaitResult{UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{}}
	pending := []exitcertificate.PendingBridge{{
		DepositCount: 0, BlockNumber: 10, LeafType: leafTypeMessage, OriginNetwork: 7, OriginAddress: sender,
		DestinationAddress: dest, Amount: big.NewInt(7), Metadata: payload,
	}}
	c := NewClaimer(log.GetDefaultLogger(), cert, lt, l1, 0, waitResult).WithPendingBridges(pending)

	bridges, err := c.ListBridgesV2(dest)
	require.NoError(t, err)
	require.Len(t, bridges, 2)
	require.Equal(t, uint32(1), bridges[0].DepositCount)
	require.Equal(t, "5", bridges[0].Amount)
	require.Equal(t, uint32(0), bridges[1].DepositCount)
	require.Equal(t, "7", bridges[1].Amount)

	claims, err := c.BuildClaimParamsV2(ctx, dest, nil)
	require.NoError(t, err)
	require.Len(t, claims, 2)
	for i, want := range []struct {
		depositCount uint32
		amount       string
	}{{1, "5"}, {0, "7"}} {
		got := claims[i]
		require.Equal(t, want.depositCount, got.DepositCount)
		require.Equal(t, ClaimMethodMessage, got.ClaimMethod)
		require.Equal(t, want.amount, got.Amount)
		require.Equal(t, metadataHex(payload), got.Metadata)
		// The proof is of the leaf that carries the amount: claimMessage with these arguments verifies.
		leafHash := (&bridgesyncerlite.BridgeLeaf{
			LeafType: leafTypeMessage, OriginNetwork: 7, OriginAddress: sender, DestinationAddress: dest,
			Amount: leaves[want.depositCount].Amount, Metadata: payload,
		}).Hash()
		var proof [32]common.Hash
		for j, h := range got.SmtProofLocalExitRoot {
			proof[j] = common.HexToHash(h)
		}
		require.NoError(t, tree.VerifyProof(leafHash, proof, want.depositCount, root))
	}
}
