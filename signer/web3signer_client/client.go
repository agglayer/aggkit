package web3signerclient

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/ethereum/go-ethereum/common"
)

type Web3SignerClient struct {
	url string
}

func NewWeb3SignerClient(url string) *Web3SignerClient {
	return &Web3SignerClient{
		url: url,
	}
}

func (e *Web3SignerClient) EthAccounts(ctx context.Context) ([]common.Address, error) {
	response, err := rpc.JSONRPCCallWithContext(ctx, e.url, "eth_accounts")
	if err != nil {
		return nil, err
	}

	if response.Error != nil {
		return nil, fmt.Errorf("%v %v", response.Error.Code, response.Error.Message)
	}
	var result []common.Address
	err = json.Unmarshal(response.Result, &result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func (e *Web3SignerClient) SignHash(ctx context.Context,
	address common.Address,
	hashToSign common.Hash) ([]byte, error) {
	params := []interface{}{address, hashToSign}
	response, err := rpc.JSONRPCCallWithContext(ctx, e.url, "eth_sign", params...)
	if err != nil {
		return nil, err
	}

	if response.Error != nil {
		return nil, fmt.Errorf("%v %v", response.Error.Code, response.Error.Message)
	}
	var resultStr string
	err = json.Unmarshal(response.Result, &resultStr)
	if err != nil {
		return nil, err
	}
	result := common.FromHex(resultStr)
	return result, nil
}
