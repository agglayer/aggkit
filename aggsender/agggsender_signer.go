package aggsender

import (
	"context"
	"fmt"

	kms "cloud.google.com/go/kms/apiv1"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/pascaldekloe/etherkeyms"
)

type funcSignHash = func(context.Context, common.Hash) ([]byte, error)

func newSigner(logger *log.Logger, cfg Config) (funcSignHash, common.Address, error) {
	var signer funcSignHash
	var err error
	var publicKey common.Address
	if cfg.KMSKeyName != "" {
		logger.Debugf("using KMS key: %s", cfg.KMSKeyName)
		signer, publicKey, err = useKMSAuth(cfg)
		if err != nil {
			return nil, common.Address{}, fmt.Errorf("error using KMS: %w", err)
		}
	} else {
		log.Debugf("using local private key: %s", cfg.AggsenderPrivateKey.Path)
		signer, publicKey, err = useLocalAuth(cfg)
		if err != nil {
			return nil, common.Address{}, fmt.Errorf("error using local key: %w", err)
		}
	}
	return signer, publicKey, nil
}

func useKMSAuth(config Config) (funcSignHash, common.Address, error) {
	ctx, cancel := context.WithTimeout(context.Background(), config.KMSConnectionTimeout.Duration)
	defer cancel()

	client, err := kms.NewKeyManagementClient(ctx)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to create kms client: %w", err)
	}

	mk, err := etherkeyms.NewManagedKey(ctx, client, config.KMSKeyName)
	if err != nil {
		return nil, common.Address{}, fmt.Errorf("failed to create managed key: %w", err)
	}
	return mk.SignHash, mk.EthereumAddr, nil

}

func useLocalAuth(config Config) (funcSignHash, common.Address, error) {
	privateKey, err := aggkitcommon.NewKeyFromKeystore(config.AggsenderPrivateKey)
	if err != nil {
		return nil, common.Address{}, err
	}
	addr := crypto.PubkeyToAddress(privateKey.PublicKey)
	return func(ctx context.Context, hash common.Hash) ([]byte, error) {
		return crypto.Sign(hash.Bytes(), privateKey)
	}, addr, nil
}
