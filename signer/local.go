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
	MethodLocal   = "local"
	FieldPath     = "path"
	FieldPassword = "password"
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

func NewLocalSignerConfig(path, pass string) SignerConfig {
	return SignerConfig{
		Method: MethodLocal,
		Config: map[string]interface{}{
			FieldPath:     path,
			FieldPassword: pass,
		},
	}
}

func NewKeyStoreFileConfig(cfg SignerConfig) (types.KeystoreFileConfig, error) {
	var res types.KeystoreFileConfig
	// If there are no field in the config, return empty config
	// but if there are some field must match the expected ones
	if len(cfg.Config) == 0 {
		return types.KeystoreFileConfig{}, nil
	}
	pathStr, ok := cfg.Config[FieldPath].(string)
	if !ok {
		return res, fmt.Errorf("field path is not string %v", cfg.Config[FieldPath])
	}
	passStr, ok := cfg.Config[FieldPassword].(string)
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
		return nil, fmt.Errorf("%s private key is nil", e.logPrefix())
	}
	return crypto.Sign(hash.Bytes(), e.privateKey)
}

func (e *KeyStoreFileSign) PublicAddress() common.Address {
	return e.publicAddress
}
func (e *KeyStoreFileSign) String() string {
	return fmt.Sprintf("%s path:%s, pubAddr: %s", e.logPrefix(), e.file, e.publicAddress.String())
}

func (e *KeyStoreFileSign) logPrefix() string {
	return fmt.Sprintf("localSigner[%s]: ", e.name)
}
