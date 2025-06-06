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
		return nil, fmt.Errorf("opNodeClient error calling optimism_syncStatus jSONRPCCall. Err:%w", err)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("opNodeClient error calling optimism_syncStatus, server returns error: %v %v",
			response.Error.Code, response.Error.Message)
	}
	var result BlockInfo
	var data map[string]interface{}
	err = json.Unmarshal(response.Result, &data)
	if err != nil {
		return nil, fmt.Errorf("opNodeClient error calling optimism_syncStatus. Unmarshal json fails. Err:%w", err)
	}
	if finalizedL2, ok := data["finalized_l2"]; ok {
		marshaled, err := json.Marshal(finalizedL2)
		if err != nil {
			return nil, fmt.Errorf("opNodeClient error converting finalizedL2 to json. Err: %w", err)
		}
		err = json.Unmarshal(marshaled, &result)
		if err != nil {
			return nil, fmt.Errorf("opNodeClient error unmarshaling finalizedL2 key. Err: %w", err)
		}
	} else {
		return nil, fmt.Errorf("finalized_l2 not found in RPC response")
	}
	return &result, nil
}

// OutputAtBlockRoot retrieves the output root at a specific block number from the OP Node.
func (c *OpNodeClient) OutputAtBlockRoot(number uint64) (common.Hash, error) {
	emptyAnswer := common.Hash{}
	NumberHex := fmt.Sprintf("0x%x", number)
	response, err := jSONRPCCall(c.url, "optimism_outputAtBlock", NumberHex)
	if err != nil {
		return emptyAnswer, fmt.Errorf("opNodeClient error calling optimism_outputAtBlock jSONRPCCall. Err:%w", err)
	}
	if response.Error != nil {
		return emptyAnswer, fmt.Errorf("opNodeClient error calling optimism_outputAtBlock, server returns error: %v %v",
			response.Error.Code, response.Error.Message)
	}
	var data map[string]interface{}
	err = json.Unmarshal(response.Result, &data)
	if err != nil {
		return emptyAnswer, fmt.Errorf("opNodeClient error calling optimism_outputAtBlock. Unmarshal json fails. Err:%w", err)
	}
	if outputRoot, ok := data["outputRoot"]; ok {
		str, ok := outputRoot.(string)
		if !ok {
			return emptyAnswer, fmt.Errorf("opNodeClient.OutputAtBlockRoot: outputRoot is not a string")
		}
		return common.HexToHash(str), nil
	} else {
		return emptyAnswer, fmt.Errorf("opNodeClient.OutputAtBlockRoot: outputRoot not found in RPC response")
	}
}
