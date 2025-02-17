package signer

import (
	"context"
	"fmt"

	"github.com/agglayer/aggkit/aggsender/types"
	web3signerclient "github.com/agglayer/aggkit/signer/web3signer_client"
	"github.com/ethereum/go-ethereum/common"
)

type Web3Signer interface {
	EthAccounts(ctx context.Context) ([]common.Address, error)
	SignHash(ctx context.Context, address common.Address, hashToSign common.Hash) ([]byte, error)
}

type Web3SignerConfig struct {
	// Url is the url of the web3 signer
	Url string
	// Address is the address of the account to use, if not specified the first account (if only 1 exposed) will be used
	Address common.Address
}

func NewWeb3SignerConfig(cfg SignerConfig) (Web3SignerConfig, error) {
	var addr common.Address
	addrStr, ok := cfg.Config["address"]
	if ok {
		if !common.IsHexAddress(addrStr.(string)) {
			return Web3SignerConfig{}, fmt.Errorf("invalid address %s", addrStr.(string))
		}
		addr = common.HexToAddress(addrStr.(string))
	}

	return Web3SignerConfig{
		Url:     cfg.Config["url"].(string),
		Address: addr,
	}, nil
}

type Web3SignerSign struct {
	name    string
	logger  types.Logger
	client  Web3Signer
	address common.Address
}

func NewWeb3SignerSign(name string, logger types.Logger, client Web3Signer,
	address common.Address) *Web3SignerSign {
	return &Web3SignerSign{
		name:    name,
		logger:  logger,
		client:  client,
		address: address,
	}
}

func NewWeb3SignerSignFromConfig(name string, logger types.Logger, cfg Web3SignerConfig) *Web3SignerSign {
	client := web3signerclient.NewWeb3SignerClient(cfg.Url)
	return NewWeb3SignerSign(name, logger, client, cfg.Address)

}

func (e *Web3SignerSign) Initialize(ctx context.Context) error {
	if e.client == nil {
		return fmt.Errorf("%s client is nil", e.logPrefix())
	}
	if e.logger == nil {
		return fmt.Errorf("%s logger is nil", e.logPrefix())
	}
	var zeroAddr common.Address
	if e.address == zeroAddr {
		accounts, err := e.client.EthAccounts(ctx)
		if err != nil {
			return err
		}
		if len(accounts) == 0 {
			return fmt.Errorf("%s no accounts found", e.logPrefix())
		}
		if len(accounts) > 1 {
			return fmt.Errorf("%s more than one account found, please specify the account", e.logPrefix())
		}
		e.logger.Infof("%s Using account %v", e.logPrefix(), accounts[0])
		e.address = accounts[0]
	}
	return nil
}

func (e *Web3SignerSign) SignHash(ctx context.Context, hash common.Hash) ([]byte, error) {
	var zeroAddr common.Address
	if e.address == zeroAddr {
		accounts, err := e.client.EthAccounts(ctx)
		if err != nil {
			return nil, err
		}
		if len(accounts) == 0 {
			return nil, fmt.Errorf("no accounts found")
		}
		if len(accounts) > 1 {
			return nil, fmt.Errorf("more than one account found, please specify the account")
		}
		e.address = accounts[0]
	}

	return e.client.SignHash(ctx, e.address, hash)
}

func (e *Web3SignerSign) PublicAddress() common.Address {
	return e.address
}

func (e *Web3SignerSign) logPrefix() string {
	return fmt.Sprintf("Web3SignerSign[%s]: ", e.name)
}

func (e *Web3SignerSign) String() string {
	return fmt.Sprintf("Web3SignerSign[%s]: pubAddr: %s", e.name, e.address.String())
}
