package backward_forward_let

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	aggsenderdb "github.com/agglayer/aggkit/aggsender/db"
	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	aggkitgrpc "github.com/agglayer/aggkit/grpc"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	signertypes "github.com/agglayer/go_signer/signer/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v2"
	"google.golang.org/grpc/codes"
)

const defaultL1InfoTreeLeafCount uint32 = 1

const (
	craftCertFetchMaxAttempts    = 6
	craftCertFetchInitialBackoff = 500 * time.Millisecond
	craftCertFetchMaxBackoff     = 5 * time.Second
	craftCertRPCRequestTimeout   = 5 * time.Second
	craftCertFileMode            = 0o600
)

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
	if f := c.String("cert-exits-file"); f != "" {
		cfg.BackwardForwardLET.CertificateExitsFile = f
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

	certSigner, err := loadCraftCertSigner(c.Context, env, cfg, c)
	if err != nil {
		return err
	}

	cert, err := craftMaliciousCertificate(c.Context, env, certStore, certSigner, opts)
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

	if err := os.WriteFile(filepath.Clean(outPath), data, craftCertFileMode); err != nil {
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

func loadCraftCertSigner(
	ctx context.Context,
	env *Env,
	cfg *Config,
	c *cli.Context,
) (signertypes.Signer, error) {
	signerCfg, err := resolveCraftCertSignerConfig(cfg, c)
	if err != nil {
		return nil, err
	}

	l2ChainID, err := env.chainIDFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("get L2 chain ID for craft-cert signer: %w", err)
	}

	signingKey, err := signer.NewSigner(ctx, l2ChainID.Uint64(), signerCfg, "craft-cert", log.GetDefaultLogger())
	if err != nil {
		return nil, fmt.Errorf("load craft-cert signer: %w", err)
	}

	if err := signingKey.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("initialize craft-cert signer: %w", err)
	}

	return signingKey, nil
}

func resolveCraftCertSignerConfig(cfg *Config, c *cli.Context) (signertypes.SignerConfig, error) {
	if c.String("signer-key-path") != "" {
		return signer.NewLocalSignerConfig(c.String("signer-key-path"), c.String("signer-key-password")), nil
	}

	if cfg == nil {
		return signertypes.SignerConfig{}, fmt.Errorf("craft-cert signer config is required")
	}

	if cfg.AggSender.AggsenderPrivateKey.Method == "" {
		return signertypes.SignerConfig{}, fmt.Errorf(
			"craft-cert signer is not configured; set AggSender.AggsenderPrivateKey in config or pass --signer-key-path")
	}

	return cfg.AggSender.AggsenderPrivateKey, nil
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
	certSigner signertypes.HashSigner,
	opts *craftCertOptions,
) (*agglayertypes.Certificate, error) {
	if opts == nil {
		return nil, fmt.Errorf("craft certificate options are required")
	}
	if certSigner == nil {
		return nil, fmt.Errorf("craft certificate signer is required")
	}

	fakeBridgeExits := make([]*agglayertypes.BridgeExit, 0, opts.numFakeExits)
	for i := 0; i < opts.numFakeExits; i++ {
		fakeBridgeExits = append(fakeBridgeExits, makeFakeBridgeExit(opts, opts.startingExitIndex+i))
	}

	certHeight, prevLER, existingLeafCount, l1InfoTreeLeafCount, err := currentCertBaseState(ctx, env, certStore)
	if err != nil {
		return nil, err
	}

	existingHashes, err := loadExistingLeafHashes(ctx, env, certStore, certHeight, prevLER, existingLeafCount)
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
	sig, err := certSigner.SignHash(ctx, hashToSign)
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
	settledLER common.Hash,
	existingLeafCount uint32,
) ([]common.Hash, error) {
	if certHeight == 0 {
		return loadLeafHashesFromBridgeService(ctx, env, existingLeafCount)
	}

	bridgeMatchesSettled, err := currentBridgeMatchesSettled(ctx, env, settledLER, existingLeafCount)
	if err != nil {
		return nil, err
	}
	if bridgeMatchesSettled {
		return loadLeafHashesFromBridgeService(ctx, env, existingLeafCount)
	}

	settledHeight := certHeight - 1
	hashes := make([]common.Hash, 0, existingLeafCount)
	prefixMissing := true
	for h := uint64(0); h <= settledHeight; h++ {
		exits, err := getStoredBridgeExitsForHeight(env, certStore, h)
		if err != nil {
			if !prefixMissing {
				return nil, fmt.Errorf("load certificate bridge exits at height %d after later heights already loaded: %w", h, err)
			}
			continue
		}
		prefixMissing = false
		for _, be := range exits {
			hashes = append(hashes, BridgeExitLeafHash(be))
		}
	}

	if uint32(len(hashes)) > existingLeafCount {
		return nil, fmt.Errorf(
			"loaded %d historical leaf hashes, exceeds expected settled leaf count %d",
			len(hashes), existingLeafCount,
		)
	}

	missingPrefixLeafCount := existingLeafCount - uint32(len(hashes))
	if missingPrefixLeafCount > 0 {
		prefixHashes, err := loadLeafHashesFromBridgeService(ctx, env, missingPrefixLeafCount)
		if err != nil {
			return nil, fmt.Errorf("reconstruct missing certificate prefix from bridge service: %w", err)
		}
		hashes = append(prefixHashes, hashes...)
	}

	if uint32(len(hashes)) != existingLeafCount {
		return nil, fmt.Errorf("reconstructed %d total leaf hashes, expected %d", len(hashes), existingLeafCount)
	}

	return hashes, nil
}

func currentBridgeMatchesSettled(
	ctx context.Context,
	env *Env,
	settledLER common.Hash,
	existingLeafCount uint32,
) (bool, error) {
	if env == nil || env.L2Bridge == nil {
		return false, nil
	}

	callOpts := &bind.CallOpts{Context: ctx}
	root, err := env.L2Bridge.GetRoot(callOpts)
	if err != nil {
		return false, fmt.Errorf("get L2 root for settled-state comparison: %w", err)
	}

	dcBig, err := env.L2Bridge.DepositCount(callOpts)
	if err != nil {
		return false, fmt.Errorf("get L2 deposit count for settled-state comparison: %w", err)
	}

	return common.Hash(root) == settledLER && uint32(dcBig.Uint64()) == existingLeafCount, nil
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
	if env != nil && env.BridgeExitsOverride != nil {
		if exits, ok := env.BridgeExitsOverride.GetExits(height); ok {
			return exits, nil
		}
	}

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
		return parseBridgeExitsFromSignedCertificate(height, *cert.SignedCertificate)
	}

	var lastErr error
	backoff := craftCertFetchInitialBackoff
	for attempt := 1; attempt <= craftCertFetchMaxAttempts; attempt++ {
		cert, headerErr := callCraftCertRPCWithTimeout(
			func() (*aggsendertypes.Certificate, error) {
				return env.AggsenderRPC.GetCertificateHeaderPerHeight(&height)
			},
		)
		if headerErr == nil && cert != nil && cert.SignedCertificate != nil {
			exits, parseErr := parseBridgeExitsFromSignedCertificate(height, *cert.SignedCertificate)
			if parseErr == nil {
				return exits, nil
			}
		} else if headerErr != nil {
			lastErr = headerErr
			if isRetryableCraftCertFetchError(headerErr) {
				if attempt == craftCertFetchMaxAttempts {
					break
				}
				time.Sleep(backoff)
				if backoff < craftCertFetchMaxBackoff {
					backoff *= 2
					if backoff > craftCertFetchMaxBackoff {
						backoff = craftCertFetchMaxBackoff
					}
				}
				continue
			}
		}

		exits, err := callCraftCertRPCWithTimeout(
			func() ([]*agglayertypes.BridgeExit, error) {
				return env.AggsenderRPC.GetCertificateBridgeExits(&height)
			},
		)
		if err == nil {
			return exits, nil
		}
		lastErr = err

		if !isRetryableCraftCertFetchError(err) && !isRetryableCraftCertFetchError(lastErr) {
			return nil, lastErr
		}

		if attempt == craftCertFetchMaxAttempts {
			break
		}
		time.Sleep(backoff)
		if backoff < craftCertFetchMaxBackoff {
			backoff *= 2
			if backoff > craftCertFetchMaxBackoff {
				backoff = craftCertFetchMaxBackoff
			}
		}
	}

	return nil, lastErr
}

func callCraftCertRPCWithTimeout[T any](fn func() (T, error)) (T, error) {
	type result struct {
		value T
		err   error
	}

	resultCh := make(chan result, 1)
	go func() {
		value, err := fn()
		resultCh <- result{value: value, err: err}
	}()

	select {
	case result := <-resultCh:
		return result.value, result.err
	case <-time.After(craftCertRPCRequestTimeout):
		var zero T
		return zero, fmt.Errorf("aggsender RPC request timed out after %s", craftCertRPCRequestTimeout)
	}
}

func parseBridgeExitsFromSignedCertificate(height uint64, signedCert string) ([]*agglayertypes.BridgeExit, error) {
	var agglayerCert agglayertypes.Certificate
	if err := json.Unmarshal([]byte(signedCert), &agglayerCert); err != nil {
		return nil, fmt.Errorf("unmarshal signed certificate at height %d: %w", height, err)
	}
	return agglayerCert.BridgeExits, nil
}

func isRetryableCraftCertFetchError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "found: 429") ||
		strings.Contains(msg, "too many requests") ||
		strings.Contains(msg, "connect: connection refused") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "timeout")
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
