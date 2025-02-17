package signer

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	ErrUnknownSignerMethod = fmt.Errorf("Unknown signer method")
)

func NewSigner(name string, logger types.Logger, ctx context.Context, cfg SignerConfig) (Signer, error) {
	var res Signer
	if cfg.Method == "" {
		logger.Warnf("No signer method specified, defaulting to local (keystore file)")
		cfg.Method = "local"
	}
	switch cfg.Method {
	case "local":
		specificCfg, err := NewKeyStoreFileConfig(cfg)
		if err != nil {
			return nil, err
		}
		res = NewKeyStoreFileSign(name, logger, specificCfg)
	case "web3signer":
		specificCfg, err := NewWeb3SignerConfig(cfg)
		if err != nil {
			return nil, err
		}
		res = NewWeb3SignerSignFromConfig(name, logger, specificCfg)
	default:
		return nil, fmt.Errorf("unknown signer method %s", cfg.Method)
	}
	err := res.Initialize(ctx)
	return res, err
}
