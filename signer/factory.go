package signer

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
)

var (
	ErrUnknownSignerMethod = fmt.Errorf("unknown signer method")
)

func NewSigner(name string, logger types.Logger, ctx context.Context, cfg SignerConfig) (Signer, error) {
	var res Signer
	if cfg.Method == "" {
		logger.Warnf("No signer method specified, defaulting to local (keystore file)")
		cfg.Method = MethodLocal
	}
	switch cfg.Method {
	case MethodLocal:
		specificCfg, err := NewLocalConfig(cfg)
		if err != nil {
			return nil, err
		}
		res = NewLocalSign(name, logger, specificCfg)
	case MethodWeb3Signer:
		specificCfg, err := NewWeb3SignerConfig(cfg)
		if err != nil {
			return nil, err
		}
		res = NewWeb3SignerSignFromConfig(name, logger, specificCfg)
	default:
		return nil, fmt.Errorf("unknown signer method %s", cfg.Method)
	}
	return res, nil
}
