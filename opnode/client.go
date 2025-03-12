package opnode

import (
	"encoding/json"
	"fmt"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/ethereum/go-ethereum/common"
)

var jSONRPCCall = rpc.JSONRPCCall

type OpNodeClient struct {
	url string
}

func NewOpNodeClient(url string) *OpNodeClient {
	return &OpNodeClient{
		url: url,
	}
}

type BlockInfo struct {
	Number     uint64      `json:"number"`
	Hash       common.Hash `json:"hash"`
	ParentHash common.Hash `json:"parentHash"`
	Timestamp  uint64      `json:"timestamp"`
}

func (c *OpNodeClient) FinalizedL2Block() (*BlockInfo, error) {
	response, err := jSONRPCCall(c.url, "optimism_syncStatus")
	if err != nil {
		return nil, err
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%v %v", response.Error.Code, response.Error.Message)
	}
	var result BlockInfo
	var data map[string]interface{}
	err = json.Unmarshal(response.Result, &data)
	if err != nil {
		return nil, err
	}
	if finalizedL2, ok := data["finalized_l2"]; ok {
		marshaled, err := json.Marshal(finalizedL2)
		if err != nil {
			return nil, fmt.Errorf("error converting finalizedL2 to json. Err: %w", err)
		}
		err = json.Unmarshal(marshaled, &result)
		if err != nil {
			return nil, fmt.Errorf("error unmarshaling finalizedL2 key. Err: %w", err)
		}
	} else {
		return nil, fmt.Errorf("finalized_l2 not found in RPC response")
	}
	return &result, nil
}
