package backward_forward_let

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/validator"
	bridgetypes "github.com/agglayer/aggkit/bridgesync/types"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/go_signer/signer"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/urfave/cli/v2"
)

const craftCertFileMode os.FileMode = 0o600

// RunCraftCert builds a staging-only testing certificate and writes it as JSON.
func RunCraftCert(c *cli.Context) error {
	if !c.Bool("staging-only") {
		return fmt.Errorf("craft-cert is dangerous and requires --staging-only")
	}
	if c.Uint("num-fake-exits") == 0 {
		return fmt.Errorf("--num-fake-exits must be greater than zero")
	}

	cfg, err := LoadConfig(c)
	if err != nil {
		return err
	}
	if f := c.String("cert-exits-file"); f != "" {
		cfg.BackwardForwardLET.CertificateExitsFile = f
	}

	dialCtx, dialCancel := context.WithTimeout(c.Context, dialTimeout)
	env, err := SetupEnv(dialCtx, cfg)
	dialCancel()
	if err != nil {
		return err
	}
	defer env.Close()

	cert, err := craftStagingCertificate(c.Context, env, craftCertOptions{
		NumFakeExits:           uint32(c.Uint("num-fake-exits")),
		Amount:                 c.String("amount"),
		StartingExitIndex:      uint32(c.Uint("starting-exit-index")),
		Nonce:                  c.String("nonce"),
		L1InfoTreeLeafCount:    uint32(c.Uint64("l1-info-tree-leaf-count")),
		SignerIndex:            uint32(c.Uint64("signer-index")),
		CertificateOutputFile:  c.String("out"),
		RequireNoOpenCerts:     true,
		AllowL1InfoCountSource: true,
	})
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(cert, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal crafted certificate: %w", err)
	}
	out := filepath.Clean(c.String("out"))
	if err := os.WriteFile(out, data, craftCertFileMode); err != nil {
		return fmt.Errorf("write crafted certificate %s: %w", out, err)
	}

	fmt.Println("STAGING ONLY: crafted testing certificate.")
	fmt.Printf("Certificate height: %d\n", cert.Height)
	fmt.Printf("Previous local exit root: %s\n", cert.PrevLocalExitRoot.Hex())
	fmt.Printf("New local exit root: %s\n", cert.NewLocalExitRoot.Hex())
	fmt.Printf("Fake bridge exits: %d\n", len(cert.BridgeExits))
	fmt.Printf("Certificate file: %s\n", out)
	fmt.Printf("Next: backward-forward-let --cfg <config> send-cert --cert-file %s --no-db --staging-only\n", out)
	return nil
}

type craftCertOptions struct {
	NumFakeExits           uint32
	Amount                 string
	StartingExitIndex      uint32
	Nonce                  string
	L1InfoTreeLeafCount    uint32
	SignerIndex            uint32
	CertificateOutputFile  string
	RequireNoOpenCerts     bool
	AllowL1InfoCountSource bool
}

func craftStagingCertificate(
	ctx context.Context,
	env *Env,
	opts craftCertOptions,
) (*agglayertypes.Certificate, error) {
	amount, ok := new(big.Int).SetString(opts.Amount, decimalBase)
	if !ok || amount.Sign() < 0 {
		return nil, fmt.Errorf("--amount must be a non-negative base-10 integer")
	}

	info, _, err := getNetworkInfoAllowNotFound(ctx, env.AgglayerClient, env.L2NetworkID)
	if err != nil {
		return nil, err
	}

	var certHeight uint64
	var prevLER common.Hash
	var existingLeafCount uint32
	l1InfoTreeLeafCount := opts.L1InfoTreeLeafCount

	if info.SettledHeight != nil {
		certHeight = *info.SettledHeight + 1
		if info.SettledLER == nil || info.SettledLETLeafCount == nil {
			return nil, fmt.Errorf("agglayer settled state is missing LER or LET leaf count")
		}
		prevLER = *info.SettledLER
		existingLeafCount = uint32(*info.SettledLETLeafCount)
		if opts.RequireNoOpenCerts && hasOpenPendingAtOrAbove(info, certHeight) {
			return nil, fmt.Errorf(
				"pending certificate race: latest pending height/status is %s; "+
					"wait for it to settle or enter InError before crafting",
				pendingSummary(info),
			)
		}
		if l1InfoTreeLeafCount == 0 {
			count, err := l1InfoTreeLeafCountFromAggsender(env, *info.SettledHeight)
			if err != nil {
				return nil, fmt.Errorf(
					"get L1 info tree leaf count from aggsender for height %d: %w; "+
						"rerun with --l1-info-tree-leaf-count",
					*info.SettledHeight, err,
				)
			}
			l1InfoTreeLeafCount = count
		}
	} else {
		if opts.RequireNoOpenCerts && hasOpenPendingAtOrAbove(info, 0) {
			return nil, fmt.Errorf(
				"pending certificate race: latest pending height/status is %s; "+
					"wait for it to settle or enter InError before crafting",
				pendingSummary(info),
			)
		}
		callOpts := &bind.CallOpts{Context: ctx}
		root, err := env.L2Bridge.GetRoot(callOpts)
		if err != nil {
			return nil, fmt.Errorf("get initial L2 bridge root: %w", err)
		}
		prevLER = common.Hash(root)
		dcBig, err := env.L2Bridge.DepositCount(callOpts)
		if err != nil {
			return nil, fmt.Errorf("get initial L2 deposit count: %w", err)
		}
		existingLeafCount = uint32(dcBig.Uint64())
		if l1InfoTreeLeafCount == 0 {
			l1InfoTreeLeafCount = 1
		}
	}

	existingHashes, err := stagingExistingLeafHashes(ctx, env, info.SettledHeight, existingLeafCount)
	if err != nil {
		return nil, err
	}

	fakeExits := makeFakeBridgeExits(opts.NumFakeExits, opts.StartingExitIndex, opts.Nonce, amount)
	newHashes := make([]common.Hash, 0, len(fakeExits))
	for _, be := range fakeExits {
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
		BridgeExits:         fakeExits,
		ImportedBridgeExits: nil,
		L1InfoTreeLeafCount: l1InfoTreeLeafCount,
		CustomChainData:     nil,
		AggchainData:        nil,
	}
	if err := signStagingCertificate(ctx, env, cert, opts.SignerIndex); err != nil {
		return nil, err
	}
	return cert, nil
}

func stagingExistingLeafHashes(
	ctx context.Context,
	env *Env,
	settledHeight *uint64,
	existingLeafCount uint32,
) ([]common.Hash, error) {
	if settledHeight == nil {
		return fetchL2LeafHashesUpTo(ctx, env, existingLeafCount)
	}
	hashes := make([]common.Hash, 0, existingLeafCount)
	for h := uint64(0); h <= *settledHeight; h++ {
		exits, err := getBridgeExitsForHeight(env, h)
		if err != nil {
			return nil, fmt.Errorf("load historical bridge exits for cert height %d: %w", h, err)
		}
		for _, be := range exits {
			hashes = append(hashes, BridgeExitLeafHash(be))
		}
	}
	return hashes, nil
}

func l1InfoTreeLeafCountFromAggsender(env *Env, settledHeight uint64) (uint32, error) {
	cert, err := env.AggsenderRPC.GetCertificateHeaderPerHeight(&settledHeight)
	if err != nil {
		return 0, err
	}
	if cert == nil || cert.Header == nil || cert.Header.L1InfoTreeLeafCount == 0 {
		return 0, fmt.Errorf("aggsender returned no L1InfoTreeLeafCount")
	}
	return cert.Header.L1InfoTreeLeafCount, nil
}

func signStagingCertificate(ctx context.Context, env *Env, cert *agglayertypes.Certificate, signerIndex uint32) error {
	l2ChainID, err := env.chainIDFn(ctx)
	if err != nil {
		return fmt.Errorf("get L2 chain ID: %w", err)
	}
	s, err := signer.NewSigner(
		ctx,
		l2ChainID.Uint64(),
		env.Config.AggSender.AggsenderPrivateKey,
		"staging-craft-cert",
		log.GetDefaultLogger(),
	)
	if err != nil {
		return fmt.Errorf("load aggsender signer: %w", err)
	}
	if err := s.Initialize(ctx); err != nil {
		return fmt.Errorf("initialize aggsender signer: %w", err)
	}
	hashToSign, err := validator.HashCertificateToSign(cert)
	if err != nil {
		return fmt.Errorf("hash crafted certificate: %w", err)
	}
	sig, err := s.SignHash(ctx, hashToSign)
	if err != nil {
		return fmt.Errorf("sign crafted certificate with aggsender signer: %w", err)
	}
	cert.AggchainData = &agglayertypes.AggchainDataMultisig{
		Multisig: &agglayertypes.Multisig{
			Signatures: []agglayertypes.ECDSAMultisigEntry{
				{Index: signerIndex, Signature: sig},
			},
		},
	}
	return nil
}

func makeFakeBridgeExits(count, startingIndex uint32, nonce string, amount *big.Int) []*agglayertypes.BridgeExit {
	if nonce == "" {
		nonce = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	exits := make([]*agglayertypes.BridgeExit, 0, count)
	for i := uint32(0); i < count; i++ {
		exitIndex := startingIndex + i
		addrBytes := crypto.Keccak256([]byte(fmt.Sprintf("%s:%d", nonce, exitIndex)))
		exits = append(exits, &agglayertypes.BridgeExit{
			LeafType: bridgetypes.LeafTypeAsset,
			TokenInfo: &agglayertypes.TokenInfo{
				OriginNetwork:      0,
				OriginTokenAddress: common.Address{},
			},
			DestinationNetwork: 0,
			DestinationAddress: common.BytesToAddress(addrBytes),
			Amount:             new(big.Int).Set(amount),
			Metadata:           nil,
		})
	}
	return exits
}

func hasOpenPendingAtOrAbove(info agglayertypes.NetworkInfo, height uint64) bool {
	if info.LatestPendingHeight == nil || *info.LatestPendingHeight < height {
		return false
	}
	if info.LatestPendingStatus == nil {
		return true
	}
	return info.LatestPendingStatus.IsOpen()
}

func pendingSummary(info agglayertypes.NetworkInfo) string {
	height := "none"
	if info.LatestPendingHeight != nil {
		height = fmt.Sprintf("%d", *info.LatestPendingHeight)
	}
	status := "unknown"
	if info.LatestPendingStatus != nil {
		status = info.LatestPendingStatus.String()
	}
	return fmt.Sprintf("height=%s status=%s", height, status)
}
