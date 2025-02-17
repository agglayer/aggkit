package signer

import (
	"context"
	"crypto/ecdsa"
	"fmt"

	commontypes "github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// // AggsenderPrivateKey is the private key which is used to sign certificates
// AggsenderPrivateKey types.KeystoreFileConfig `mapstructure:"AggsenderPrivateKey"`
type KeyStoreFileSign struct {
	name          string
	logger        commontypes.Logger
	file          types.KeystoreFileConfig
	privateKey    *ecdsa.PrivateKey
	publicAddress common.Address
}

func NewKeyStoreFileConfig(cfg SignerConfig) (types.KeystoreFileConfig, error) {
	var res types.KeystoreFileConfig
	if cfg.Method != "local" {
		return res, fmt.Errorf("invalid signer method %s", cfg.Method)
	}
	res = types.KeystoreFileConfig{
		Path:     cfg.Config["path"].(string),
		Password: cfg.Config["pass"].(string),
	}
	return res, nil
}

func NewKeyStoreFileSign(name string, logger commontypes.Logger, file types.KeystoreFileConfig) *KeyStoreFileSign {
	return &KeyStoreFileSign{
		name:   name,
		logger: logger,
		file:   file,
	}
}

func (e *KeyStoreFileSign) Initialize(ctx context.Context) error {
	privateKey, err := aggkitcommon.NewKeyFromKeystore(e.file)
	if err != nil {
		return err
	}
	e.privateKey = privateKey
	e.publicAddress = crypto.PubkeyToAddress(privateKey.PublicKey)
	return nil
}

func (e *KeyStoreFileSign) SignHash(ctx context.Context, hash common.Hash) ([]byte, error) {
	if e.privateKey == nil {
		return nil, fmt.Errorf("private key is nil")
	}
	return crypto.Sign(hash.Bytes(), e.privateKey)
}

func (e *KeyStoreFileSign) PublicAddress() common.Address {
	return e.publicAddress
}
func (e *KeyStoreFileSign) String() string {
	return fmt.Sprintf("local[%s]: path:%s, pubAddr: %s", e.name, e.file, e.publicAddress.String())
}
