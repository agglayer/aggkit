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

const (
	MethodLocal = "local"
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
	if cfg.Method != MethodLocal {
		return res, fmt.Errorf("invalid signer method %s", cfg.Method)
	}
	pathStr, ok := cfg.Config["path"].(string)
	if !ok {
		return res, fmt.Errorf("field path is not string %v", cfg.Config["path"])
	}
	passStr, ok := cfg.Config["pass"].(string)
	if !ok {
		return res, fmt.Errorf("field pass is not string")
	}
	res = types.KeystoreFileConfig{
		Path:     pathStr,
		Password: passStr,
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

func NewKeyStoreFileSigFromPrivateKey(name string,
	logger commontypes.Logger,
	privateKey *ecdsa.PrivateKey) *KeyStoreFileSign {
	return &KeyStoreFileSign{
		name:          name,
		logger:        logger,
		privateKey:    privateKey,
		publicAddress: crypto.PubkeyToAddress(privateKey.PublicKey),
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
