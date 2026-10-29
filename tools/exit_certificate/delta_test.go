package exit_certificate

import (
	"context"
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"github.com/agglayer/aggkit/agglayer/mocks"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/tools/exit_certificate/bridgesyncerlite"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const deltaTestNetworkID = uint32(7)

var (
	deltaDest   = common.HexToAddress("0x00000000000000000000000000000000000000d1")
	deltaToken  = common.HexToAddress("0x00000000000000000000000000000000000000a1")
	deltaRawMD1 = []byte("raw-metadata-1")
)

func deltaExit(amount int64, token common.Address, md []byte) *agglayertypes.BridgeExit {
	return &agglayertypes.BridgeExit{
		LeafType:           bridgetypes.LeafTypeAsset,
		TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 0, OriginTokenAddress: token},
		DestinationNetwork: 0,
		DestinationAddress: deltaDest,
		Amount:             big.NewInt(amount),
		Metadata:           md,
	}
}

// writeLiteTree builds a lite DB at path holding the given leaves (deposit counts 0..n-1) with the
// tree built, and returns the tree root.
func writeLiteTree(t *testing.T, path string, leaves []bridgesyncerlite.BridgeLeaf) common.Hash {
	t.Helper()
	syncer, err := bridgesyncerlite.New(context.Background(), bridgesyncerlite.Config{DBPath: path}, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, syncer.Close()) }()
	require.NoError(t, syncer.StoreBridges(context.Background(), leaves))
	root, err := syncer.BuildTree(context.Background())
	require.NoError(t, err)
	return root
}

func baseLeaves(n int) []bridgesyncerlite.BridgeLeaf {
	leaves := make([]bridgesyncerlite.BridgeLeaf, n)
	for i := range leaves {
		leaves[i] = bridgesyncerlite.BridgeLeaf{
			BlockNum: uint64(i + 1), BlockPos: 0, DepositCount: uint32(i),
			DestinationAddress: deltaDest, Amount: big.NewInt(int64(100 + i)),
		}
	}
	return leaves
}

// writePreviousRun creates a previous-run dir with a lite tree of nBase leaves, a settled wait result
// and a certificate file (named certFile) carrying the tree root and the given exits.
func writePreviousRun(
	t *testing.T, certFile string, nBase int, exits []*agglayertypes.BridgeExit,
) (string, common.Hash) {
	t.Helper()
	dir := t.TempDir()
	root := writeLiteTree(t, filepath.Join(dir, fileStepGLiteDB), baseLeaves(nBase))
	cert := &agglayertypes.Certificate{
		NetworkID: deltaTestNetworkID, Height: 3, NewLocalExitRoot: root, BridgeExits: exits,
	}
	require.NoError(t, saveJSON(dir, certFile, cert))
	require.NoError(t, saveJSON(dir, fileStepWaitResult, &StepWaitResult{
		CertificateHash: common.HexToHash("0x99"), FinalStatus: agglayertypes.Settled,
	}))
	return dir, root
}

func TestLoadDeltaExits(t *testing.T) {
	t.Parallel()
	write := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "delta.json")
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		return p
	}
	tokenInfo := `{"origin_network":0,"origin_token_address":"0x00000000000000000000000000000000000000a1"}`
	exit := func(extra string) string {
		return `{"bridge_exits":[{"leaf_type":"Transfer","token_info":` + tokenInfo +
			`,"dest_network":0,"dest_address":"0x00000000000000000000000000000000000000d1",` + extra + `}]}`
	}

	t.Run("ok with and without metadata", func(t *testing.T) {
		t.Parallel()
		exits, err := LoadDeltaExits(write(t, exit(`"amount":"5","metadata":"abcd"`)), deltaTestNetworkID)
		require.NoError(t, err)
		require.Len(t, exits, 1)
		require.Equal(t, big.NewInt(5), exits[0].Amount)
		require.Equal(t, []byte{0xab, 0xcd}, exits[0].Metadata)

		exits, err = LoadDeltaExits(write(t, exit(`"amount":"5","metadata":null`)), deltaTestNetworkID)
		require.NoError(t, err)
		require.Empty(t, exits[0].Metadata)
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		_, err := LoadDeltaExits(filepath.Join(t.TempDir(), "missing.json"), deltaTestNetworkID)
		require.ErrorContains(t, err, "read delta exits file")

		_, err = LoadDeltaExits(write(t, `{not json`), deltaTestNetworkID)
		require.ErrorContains(t, err, "parse delta exits file")

		_, err = LoadDeltaExits(write(t, `{"bridge_exits":[]}`), deltaTestNetworkID)
		require.ErrorContains(t, err, "no bridge_exits")

		_, err = LoadDeltaExits(write(t, exit(`"amount":"0"`)), deltaTestNetworkID)
		require.ErrorContains(t, err, "amount must be positive")

		_, err = LoadDeltaExits(write(t, `{"bridge_exits":[{"leaf_type":"Transfer","token_info":null,`+
			`"dest_network":0,"dest_address":"0x00000000000000000000000000000000000000d1","amount":"1"}]}`),
			deltaTestNetworkID)
		require.ErrorContains(t, err, "token info is invalid")

		_, err = LoadDeltaExits(write(t, exit(`"amount":"5"`)), 0)
		require.ErrorContains(t, err, "is the exiting network itself")

		msg := `{"bridge_exits":[{"leaf_type":"Message","token_info":` + tokenInfo +
			`,"dest_network":0,"dest_address":"0x00000000000000000000000000000000000000d1","amount":"1"}]}`
		_, err = LoadDeltaExits(write(t, msg), deltaTestNetworkID)
		require.ErrorContains(t, err, "only Transfer exits")
	})
}

func TestLoadPreviousRun(t *testing.T) {
	t.Parallel()

	t.Run("full certificate has priority over the signed one", func(t *testing.T) {
		t.Parallel()
		dir, root := writePreviousRun(t, fileFullCertificate, 2,
			[]*agglayertypes.BridgeExit{deltaExit(1, deltaToken, nil)})
		require.NoError(t, saveJSON(dir, fileSignedCertificate, &agglayertypes.Certificate{Height: 99}))

		prev, err := loadPreviousRun(dir)
		require.NoError(t, err)
		require.Equal(t, fileFullCertificate, prev.CertificateFile)
		require.Equal(t, root, prev.Certificate.NewLocalExitRoot)
		require.Equal(t, uint64(3), prev.Certificate.Height)
		require.Len(t, prev.Certificate.BridgeExits, 1)
	})

	t.Run("falls back to the signed certificate of a regular run", func(t *testing.T) {
		t.Parallel()
		dir, _ := writePreviousRun(t, fileSignedCertificate, 2, nil)
		prev, err := loadPreviousRun(dir)
		require.NoError(t, err)
		require.Equal(t, fileSignedCertificate, prev.CertificateFile)
	})

	t.Run("errors", func(t *testing.T) {
		t.Parallel()
		_, err := loadPreviousRun(t.TempDir())
		require.ErrorContains(t, err, "load previous run certificate")

		dir, _ := writePreviousRun(t, fileSignedCertificate, 1, nil)
		require.NoError(t, os.Remove(filepath.Join(dir, fileStepWaitResult)))
		_, err = loadPreviousRun(dir)
		require.ErrorContains(t, err, "submitted and settled")

		dir, _ = writePreviousRun(t, fileSignedCertificate, 1, nil)
		require.NoError(t, saveJSON(dir, fileStepWaitResult, &StepWaitResult{FinalStatus: agglayertypes.Pending}))
		_, err = loadPreviousRun(dir)
		require.ErrorContains(t, err, "is not settled")

		dir, _ = writePreviousRun(t, fileSignedCertificate, 1, nil)
		require.NoError(t, os.Remove(filepath.Join(dir, fileStepGLiteDB)))
		_, err = loadPreviousRun(dir)
		require.ErrorContains(t, err, "local exit tree DB")

		dir = t.TempDir()
		require.NoError(t, saveJSON(dir, fileSignedCertificate, &agglayertypes.Certificate{}))
		_, err = loadPreviousRun(dir)
		require.ErrorContains(t, err, "no new_local_exit_root")
	})
}

// AET-11 against the previous certificate's LER.
func TestVerifyChainsOnPreviousRun(t *testing.T) {
	t.Parallel()
	prevLER := common.HexToHash("0xabc")
	prev := &previousRun{
		Certificate: &agglayertypes.Certificate{Height: 3, NewLocalExitRoot: prevLER},
		WaitResult:  &StepWaitResult{CertificateHash: common.HexToHash("0x99")},
	}

	require.NoError(t, verifyChainsOnPreviousRun(prev, prevLER, 4))

	err := verifyChainsOnPreviousRun(prev, common.HexToHash("0xdef"), 4)
	require.ErrorIs(t, err, errUnsettledBridgeExits)
	require.ErrorContains(t, err, "not the previous certificate's new_local_exit_root")

	err = verifyChainsOnPreviousRun(prev, prevLER, 6)
	require.ErrorIs(t, err, errUnsettledBridgeExits)
	require.ErrorContains(t, err, "next certificate height is 6")
}

func TestRunStepHDelta(t *testing.T) {
	t.Parallel()
	prevLER := common.HexToHash("0xabc")
	prev := &previousRun{
		Certificate: &agglayertypes.Certificate{Height: 3, NewLocalExitRoot: prevLER},
		WaitResult:  &StepWaitResult{},
	}
	cfg := &Config{L2NetworkID: deltaTestNetworkID}

	t.Run("chains on the previous certificate", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			SettledLER: &prevLER, SettledHeight: ptrUint64(3),
		}, nil)
		res, err := runStepHDelta(context.Background(), cfg, client, prev)
		require.NoError(t, err)
		require.Equal(t, prevLER, res.PreviousLocalExitRoot)
		require.Equal(t, uint64(4), res.Height)
	})

	t.Run("pending certificate is rejected", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			LatestPendingStatus: ptrStatus(agglayertypes.Pending), LatestPendingHeight: ptrUint64(4),
		}, nil)
		_, err := runStepHDelta(context.Background(), cfg, client, prev)
		require.ErrorContains(t, err, "has a pending certificate")
	})

	t.Run("settled LER differs", func(t *testing.T) {
		t.Parallel()
		other := common.HexToHash("0xdef")
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			SettledLER: &other, SettledHeight: ptrUint64(3),
		}, nil)
		_, err := runStepHDelta(context.Background(), cfg, client, prev)
		require.ErrorIs(t, err, errUnsettledBridgeExits)
	})

	t.Run("ignoreLERMismatch downgrades to a warning", func(t *testing.T) {
		t.Parallel()
		other := common.HexToHash("0xdef")
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			SettledLER: &other, SettledHeight: ptrUint64(3),
		}, nil)
		ignoring := &Config{L2NetworkID: deltaTestNetworkID, Options: Options{IgnoreLERMismatch: true}}
		res, err := runStepHDelta(context.Background(), ignoring, client, prev)
		require.NoError(t, err)
		require.Equal(t, prevLER, res.PreviousLocalExitRoot)
	})
}

func TestCheckDeltaUnsettledExitsWith(t *testing.T) {
	t.Parallel()
	prevLER := common.HexToHash("0xabc")
	prev := &previousRun{
		Certificate: &agglayertypes.Certificate{Height: 3, NewLocalExitRoot: prevLER},
		WaitResult:  &StepWaitResult{},
	}
	cfg := &Config{L2NetworkID: deltaTestNetworkID}

	t.Run("ok", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			SettledLER: &prevLER, SettledHeight: ptrUint64(3),
		}, nil)
		var res StepCheckResult
		var failures []string
		checkDeltaUnsettledExitsWith(context.Background(), cfg, client, prev, &res, &failures)
		require.Empty(t, failures)
		require.Equal(t, okStatus, res.UnsettledExitsStatus)
		require.Equal(t, prevLER.Hex(), res.SettledLER)
	})

	t.Run("previous certificate is not the latest settled", func(t *testing.T) {
		t.Parallel()
		other := common.HexToHash("0xdef")
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			SettledLER: &other, SettledHeight: ptrUint64(4),
		}, nil)
		var res StepCheckResult
		var failures []string
		checkDeltaUnsettledExitsWith(context.Background(), cfg, client, prev, &res, &failures)
		require.Len(t, failures, 1)
		require.NotEqual(t, okStatus, res.UnsettledExitsStatus)
	})

	t.Run("pending certificate", func(t *testing.T) {
		t.Parallel()
		client := mocks.NewAgglayerClientMock(t)
		client.EXPECT().GetNetworkInfo(mock.Anything, deltaTestNetworkID).Return(agglayertypes.NetworkInfo{
			LatestPendingStatus: ptrStatus(agglayertypes.Candidate),
		}, nil)
		var res StepCheckResult
		var failures []string
		checkDeltaUnsettledExitsWith(context.Background(), cfg, client, prev, &res, &failures)
		require.Len(t, failures, 1)
		require.Equal(t, errorStatus, res.UnsettledExitsStatus)
	})
}

// Per-token check: the sum of the new exits must stay within the agglayer remaining balance.
func TestCompareDeltaBalancesAndCap(t *testing.T) {
	t.Parallel()
	other := common.HexToAddress("0x00000000000000000000000000000000000000a2")
	exits := []*agglayertypes.BridgeExit{
		deltaExit(30, deltaToken, nil), deltaExit(50, deltaToken, nil), deltaExit(10, other, nil),
	}
	groups := groupBridgeExitsByToken(&agglayertypes.Certificate{BridgeExits: exits})

	t.Run("within the remaining balance", func(t *testing.T) {
		t.Parallel()
		checks, err := compareDeltaBalances(groups, []agglayerTokenEntry{
			{OriginTokenAddress: deltaToken, Amount: "80"}, // exactly the sum
			{OriginTokenAddress: other, Amount: "1000"},
			{OriginTokenAddress: common.HexToAddress("0xff"), Amount: "5"}, // untouched token, ignored
		})
		require.NoError(t, err)
		require.Len(t, checks, 2)
		for _, c := range checks {
			require.True(t, c.Match, c)
		}
	})

	t.Run("exceeds and token unknown to the agglayer", func(t *testing.T) {
		t.Parallel()
		checks, err := compareDeltaBalances(groups,
			[]agglayerTokenEntry{{OriginTokenAddress: deltaToken, Amount: "79"}})
		require.NoError(t, err)
		byToken := map[string]TokenBalanceCheck{}
		for _, c := range checks {
			byToken[c.OriginTokenAddress] = c
		}
		require.False(t, byToken[deltaToken.Hex()].Match)
		require.Equal(t, "80", byToken[deltaToken.Hex()].CertificateAmount)
		require.Len(t, byToken[deltaToken.Hex()].CertificateEntries, 2)
		// The agglayer holds nothing of `other` → balance 0 → the 10 exit does not fit.
		require.False(t, byToken[other.Hex()].Match)
		require.Equal(t, "0", byToken[other.Hex()].AgglayerAmount)

		// capMode none forbids trimming; "appearance" trims to the remaining balance.
		_, err = capCertificateExits(exits, checks, CapModeNone)
		require.ErrorIs(t, err, errCapForbidden)
		capped, err := capCertificateExits(exits, checks, CapModeByAppearance)
		require.NoError(t, err)
		require.Len(t, capped, 2) // `other` exit dropped, second exit capped 50 → 49
		require.Equal(t, big.NewInt(30), capped[0].Amount)
		require.Equal(t, big.NewInt(49), capped[1].Amount)
	})

	t.Run("unparseable agglayer amount", func(t *testing.T) {
		t.Parallel()
		_, err := compareDeltaBalances(groups, []agglayerTokenEntry{{OriginTokenAddress: deltaToken, Amount: "x"}})
		require.ErrorContains(t, err, "parse agglayer amount")
	})
}

func TestRunStepFDeltaSkippedWithoutAdminCheck(t *testing.T) {
	t.Parallel()
	res, err := RunStepFDelta(context.Background(), &Config{}, &agglayertypes.Certificate{})
	require.NoError(t, err)
	require.True(t, res.AllMatch)

	_, err = RunStepFDelta(context.Background(),
		&Config{Options: Options{UseAgglayerAdminToStepFCheck: true}}, &agglayertypes.Certificate{})
	require.ErrorContains(t, err, "agglayerAdminURL")
}

// Tree continuation: appending the delta to the previous tree must give the same root as building
// the whole tree (old + new leaves) from scratch, keep the deposit counts of the old leaves and chain
// repeatedly (a delta output is a valid previous run).
func TestRunStepG2DeltaContinuesTree(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l2RPC := gasTokenStubURL(t)

	prevExit := deltaExit(7, deltaToken, nil)
	prevDir, prevRoot := writePreviousRun(t, fileSignedCertificate, 3, []*agglayertypes.BridgeExit{prevExit})
	prev, err := loadPreviousRun(prevDir)
	require.NoError(t, err)

	newExits := func() []*agglayertypes.BridgeExit {
		return []*agglayertypes.BridgeExit{
			deltaExit(11, deltaToken, deltaRawMD1), deltaExit(12, deltaToken, deltaRawMD1),
		}
	}

	// First delta.
	cfg1 := &Config{L2RPCURL: l2RPC, Options: Options{OutputDir: t.TempDir()}}
	cert1 := &agglayertypes.Certificate{BridgeExits: newExits()}
	res1, err := RunStepG2Delta(ctx, cfg1, prev, cert1)
	require.NoError(t, err)
	require.Equal(t, prevRoot, res1.InitialLocalExitRoot)
	require.Equal(t, uint32(3), res1.FirstDepositCount)
	require.Equal(t, uint64(2), res1.BridgeExitCount)
	require.NotEqual(t, prevRoot, res1.NewLocalExitRoot)
	// Metadata is stored hashed in the certificate, raw in the step result (Step I hashes it again).
	require.Equal(t, crypto.Keccak256(deltaRawMD1), cert1.BridgeExits[0].Metadata)
	require.Equal(t, deltaRawMD1, res1.BridgeExitMetadata[0])

	// Reference: the whole tree built from scratch with the same leaves.
	gasNet, gasAddr := uint32(0), common.Address{}
	all := append(baseLeaves(3), liteLeavesFromExits(newExits(), 100, 3, gasNet, gasAddr,
		[][]byte{deltaRawMD1, deltaRawMD1})...)
	want := writeLiteTree(t, filepath.Join(t.TempDir(), "ref.sqlite"), all)
	require.Equal(t, want, res1.NewLocalExitRoot)

	// The previous run's DB is untouched.
	prevAgain, err := loadPreviousRun(prevDir)
	require.NoError(t, err)
	require.Equal(t, prevRoot, prevAgain.Certificate.NewLocalExitRoot)

	// Second delta, chained on the output of the first one.
	full, manifest := buildFullCertificate(cfg1, prev, withSigLessCopy(cert1, res1), res1)
	dir1 := cfg1.Options.OutputDir
	require.NoError(t, saveJSON(dir1, fileFullCertificate, full))
	require.NoError(t, saveJSON(dir1, fileStepWaitResult, &StepWaitResult{FinalStatus: agglayertypes.Settled}))
	prev2, err := loadPreviousRun(dir1)
	require.NoError(t, err)
	require.Equal(t, fileFullCertificate, prev2.CertificateFile)
	require.Len(t, prev2.Certificate.BridgeExits, 3) // previous exit + 2 new ones
	require.Equal(t, uint32(3), manifest.FirstDepositCount)
	require.Equal(t, uint32(4), manifest.LastDepositCount)

	cfg2 := &Config{L2RPCURL: l2RPC, Options: Options{OutputDir: t.TempDir()}}
	cert2 := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{deltaExit(13, deltaToken, deltaRawMD1)},
	}
	res2, err := RunStepG2Delta(ctx, cfg2, prev2, cert2)
	require.NoError(t, err)
	require.Equal(t, res1.NewLocalExitRoot, res2.InitialLocalExitRoot)
	require.Equal(t, uint32(5), res2.FirstDepositCount)
	// The synthetic block must sort after the previous leaves: the tree's last root is block ordered.
	all = append(all, liteLeavesFromExits(cert2.BridgeExits, 101, 5, gasNet, gasAddr, [][]byte{deltaRawMD1})...)
	want = writeLiteTree(t, filepath.Join(t.TempDir(), "ref2.sqlite"), all)
	require.Equal(t, want, res2.NewLocalExitRoot)
}

// withSigLessCopy returns the delta certificate as Step I would leave it (roots and height set).
func withSigLessCopy(cert *agglayertypes.Certificate, g *StepGResult) *agglayertypes.Certificate {
	c := *cert
	c.NetworkID = deltaTestNetworkID
	c.Height = 4
	c.PrevLocalExitRoot = g.InitialLocalExitRoot
	c.NewLocalExitRoot = g.NewLocalExitRoot
	return &c
}

func TestRunStepG2DeltaErrors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	l2RPC := gasTokenStubURL(t)

	t.Run("no exits", func(t *testing.T) {
		t.Parallel()
		_, err := RunStepG2Delta(ctx, &Config{L2RPCURL: l2RPC}, &previousRun{}, &agglayertypes.Certificate{})
		require.ErrorContains(t, err, "no bridge exits")
	})

	t.Run("previous tree root differs from the previous certificate", func(t *testing.T) {
		t.Parallel()
		dir, _ := writePreviousRun(t, fileSignedCertificate, 2, nil)
		prev, err := loadPreviousRun(dir)
		require.NoError(t, err)
		prev.Certificate.NewLocalExitRoot = common.HexToHash("0xbad")

		cfg := &Config{L2RPCURL: l2RPC, Options: Options{OutputDir: t.TempDir()}}
		cert := &agglayertypes.Certificate{
			BridgeExits: []*agglayertypes.BridgeExit{deltaExit(1, deltaToken, deltaRawMD1)},
		}
		_, err = RunStepG2Delta(ctx, cfg, prev, cert)
		require.ErrorContains(t, err, "previous run is inconsistent")
	})
}

// Two exits with the same leaf hash would be served with the same deposit count by the claimer.
func TestRunStepG2DeltaRejectsDuplicateExits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cfg := func() *Config { return &Config{L2RPCURL: gasTokenStubURL(t), Options: Options{OutputDir: t.TempDir()}} }

	// duplicates a previous exit (metadata hash of the previous exit = keccak of the delta raw metadata)
	prevExit := deltaExit(7, deltaToken, crypto.Keccak256(deltaRawMD1))
	dir, _ := writePreviousRun(t, fileSignedCertificate, 2, []*agglayertypes.BridgeExit{prevExit})
	prev, err := loadPreviousRun(dir)
	require.NoError(t, err)
	_, err = RunStepG2Delta(ctx, cfg(), prev, &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{deltaExit(7, deltaToken, deltaRawMD1)},
	})
	require.ErrorContains(t, err, "duplicates previous exit 0")

	// duplicates another delta exit
	_, err = RunStepG2Delta(ctx, cfg(), prev, &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			deltaExit(8, deltaToken, deltaRawMD1), deltaExit(8, deltaToken, deltaRawMD1),
		},
	})
	require.ErrorContains(t, err, "duplicates delta exit 0")

	// a different amount is fine
	_, err = RunStepG2Delta(ctx, cfg(), prev, &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{deltaExit(9, deltaToken, deltaRawMD1)},
	})
	require.NoError(t, err)
}

func TestBuildFullCertificate(t *testing.T) {
	t.Parallel()
	prevExit := deltaExit(1, deltaToken, nil)
	newExit := deltaExit(2, deltaToken, nil)
	prev := &previousRun{
		Certificate: &agglayertypes.Certificate{Height: 3, NewLocalExitRoot: common.HexToHash("0x1"),
			BridgeExits: []*agglayertypes.BridgeExit{prevExit}},
		WaitResult: &StepWaitResult{CertificateHash: common.HexToHash("0x99")},
	}
	delta := &agglayertypes.Certificate{
		NetworkID: deltaTestNetworkID, Height: 4, PrevLocalExitRoot: common.HexToHash("0x1"),
		NewLocalExitRoot: common.HexToHash("0x2"), L1InfoTreeLeafCount: 8,
		BridgeExits: []*agglayertypes.BridgeExit{newExit},
	}
	cfg := &Config{Options: Options{PreviousRun: "prev"}}

	full, manifest := buildFullCertificate(cfg, prev, delta, &StepGResult{FirstDepositCount: 10})
	// Old exits first, then the new ones: the order of the tree leaves.
	require.Equal(t, []*agglayertypes.BridgeExit{prevExit, newExit}, full.BridgeExits)
	require.Equal(t, delta.NewLocalExitRoot, full.NewLocalExitRoot)
	require.Equal(t, uint64(4), full.Height)
	require.Equal(t, uint32(8), full.L1InfoTreeLeafCount)
	// The delta certificate itself is untouched.
	require.Len(t, delta.BridgeExits, 1)

	require.Equal(t, DeltaManifest{
		NetworkID: deltaTestNetworkID, PreviousRun: "prev",
		PreviousCertificateHash: common.HexToHash("0x99"), PreviousCertificateHeight: 3,
		DeltaCertificateID: delta.CertificateID(), DeltaCertificateHeight: 4,
		RootBefore: common.HexToHash("0x1"), RootAfter: common.HexToHash("0x2"),
		FirstDepositCount: 10, LastDepositCount: 10, DeltaExitCount: 1, TotalExitCount: 2,
	}, *manifest)

	// The full certificate round-trips through the JSON shape the claimer and the next delta read.
	raw, err := json.Marshal(full)
	require.NoError(t, err)
	var cj certificateJSON
	require.NoError(t, json.Unmarshal(raw, &cj))
	back, err := cj.toAgglayerCertificate()
	require.NoError(t, err)
	require.Len(t, back.BridgeExits, 2)
}

func TestRunDeltaStepsRejectsL2ScanSteps(t *testing.T) {
	t.Parallel()
	for _, step := range []string{"a", "f", "g", "h", "check"} {
		err := runDeltaSteps(context.Background(), &Config{}, step)
		require.ErrorContains(t, err, "not available with options.deltaMode", step)
	}
	require.Error(t, runDeltaSteps(context.Background(), &Config{}, "nonsense"))
}

func TestRunDeltaRejectsSameOutputAndPreviousDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	err := runDelta(context.Background(), &Config{Options: Options{OutputDir: dir, PreviousRun: dir}})
	require.ErrorContains(t, err, "must be different directories")
}

func TestDeltaConfigValidation(t *testing.T) {
	t.Parallel()
	base := `"l2RpcUrl":"http://x","l2BridgeAddress":"0x2a3DD3EB832aF982ec71669E178424b10Dca2EDe",` +
		`"targetBlock":"100"`
	load := func(t *testing.T, opts string) (*Config, error) {
		t.Helper()
		p := filepath.Join(t.TempDir(), "cfg.json")
		content := `{` + base + `,"options":{"useAgglayerAdminToStepFCheck":false` + opts + `}}`
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
		return LoadConfig(p)
	}

	// Delta mode: exitAddress is not required, relative paths resolve from the config dir.
	cfg, err := load(t, `,"deltaMode":true,"previousRun":"prev","deltaExitsFile":"exits.json"`)
	require.NoError(t, err)
	require.True(t, cfg.Options.DeltaMode)
	require.True(t, filepath.IsAbs(cfg.Options.PreviousRun) || filepath.Base(cfg.Options.PreviousRun) == "prev")
	require.Equal(t, "exits.json", filepath.Base(cfg.Options.DeltaExitsFile))

	_, err = load(t, `,"deltaMode":true,"deltaExitsFile":"exits.json"`)
	require.ErrorContains(t, err, "options.previousRun")
	_, err = load(t, `,"deltaMode":true,"previousRun":"prev"`)
	require.ErrorContains(t, err, "options.deltaExitsFile")

	// Without deltaMode the delta options would be silently ignored → rejected; exitAddress stays required.
	_, err = load(t, `,"previousRun":"prev"`)
	require.ErrorContains(t, err, "only valid with options.deltaMode=true")
	_, err = load(t, ``)
	require.ErrorContains(t, err, "exitAddress")
}
