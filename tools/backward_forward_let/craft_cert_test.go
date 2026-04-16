package backward_forward_let

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"flag"
	"math/big"
	"testing"
	"time"

	"github.com/agglayer/aggkit/agglayer/types"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	bridgeservicetypes "github.com/agglayer/aggkit/bridgeservice/types"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

type stubAgglayerClient struct {
	info types.NetworkInfo
	err  error
}

func (s *stubAgglayerClient) SendCertificate(context.Context, *agglayertypes.Certificate) (common.Hash, error) {
	return common.Hash{}, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetCertificateHeader(context.Context, common.Hash) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetNetworkInfo(context.Context, uint32) (types.NetworkInfo, error) {
	return s.info, s.err
}

func (s *stubAgglayerClient) GetEpochConfiguration(context.Context) (*agglayertypes.ClockConfiguration, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetLatestSettledCertificateHeader(context.Context, uint32) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetLatestPendingCertificateHeader(context.Context, uint32) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetCertificateHeaderByID(context.Context, common.Hash) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetCertificateHeaderByHash(context.Context, common.Hash) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetCertificateHeaderByCertificateID(context.Context, common.Hash) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetCertificateHeaderPerHeight(context.Context, uint32, uint64) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func (s *stubAgglayerClient) GetCertificateHeaderLegacy(context.Context, common.Hash) (*agglayertypes.CertificateHeader, error) {
	return nil, errors.New("not implemented")
}

func TestMakeFakeBridgeExit(t *testing.T) {
	t.Parallel()

	opts := &craftCertOptions{
		nonce:           []byte("nonce"),
		originNetwork:   7,
		originTokenAddr: common.HexToAddress("0x1111111111111111111111111111111111111111"),
		destNetwork:     9,
		amount:          big.NewInt(123),
	}

	exit0 := makeFakeBridgeExit(opts, 0)
	exit1 := makeFakeBridgeExit(opts, 1)

	require.Equal(t, bridgetypes.LeafTypeAsset, exit0.LeafType)
	require.Equal(t, uint32(7), exit0.TokenInfo.OriginNetwork)
	require.Equal(t, common.HexToAddress("0x1111111111111111111111111111111111111111"), exit0.TokenInfo.OriginTokenAddress)
	require.Equal(t, uint32(9), exit0.DestinationNetwork)
	require.Equal(t, big.NewInt(123), exit0.Amount)
	require.NotEqual(t, exit0.DestinationAddress, exit1.DestinationAddress)
}

func TestCraftMaliciousCertificate_NoSettledCerts(t *testing.T) {
	t.Parallel()

	signerKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	bridge := &stubL2Bridge{
		depositCount: big.NewInt(1),
		root:         [32]byte(common.HexToHash("0x1111111111111111111111111111111111111111111111111111111111111111")),
	}
	bridgeSvc := &stubBridgeService{
		bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
			0: {
				LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
				OriginNetwork:      0,
				OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
				DestinationNetwork: 1,
				DestinationAddress: bridgeservicetypes.Address("0x2222222222222222222222222222222222222222"),
				Amount:             bridgeservicetypes.BigIntString("5"),
			},
		},
	}
	env := &Env{
		L2Bridge:       bridge,
		BridgeService:  bridgeSvc,
		AgglayerClient: &stubAgglayerClient{},
		L2NetworkID:    1,
	}

	opts := &craftCertOptions{
		numFakeExits:      1,
		startingExitIndex: 0,
		nonce:             []byte("run-a"),
		originNetwork:     0,
		originTokenAddr:   common.Address{},
		destNetwork:       0,
		amount:            big.NewInt(0),
	}

	cert, err := craftMaliciousCertificate(context.Background(), env, nil, &stubHashSigner{key: signerKey}, opts)
	require.NoError(t, err)
	require.Equal(t, uint64(0), cert.Height)
	require.Equal(t, common.Hash(bridge.root), cert.PrevLocalExitRoot)
	require.Len(t, cert.BridgeExits, 1)
	require.Equal(t, uint32(1), cert.L1InfoTreeLeafCount)

	expectedLER, err := ComputeLERForNewLeaves(
		[]common.Hash{BridgeResponseLeafHash(bridgeSvc.bridges[0])},
		[]common.Hash{BridgeExitLeafHash(cert.BridgeExits[0])},
	)
	require.NoError(t, err)
	require.Equal(t, expectedLER, cert.NewLocalExitRoot)

	multisig, ok := cert.AggchainData.(*agglayertypes.AggchainDataMultisig)
	require.True(t, ok)
	require.Len(t, multisig.Multisig.Signatures, 1)
}

func TestCraftMaliciousCertificate_SettledCertsFromAggsenderRPC(t *testing.T) {
	t.Parallel()

	signerKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	settledHeight := uint64(1)
	settledLER := common.HexToHash("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	settledLeafCount := uint64(2)
	info := types.NetworkInfo{
		SettledHeight:       &settledHeight,
		SettledLER:          &settledLER,
		SettledLETLeafCount: &settledLeafCount,
	}

	exit0 := makeFakeBridgeExit(&craftCertOptions{
		nonce:           []byte("existing-0"),
		originNetwork:   0,
		originTokenAddr: common.Address{},
		destNetwork:     0,
		amount:          big.NewInt(0),
	}, 0)
	exit1 := makeFakeBridgeExit(&craftCertOptions{
		nonce:           []byte("existing-1"),
		originNetwork:   0,
		originTokenAddr: common.Address{},
		destNetwork:     0,
		amount:          big.NewInt(0),
	}, 0)

	rpc := &stubAggsenderRPC{
		exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
			0: {exit0},
			1: {exit1},
		},
		failHeights: map[uint64]bool{},
	}
	rpcHeader := &aggsendertypes.Certificate{
		Header: &aggsendertypes.CertificateHeader{L1InfoTreeLeafCount: 7},
	}
	rpcWithHeader := &stubCraftAggsenderRPC{stubAggsenderRPC: rpc, certByHeight: map[uint64]*aggsendertypes.Certificate{1: rpcHeader}}

	env := &Env{
		AgglayerClient: &stubAgglayerClient{info: info},
		AggsenderRPC:   rpcWithHeader,
		L2NetworkID:    1,
	}

	opts := &craftCertOptions{
		numFakeExits:      1,
		startingExitIndex: 5,
		nonce:             []byte("new"),
		originNetwork:     0,
		originTokenAddr:   common.Address{},
		destNetwork:       0,
		amount:            big.NewInt(0),
	}

	cert, err := craftMaliciousCertificate(context.Background(), env, nil, &stubHashSigner{key: signerKey}, opts)
	require.NoError(t, err)
	require.Equal(t, uint64(2), cert.Height)
	require.Equal(t, settledLER, cert.PrevLocalExitRoot)
	require.Equal(t, uint32(7), cert.L1InfoTreeLeafCount)

	expectedLER, err := ComputeLERForNewLeaves(
		[]common.Hash{BridgeExitLeafHash(exit0), BridgeExitLeafHash(exit1)},
		[]common.Hash{BridgeExitLeafHash(cert.BridgeExits[0])},
	)
	require.NoError(t, err)
	require.Equal(t, expectedLER, cert.NewLocalExitRoot)
}

func TestLoadExistingLeafHashes_ReconstructsMissingPrefixFromBridgeService(t *testing.T) {
	t.Parallel()

	exit2 := makeFakeBridgeExit(&craftCertOptions{
		nonce:           []byte("existing-2"),
		originNetwork:   0,
		originTokenAddr: common.Address{},
		destNetwork:     0,
		amount:          big.NewInt(0),
	}, 0)
	exit3 := makeFakeBridgeExit(&craftCertOptions{
		nonce:           []byte("existing-3"),
		originNetwork:   0,
		originTokenAddr: common.Address{},
		destNetwork:     0,
		amount:          big.NewInt(0),
	}, 0)

	bridge0 := &bridgeservicetypes.BridgeResponse{
		LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      0,
		OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
		DestinationNetwork: 0,
		DestinationAddress: bridgeservicetypes.Address("0x1111111111111111111111111111111111111111"),
		Amount:             bridgeservicetypes.BigIntString("1"),
	}
	bridge1 := &bridgeservicetypes.BridgeResponse{
		LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      0,
		OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
		DestinationNetwork: 0,
		DestinationAddress: bridgeservicetypes.Address("0x2222222222222222222222222222222222222222"),
		Amount:             bridgeservicetypes.BigIntString("2"),
	}

	env := &Env{
		L2NetworkID: 1,
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: bridge0,
				1: bridge1,
			},
		},
		AggsenderRPC: &stubAggsenderRPC{
			exitsByHeight: map[uint64][]*agglayertypes.BridgeExit{
				2: {exit2},
				3: {exit3},
			},
			failHeights: map[uint64]bool{
				0: true,
				1: true,
			},
		},
	}

	hashes, err := loadExistingLeafHashes(context.Background(), env, nil, 4, common.Hash{}, 4)
	require.NoError(t, err)
	require.Equal(t, []common.Hash{
		BridgeResponseLeafHash(bridge0),
		BridgeResponseLeafHash(bridge1),
		BridgeExitLeafHash(exit2),
		BridgeExitLeafHash(exit3),
	}, hashes)
}

func TestLoadExistingLeafHashes_AllHistoricalHeightsMissingFallsBackToBridgeService(t *testing.T) {
	t.Parallel()

	bridge0 := &bridgeservicetypes.BridgeResponse{
		LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      0,
		OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
		DestinationNetwork: 0,
		DestinationAddress: bridgeservicetypes.Address("0x3333333333333333333333333333333333333333"),
		Amount:             bridgeservicetypes.BigIntString("3"),
	}
	bridge1 := &bridgeservicetypes.BridgeResponse{
		LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      0,
		OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
		DestinationNetwork: 0,
		DestinationAddress: bridgeservicetypes.Address("0x4444444444444444444444444444444444444444"),
		Amount:             bridgeservicetypes.BigIntString("4"),
	}

	env := &Env{
		L2NetworkID: 1,
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: bridge0,
				1: bridge1,
			},
		},
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{
				0: true,
				1: true,
			},
		},
	}

	hashes, err := loadExistingLeafHashes(context.Background(), env, nil, 2, common.Hash{}, 2)
	require.NoError(t, err)
	require.Equal(t, []common.Hash{
		BridgeResponseLeafHash(bridge0),
		BridgeResponseLeafHash(bridge1),
	}, hashes)
}

func TestLoadExistingLeafHashes_UsesBridgeServiceWhenCurrentBridgeMatchesSettled(t *testing.T) {
	t.Parallel()

	bridge0 := &bridgeservicetypes.BridgeResponse{
		LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      0,
		OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
		DestinationNetwork: 0,
		DestinationAddress: bridgeservicetypes.Address("0x5555555555555555555555555555555555555555"),
		Amount:             bridgeservicetypes.BigIntString("5"),
	}
	bridge1 := &bridgeservicetypes.BridgeResponse{
		LeafType:           bridgetypes.LeafTypeAsset.Uint8(),
		OriginNetwork:      0,
		OriginAddress:      bridgeservicetypes.Address("0x0000000000000000000000000000000000000000"),
		DestinationNetwork: 0,
		DestinationAddress: bridgeservicetypes.Address("0x6666666666666666666666666666666666666666"),
		Amount:             bridgeservicetypes.BigIntString("6"),
	}

	settledLER, err := ComputeLERForNewLeaves(
		[]common.Hash{BridgeResponseLeafHash(bridge0)},
		[]common.Hash{BridgeResponseLeafHash(bridge1)},
	)
	require.NoError(t, err)

	env := &Env{
		L2NetworkID: 1,
		L2Bridge: &stubL2Bridge{
			depositCount: big.NewInt(2),
			root:         [32]byte(settledLER),
		},
		BridgeService: &stubBridgeService{
			bridges: map[uint32]*bridgeservicetypes.BridgeResponse{
				0: bridge0,
				1: bridge1,
			},
		},
		AggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{
				0: true,
				1: true,
			},
		},
	}

	hashes, err := loadExistingLeafHashes(context.Background(), env, nil, 2, settledLER, 2)
	require.NoError(t, err)
	require.Equal(t, []common.Hash{
		BridgeResponseLeafHash(bridge0),
		BridgeResponseLeafHash(bridge1),
	}, hashes)
}

func TestGetStoredBridgeExitsForHeight_FromDB(t *testing.T) {
	t.Parallel()

	payload := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			makeFakeBridgeExit(&craftCertOptions{
				nonce:           []byte("db"),
				originNetwork:   0,
				originTokenAddr: common.Address{},
				destNetwork:     0,
				amount:          big.NewInt(0),
			}, 0),
		},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	store := &stubCraftCertStore{
		certs: map[uint64]*aggsendertypes.Certificate{
			0: {SignedCertificate: ptrString(string(raw)), Header: &aggsendertypes.CertificateHeader{}},
		},
	}

	exits, err := getStoredBridgeExitsForHeight(&Env{}, store, 0)
	require.NoError(t, err)
	require.Len(t, exits, 1)
}

func TestGetStoredBridgeExitsForHeight_FromOverride(t *testing.T) {
	t.Parallel()

	overrideExit := makeFakeBridgeExit(&craftCertOptions{
		nonce:           []byte("override"),
		originNetwork:   0,
		originTokenAddr: common.Address{},
		destNetwork:     0,
		amount:          big.NewInt(0),
	}, 0)

	exits, err := getStoredBridgeExitsForHeight(&Env{
		BridgeExitsOverride: &BridgeExitsOverride{
			parsed: map[uint64][]*agglayertypes.BridgeExit{
				7: {overrideExit},
			},
		},
	}, nil, 7)
	require.NoError(t, err)
	require.Equal(t, []*agglayertypes.BridgeExit{overrideExit}, exits)
}

func TestGetStoredBridgeExitsForHeight_FromAggsenderHeaderFallback(t *testing.T) {
	t.Parallel()

	payload := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			makeFakeBridgeExit(&craftCertOptions{
				nonce:           []byte("rpc-header"),
				originNetwork:   0,
				originTokenAddr: common.Address{},
				destNetwork:     0,
				amount:          big.NewInt(0),
			}, 0),
		},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	rpc := &stubCraftAggsenderRPC{
		stubAggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{0: true},
		},
		certByHeight: map[uint64]*aggsendertypes.Certificate{
			0: {SignedCertificate: ptrString(string(raw))},
		},
	}

	exits, err := getStoredBridgeExitsForHeight(&Env{AggsenderRPC: rpc}, nil, 0)
	require.NoError(t, err)
	require.Len(t, exits, 1)
}

func TestGetStoredBridgeExitsForHeight_Retries429OnHeaderPath(t *testing.T) {
	t.Parallel()

	payload := &agglayertypes.Certificate{
		BridgeExits: []*agglayertypes.BridgeExit{
			makeFakeBridgeExit(&craftCertOptions{
				nonce:           []byte("rpc-retry"),
				originNetwork:   0,
				originTokenAddr: common.Address{},
				destNetwork:     0,
				amount:          big.NewInt(0),
			}, 0),
		},
	}
	raw, err := json.Marshal(payload)
	require.NoError(t, err)

	rpc := &stubCraftAggsenderRPC{
		stubAggsenderRPC: &stubAggsenderRPC{
			failHeights: map[uint64]bool{0: true},
		},
		certByHeight: map[uint64]*aggsendertypes.Certificate{
			0: {SignedCertificate: ptrString(string(raw))},
		},
		headerErrsRemaining: map[uint64]int{0: 2},
	}

	exits, err := getStoredBridgeExitsForHeight(&Env{AggsenderRPC: rpc}, nil, 0)
	require.NoError(t, err)
	require.Len(t, exits, 1)
}

func TestCallCraftCertRPCWithTimeout_ReturnsResult(t *testing.T) {
	t.Parallel()

	value, err := callCraftCertRPCWithTimeout(func() (int, error) {
		return 7, nil
	})
	require.NoError(t, err)
	require.Equal(t, 7, value)
}

func TestCallCraftCertRPCWithTimeout_TimesOut(t *testing.T) {
	t.Parallel()

	start := time.Now()
	_, err := callCraftCertRPCWithTimeout(func() (int, error) {
		time.Sleep(craftCertRPCRequestTimeout + 200*time.Millisecond)
		return 0, nil
	})
	require.ErrorContains(t, err, "aggsender RPC request timed out")
	require.Less(t, time.Since(start), craftCertRPCRequestTimeout+time.Second)
}

type stubCraftAggsenderRPC struct {
	*stubAggsenderRPC
	certByHeight        map[uint64]*aggsendertypes.Certificate
	headerErrsRemaining map[uint64]int
}

func (s *stubCraftAggsenderRPC) GetCertificateHeaderPerHeight(height *uint64) (*aggsendertypes.Certificate, error) {
	if s.headerErrsRemaining != nil && s.headerErrsRemaining[*height] > 0 {
		s.headerErrsRemaining[*height]--
		return nil, errors.New("invalid status code, expected: 200, found: 429")
	}
	return s.certByHeight[*height], nil
}

type stubCraftCertStore struct {
	certs   map[uint64]*aggsendertypes.Certificate
	headers map[uint64]*aggsendertypes.CertificateHeader
}

func (s *stubCraftCertStore) GetCertificateByHeight(height uint64) (*aggsendertypes.Certificate, error) {
	return s.certs[height], nil
}

func (s *stubCraftCertStore) GetCertificateHeaderByHeight(height uint64) (*aggsendertypes.CertificateHeader, error) {
	return s.headers[height], nil
}

func ptrString(v string) *string { return &v }

type stubHashSigner struct {
	key *ecdsa.PrivateKey
}

func (s *stubHashSigner) SignHash(_ context.Context, hash common.Hash) ([]byte, error) {
	return crypto.Sign(hash.Bytes(), s.key)
}

func TestResolveCraftCertSignerConfig_FromCLI(t *testing.T) {
	t.Parallel()

	app := cli.NewApp()
	set := flagSetForCraftCert(t,
		"--signer-key-path", "/tmp/sequencer.keystore",
		"--signer-key-password", "secret",
	)
	ctx := cli.NewContext(app, set, nil)

	cfg, err := resolveCraftCertSignerConfig(&Config{}, ctx)
	require.NoError(t, err)
	require.Equal(t, signer.NewLocalSignerConfig("/tmp/sequencer.keystore", "secret"), cfg)
}

func TestResolveCraftCertSignerConfig_FromAggsenderConfig(t *testing.T) {
	t.Parallel()

	app := cli.NewApp()
	ctx := cli.NewContext(app, flagSetForCraftCert(t), nil)
	expected := signertypes.SignerConfig{
		Method: signertypes.MethodGCPKMS,
		Config: map[string]any{"KeyName": "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1"},
	}

	cfg, err := resolveCraftCertSignerConfig(&Config{
		AggSender: CraftCertAggsenderConfig{
			AggsenderPrivateKey: expected,
		},
	}, ctx)
	require.NoError(t, err)
	require.Equal(t, expected, cfg)
}

func TestResolveCraftCertSignerConfig_Missing(t *testing.T) {
	t.Parallel()

	app := cli.NewApp()
	ctx := cli.NewContext(app, flagSetForCraftCert(t), nil)

	_, err := resolveCraftCertSignerConfig(&Config{}, ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "AggSender.AggsenderPrivateKey")
}

func flagSetForCraftCert(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()

	set := flag.NewFlagSet("craft-cert", flag.ContinueOnError)
	set.String("signer-key-path", "", "")
	set.String("signer-key-password", "", "")
	require.NoError(t, set.Parse(args))
	return set
}
