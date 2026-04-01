package backward_forward_let

import (
	"context"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsenderdb "github.com/agglayer/aggkit/aggsender/db"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc/codes"
)

const defaultL1InfoTreeLeafCount uint32 = 1

type certStoreReader interface {
	GetCertificateByHeight(height uint64) (*aggsendertypes.Certificate, error)
	GetCertificateHeaderByHeight(height uint64) (*aggsendertypes.CertificateHeader, error)
}

type craftCertOptions struct {
	numFakeExits      int
	startingExitIndex int
	nonce             []byte
	originNetwork     uint32
	originTokenAddr   common.Address
	destNetwork       uint32
	amount            *big.Int
}

// RunCraftCert is the CLI action for the craft-cert subcommand.
// It builds a signed malicious certificate JSON for staging drills.
func RunCraftCert(c *cli.Context) error {
	if !c.Bool("staging-only") {
		return fmt.Errorf("craft-cert requires --staging-only acknowledgement")
	}

	cfg, err := LoadConfig(c)
	if err != nil {
		return err
	}

	opts, err := craftCertOptionsFromCLI(c)
	if err != nil {
		return err
	}

	dialCtx, dialCancel := context.WithTimeout(c.Context, dialTimeout)
	env, err := SetupEnv(dialCtx, cfg)
	dialCancel()
	if err != nil {
		return err
	}
	defer env.Close()

	var certStore certStoreReader
	if dbPath := c.String("db-path"); dbPath != "" {
		certStore, err = openCraftCertStorage(dbPath)
		if err != nil {
			return err
		}
	}

	signerKey, err := loadCraftCertSignerKey(c.String("signer-key-path"), c.String("signer-key-password"))
	if err != nil {
		return err
	}

	cert, err := craftMaliciousCertificate(c.Context, env, certStore, signerKey, opts)
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal crafted certificate: %w", err)
	}
	data = append(data, '\n')

	outPath := c.String("out")
	if outPath == "" {
		_, err = os.Stdout.Write(data)
		return err
	}

	if err := os.WriteFile(filepath.Clean(outPath), data, 0o600); err != nil {
		return fmt.Errorf("write crafted certificate to %s: %w", outPath, err)
	}
	fmt.Printf("Crafted certificate written to %s\n", outPath)
	return nil
}

func craftCertOptionsFromCLI(c *cli.Context) (*craftCertOptions, error) {
	numFakeExits := c.Int("num-fake-exits")
	if numFakeExits <= 0 {
		return nil, fmt.Errorf("--num-fake-exits must be greater than 0")
	}

	amount, ok := new(big.Int).SetString(c.String("amount"), decimalBase)
	if !ok {
		return nil, fmt.Errorf("parse --amount %q as decimal big.Int", c.String("amount"))
	}

	originTokenAddr := common.HexToAddress(c.String("origin-token-address"))

	nonce := c.String("nonce")
	if nonce == "" {
		nonce = strconv.FormatInt(time.Now().UnixNano(), decimalBase)
	}

	return &craftCertOptions{
		numFakeExits:      numFakeExits,
		startingExitIndex: c.Int("starting-exit-index"),
		nonce:             []byte(nonce),
		originNetwork:     uint32(c.Uint("origin-network")),
		originTokenAddr:   originTokenAddr,
		destNetwork:       uint32(c.Uint("destination-network")),
		amount:            amount,
	}, nil
}

func loadCraftCertSignerKey(path, password string) (*ecdsa.PrivateKey, error) {
	if path == "" {
		return nil, fmt.Errorf("--signer-key-path is required")
	}
	contents, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read signer key %s: %w", path, err)
	}
	key, err := keystore.DecryptKey(contents, password)
	if err != nil {
		return nil, fmt.Errorf("decrypt signer key %s: %w", path, err)
	}
	return key.PrivateKey, nil
}

func openCraftCertStorage(dbPath string) (certStoreReader, error) {
	if dbPath == "" {
		return nil, nil
	}
	storage, err := aggsenderdb.NewAggSenderSQLStorage(log.GetDefaultLogger(), aggsenderdb.AggSenderSQLStorageConfig{
		DBPath:          dbPath,
		CertificatesDir: filepath.Join(filepath.Dir(dbPath), "certificates"),
	})
	if err != nil {
		return nil, fmt.Errorf("open aggsender DB at %s: %w", dbPath, err)
	}
	return storage, nil
}

func craftMaliciousCertificate(
	ctx context.Context,
	env *Env,
	certStore certStoreReader,
	signerKey *ecdsa.PrivateKey,
	opts *craftCertOptions,
) (*agglayertypes.Certificate, error) {
	if opts == nil {
		return nil, fmt.Errorf("craft certificate options are required")
	}

	fakeBridgeExits := make([]*agglayertypes.BridgeExit, 0, opts.numFakeExits)
	for i := 0; i < opts.numFakeExits; i++ {
		fakeBridgeExits = append(fakeBridgeExits, makeFakeBridgeExit(opts, opts.startingExitIndex+i))
	}

	certHeight, prevLER, existingLeafCount, l1InfoTreeLeafCount, err := currentCertBaseState(ctx, env, certStore)
	if err != nil {
		return nil, err
	}

	existingHashes, err := loadExistingLeafHashes(ctx, env, certStore, certHeight, existingLeafCount)
	if err != nil {
		return nil, err
	}

	newHashes := make([]common.Hash, 0, len(fakeBridgeExits))
	for _, be := range fakeBridgeExits {
		newHashes = append(newHashes, BridgeExitLeafHash(be))
	}

	newLER, err := ComputeLERForNewLeaves(existingHashes, newHashes)
	if err != nil {
		return nil, fmt.Errorf("compute new local exit root: %w", err)
	}

	cert := &agglayertypes.Certificate{
		NetworkID:           env.L2NetworkID,
		Height:              certHeight,
		PrevLocalExitRoot:   prevLER,
		NewLocalExitRoot:    newLER,
		BridgeExits:         fakeBridgeExits,
		L1InfoTreeLeafCount: l1InfoTreeLeafCount,
	}

	hashToSign, err := validator.HashCertificateToSign(cert)
	if err != nil {
		return nil, fmt.Errorf("hash crafted certificate to sign: %w", err)
	}
	sig, err := crypto.Sign(hashToSign.Bytes(), signerKey)
	if err != nil {
		return nil, fmt.Errorf("sign crafted certificate: %w", err)
	}

	cert.AggchainData = &agglayertypes.AggchainDataMultisig{
		Multisig: &agglayertypes.Multisig{
			Signatures: []agglayertypes.ECDSAMultisigEntry{
				{Index: 0, Signature: sig},
			},
		},
	}

	return cert, nil
}

func currentCertBaseState(
	ctx context.Context,
	env *Env,
	certStore certStoreReader,
) (uint64, common.Hash, uint32, uint32, error) {
	info, err := env.AgglayerClient.GetNetworkInfo(ctx, env.L2NetworkID)
	if err != nil {
		var grpcErr aggkitgrpc.GRPCError
		if !errors.As(err, &grpcErr) || grpcErr.Code != codes.NotFound {
			return 0, common.Hash{}, 0, 0, fmt.Errorf("get network info from agglayer: %w", err)
		}
	}
	if err == nil && info.SettledHeight != nil {
		if info.SettledLER == nil || info.SettledLETLeafCount == nil {
			return 0, common.Hash{}, 0, 0, fmt.Errorf("agglayer returned incomplete settled state")
		}
		certHeight := *info.SettledHeight + 1
		existingLeafCount := uint32(*info.SettledLETLeafCount)
		l1InfoTreeLeafCount := defaultL1InfoTreeLeafCount

		switch {
		case certStore != nil:
			header, headerErr := certStore.GetCertificateHeaderByHeight(*info.SettledHeight)
			if headerErr == nil && header != nil && header.L1InfoTreeLeafCount > 0 {
				l1InfoTreeLeafCount = header.L1InfoTreeLeafCount
			}
		default:
			storedCert, certErr := env.AggsenderRPC.GetCertificateHeaderPerHeight(info.SettledHeight)
			if certErr == nil && storedCert != nil && storedCert.Header != nil && storedCert.Header.L1InfoTreeLeafCount > 0 {
				l1InfoTreeLeafCount = storedCert.Header.L1InfoTreeLeafCount
			}
		}

		return certHeight, *info.SettledLER, existingLeafCount, l1InfoTreeLeafCount, nil
	}

	callOpts := &bind.CallOpts{Context: ctx}
	root, rootErr := env.L2Bridge.GetRoot(callOpts)
	if rootErr != nil {
		return 0, common.Hash{}, 0, 0, fmt.Errorf("get L2 root for initial certificate: %w", rootErr)
	}

	dcBig, dcErr := env.L2Bridge.DepositCount(callOpts)
	if dcErr != nil {
		return 0, common.Hash{}, 0, 0, fmt.Errorf("get L2 deposit count for initial certificate: %w", dcErr)
	}

	return 0, common.Hash(root), uint32(dcBig.Uint64()), defaultL1InfoTreeLeafCount, nil
}

func loadExistingLeafHashes(
	ctx context.Context,
	env *Env,
	certStore certStoreReader,
	certHeight uint64,
	existingLeafCount uint32,
) ([]common.Hash, error) {
	if certHeight == 0 {
		return loadLeafHashesFromBridgeService(ctx, env, existingLeafCount)
	}

	settledHeight := certHeight - 1
	hashes := make([]common.Hash, 0, existingLeafCount)
	for h := uint64(0); h <= settledHeight; h++ {
		exits, err := getStoredBridgeExitsForHeight(env, certStore, h)
		if err != nil {
			return nil, fmt.Errorf("load certificate bridge exits at height %d: %w", h, err)
		}
		for _, be := range exits {
			hashes = append(hashes, BridgeExitLeafHash(be))
		}
	}
	return hashes, nil
}

func loadLeafHashesFromBridgeService(ctx context.Context, env *Env, existingLeafCount uint32) ([]common.Hash, error) {
	hashes := make([]common.Hash, 0, existingLeafCount)
	for dc := uint32(0); dc < existingLeafCount; dc++ {
		br, err := env.BridgeService.GetBridgeByDepositCount(ctx, env.L2NetworkID, dc)
		if err != nil {
			return nil, fmt.Errorf("get bridge service leaf at deposit count %d: %w", dc, err)
		}
		hashes = append(hashes, BridgeResponseLeafHash(br))
	}
	return hashes, nil
}

func getStoredBridgeExitsForHeight(
	env *Env,
	certStore certStoreReader,
	height uint64,
) ([]*agglayertypes.BridgeExit, error) {
	if certStore != nil {
		cert, err := certStore.GetCertificateByHeight(height)
		if err != nil {
			return nil, err
		}
		if cert == nil {
			return nil, fmt.Errorf("certificate not found")
		}
		if cert.Header != nil && cert.Header.CertSource == aggsendertypes.CertificateSourceAggLayer {
			return nil, fmt.Errorf("certificate at height %d has agglayer source and no local bridge exits", height)
		}
		if cert.SignedCertificate == nil {
			return nil, fmt.Errorf("certificate at height %d has no signed certificate payload", height)
		}
		var agglayerCert agglayertypes.Certificate
		if err := json.Unmarshal([]byte(*cert.SignedCertificate), &agglayerCert); err != nil {
			return nil, fmt.Errorf("unmarshal signed certificate at height %d: %w", height, err)
		}
		return agglayerCert.BridgeExits, nil
	}

	exits, err := env.AggsenderRPC.GetCertificateBridgeExits(&height)
	if err != nil {
		return nil, err
	}
	return exits, nil
}

func makeFakeBridgeExit(opts *craftCertOptions, exitIndex int) *agglayertypes.BridgeExit {
	addrBytes := crypto.Keccak256(append(append([]byte(nil), opts.nonce...), byte(exitIndex)))
	return &agglayertypes.BridgeExit{
		LeafType: bridgetypes.LeafTypeAsset,
		TokenInfo: &agglayertypes.TokenInfo{
			OriginNetwork:      opts.originNetwork,
			OriginTokenAddress: opts.originTokenAddr,
		},
		DestinationNetwork: opts.destNetwork,
		DestinationAddress: common.BytesToAddress(addrBytes),
		Amount:             new(big.Int).Set(opts.amount),
		Metadata:           nil,
	}
}
