package claimer

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/bridgesync"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	exitcertificate "github.com/agglayer/aggkit/tools/exit_certificate"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

// pendingFixture is a real lite exit tree with two pre-certificate leaves for dest (deposits 0 and
// 1, identical in content on purpose) and one certificate exit (deposit 2), plus a claimer over it.
type pendingFixture struct {
	claimer *Claimer
	dest    common.Address
	pending []exitcertificate.PendingBridge
}

func newPendingFixture(t *testing.T) *pendingFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "step-g-l2bridgesyncerlite.sqlite")

	token := common.HexToAddress("0xa1")
	dest := common.HexToAddress("0xd1")
	leaf := func(depositCount uint32, amount int64, md []byte) bridgesyncerlite.BridgeLeaf {
		return bridgesyncerlite.BridgeLeaf{
			BlockNum: 10, BlockPos: uint64(depositCount), OriginAddress: token, DestinationAddress: dest,
			Amount: big.NewInt(amount), Metadata: md, DepositCount: depositCount,
		}
	}
	leaves := []bridgesyncerlite.BridgeLeaf{leaf(0, 100, []byte("md")), leaf(1, 100, []byte("md")), leaf(2, 300, nil)}

	syncer, err := bridgesyncerlite.New(ctx, bridgesyncerlite.Config{DBPath: dbPath}, nil)
	require.NoError(t, err)
	require.NoError(t, syncer.StoreBridges(ctx, leaves))
	root, err := syncer.BuildTree(ctx)
	require.NoError(t, err)
	require.NoError(t, syncer.Close())

	// The certificate only carries the last leaf, with its metadata already hashed.
	raw, err := json.Marshal(&agglayertypes.Certificate{
		NetworkID: 7, NewLocalExitRoot: root,
		BridgeExits: []*agglayertypes.BridgeExit{{
			LeafType:           bridgetypes.LeafTypeAsset,
			TokenInfo:          &agglayertypes.TokenInfo{OriginTokenAddress: token},
			DestinationAddress: dest,
			Amount:             big.NewInt(300),
			Metadata:           crypto.Keccak256(nil),
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
		leaf: &l1infotreesync.L1InfoTreeLeaf{
			L1InfoTreeIndex: 9, MainnetExitRoot: common.HexToHash("0x1111"), RollupExitRoot: common.HexToHash("0x2222"),
		},
		localRoot: root,
	}
	waitResult := &exitcertificate.StepWaitResult{
		UpdateL1InfoTree: &exitcertificate.L1InfoTreeUpdate{
			MainnetExitRoot: common.HexToHash("0x1111"), RollupExitRoot: common.HexToHash("0x2222"),
		},
	}

	pending := make([]exitcertificate.PendingBridge, 0, 2)
	for _, l := range leaves[:2] {
		pending = append(pending, exitcertificate.PendingBridge{
			DepositCount: l.DepositCount, BlockNumber: l.BlockNum, OriginAddress: l.OriginAddress,
			DestinationAddress: l.DestinationAddress, Amount: l.Amount, Metadata: l.Metadata,
		})
	}
	return &pendingFixture{
		claimer: NewClaimer(log.GetDefaultLogger(), cert, lt, l1, 0, waitResult).WithPendingBridges(pending),
		dest:    dest,
		pending: pending,
	}
}

func TestListBridgesIncludesPending(t *testing.T) {
	t.Parallel()
	f := newPendingFixture(t)

	bridges, err := f.claimer.ListBridges(f.dest)
	require.NoError(t, err)
	require.Len(t, bridges, 3)
	// Certificate exit first, then the pending ones with the deposit count from the file.
	require.Equal(t, uint32(2), bridges[0].DepositCount)
	require.Equal(t, uint32(0), bridges[1].DepositCount)
	require.Equal(t, uint32(1), bridges[2].DepositCount)
	require.Equal(t, "100", bridges[1].Amount)
	require.Equal(t, "0x"+common.Bytes2Hex([]byte("md")), bridges[1].Metadata)

	other, err := f.claimer.ListBridges(common.HexToAddress("0xee"))
	require.NoError(t, err)
	require.Empty(t, other)
}

func TestListBridgesSkipsPendingAlreadyInCertificate(t *testing.T) {
	t.Parallel()
	f := newPendingFixture(t)
	// A pending entry whose deposit count the certificate already serves is not listed twice.
	dup := f.pending[0]
	dup.DepositCount = 2
	f.claimer.WithPendingBridges([]exitcertificate.PendingBridge{dup})

	bridges, err := f.claimer.ListBridges(f.dest)
	require.NoError(t, err)
	require.Len(t, bridges, 1)
}

func TestBuildClaimParamsIncludesPending(t *testing.T) {
	t.Parallel()
	f := newPendingFixture(t)

	claims, err := f.claimer.BuildClaimParams(context.Background(), f.dest, nil)
	require.NoError(t, err)
	require.Len(t, claims, 3)
	require.Equal(t, uint32(2), claims[0].DepositCount)

	// Identical pending exits keep their own deposit count and global index.
	for i, dc := range []uint32{0, 1} {
		got := claims[i+1]
		require.Equal(t, dc, got.DepositCount)
		require.Equal(t, bridgesync.GenerateGlobalIndexForNetworkID(7, dc).String(), got.GlobalIndex)
		require.Equal(t, "100", got.Amount)
		require.Equal(t, "0x"+common.Bytes2Hex([]byte("md")), got.Metadata)
		require.Equal(t, uint32(9), got.L1InfoTreeIndex)
		require.Equal(t, common.HexToHash("0x1111").Hex(), got.MainnetExitRoot)
		require.Len(t, got.SmtProofLocalExitRoot, 32)
	}

	// Selecting one pending deposit returns only that one.
	dc := uint32(1)
	claims, err = f.claimer.BuildClaimParams(context.Background(), f.dest, &dc)
	require.NoError(t, err)
	require.Len(t, claims, 1)
	require.Equal(t, uint32(1), claims[0].DepositCount)
}

func TestBuildClaimParamsPendingMismatch(t *testing.T) {
	t.Parallel()
	f := newPendingFixture(t)
	// A pending entry whose content is not the tree's leaf must not produce a claim.
	bad := f.pending[0]
	bad.Amount = big.NewInt(999)
	f.claimer.WithPendingBridges([]exitcertificate.PendingBridge{bad})

	_, err := f.claimer.BuildClaimParams(context.Background(), f.dest, nil)
	require.ErrorContains(t, err, "does not match the local exit tree")
}

func TestLoadPendingBridges(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), exitcertificate.FileStepPendingBridges)
	want := []exitcertificate.PendingBridge{{
		DepositCount: 4, DestinationAddress: common.HexToAddress("0xd1"), Amount: big.NewInt(5), Metadata: []byte{1},
	}}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, raw, 0o600))

	got, err := LoadPendingBridges(path)
	require.NoError(t, err)
	require.Equal(t, want, got)

	_, err = LoadPendingBridges(filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
}
