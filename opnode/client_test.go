package opnode

import (
	"fmt"
	"testing"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const responseOptimismSyncStatus = `
{"current_l1":{"hash":"0x27be7488f9e079c20de05cf84c1f34e92fc82c094bd4ceceaa9d20e7ba8a5c65","number":722,"parentHash":"0x03619f1e9bd4d6fa1bd2d01b6385d686bbdbcdac8d097eb3b471ff6269dcb38e","timestamp":1741258239},"current_l1_finalized":{"hash":"0x9b846c1af03cabdcec3c61012760bf5b4fe5729ff244550d2060d199658440f7","number":696,"parentHash":"0x82ebf2ac6d28de349d38578d6ccdc368c10f440c542ae3222fbaa28e3d505d72","timestamp":1741258083},"head_l1":{"hash":"0x48f52a60944197527fb5f4cb886c6a053024eb3a1b2d07c6dde848c6b4b6c40f","number":723,"parentHash":"0x27be7488f9e079c20de05cf84c1f34e92fc82c094bd4ceceaa9d20e7ba8a5c65","timestamp":1741258245},"safe_l1":{"hash":"0xe72f0a3cd7dcccde2eec8266c3bd86ae059ede7731231491e9614f5aae2c9561","number":704,"parentHash":"0xdf96fc65a16bf9714b5f519738c6f2b4ed923062e4a988aa9a7ad9d3ddf161b0","timestamp":1741258131},"finalized_l1":{"hash":"0x9b846c1af03cabdcec3c61012760bf5b4fe5729ff244550d2060d199658440f7","number":696,"parentHash":"0x82ebf2ac6d28de349d38578d6ccdc368c10f440c542ae3222fbaa28e3d505d72","timestamp":1741258083},"unsafe_l2":{"hash":"0x853c5fa5dc7ed99f13f6d5fed24d4f075dc03ce6b1fba9feda9d25d4e3abb3bd","number":2134,"parentHash":"0x2dfc5575c223feb14a44ce7bb1c37406ecd12c0aa04a3a7422e486f4e98f3764","timestamp":1741258253,"l1origin":{"hash":"0x03619f1e9bd4d6fa1bd2d01b6385d686bbdbcdac8d097eb3b471ff6269dcb38e","number":721},"sequenceNumber":0},"safe_l2":{"hash":"0xe98cbd9bfb054f5c06c20c987caf0237d3b0f64b9c18f8a1c4d9ae1be856b50f","number":2115,"parentHash":"0xf67dbe7c64cf864c0f02caed5f0ffecd7e7f11f917b059624a926820ba78fbc9","timestamp":1741258215,"l1origin":{"hash":"0x403dc11fb1084e3fd0584e8e6e3ae74de9719cdbf80b8e384f02bf1f78ed12d8","number":714},"sequenceNumber":0},"finalized_l2":{"hash":"0x017b1679006ccc9bca588063054bd455957da9254b745d7cacbe2d211f548f7d","number":2036,"parentHash":"0x1656b9a20dbdad0887bdbdf693645a33a90d0628384879b74f654f06475f2dc8","timestamp":1741258057,"l1origin":{"hash":"0x67955f5b1e5d3d2214197ec44a386f8cacb46ad8555c40bf2ed105fb1b2dd76d","number":687},"sequenceNumber":4},"pending_safe_l2":{"hash":"0xe98cbd9bfb054f5c06c20c987caf0237d3b0f64b9c18f8a1c4d9ae1be856b50f","number":2115,"parentHash":"0xf67dbe7c64cf864c0f02caed5f0ffecd7e7f11f917b059624a926820ba78fbc9","timestamp":1741258215,"l1origin":{"hash":"0x403dc11fb1084e3fd0584e8e6e3ae74de9719cdbf80b8e384f02bf1f78ed12d8","number":714},"sequenceNumber":0},"cross_unsafe_l2":{"hash":"0x853c5fa5dc7ed99f13f6d5fed24d4f075dc03ce6b1fba9feda9d25d4e3abb3bd","number":2134,"parentHash":"0x2dfc5575c223feb14a44ce7bb1c37406ecd12c0aa04a3a7422e486f4e98f3764","timestamp":1741258253,"l1origin":{"hash":"0x03619f1e9bd4d6fa1bd2d01b6385d686bbdbcdac8d097eb3b471ff6269dcb38e","number":721},"sequenceNumber":0},"local_safe_l2":{"hash":"0xe98cbd9bfb054f5c06c20c987caf0237d3b0f64b9c18f8a1c4d9ae1be856b50f","number":2115,"parentHash":"0xf67dbe7c64cf864c0f02caed5f0ffecd7e7f11f917b059624a926820ba78fbc9","timestamp":1741258215,"l1origin":{"hash":"0x403dc11fb1084e3fd0584e8e6e3ae74de9719cdbf80b8e384f02bf1f78ed12d8","number":714},"sequenceNumber":0}}
`

func TestExploratory(t *testing.T) {
	t.Skip("exploratory test")
	client := OpNodeClient{
		url: "http://localhost:32807",
	}
	blockInfo, err := client.FinalizedL2Block()
	require.NoError(t, err)
	require.NotNil(t, blockInfo)
	fmt.Printf("BlockInfo: %+v\n", blockInfo)
}

func TestFinalizedMulti(t *testing.T) {
	cases := []struct {
		name                 string
		jSONRPCCallError     error
		responseData         string
		responseError        *rpc.ErrorObject
		expectedBlockInfo    *BlockInfo
		expectedErrorContain string
	}{
		{
			name:          "happy path",
			responseData:  responseOptimismSyncStatus,
			responseError: nil,
			expectedBlockInfo: &BlockInfo{
				Number:     2036,
				Hash:       common.HexToHash("0x017b1679006ccc9bca588063054bd455957da9254b745d7cacbe2d211f548f7d"),
				ParentHash: common.HexToHash("0x1656b9a20dbdad0887bdbdf693645a33a90d0628384879b74f654f06475f2dc8"),
				Timestamp:  1741258057,
			},
			expectedErrorContain: "",
		},
		{
			name:                 "response null",
			responseData:         "null",
			expectedErrorContain: "not found",
		},
		{
			name:                 "jSONRPCCall fails",
			jSONRPCCallError:     fmt.Errorf("error"),
			expectedErrorContain: "jSONRPCCall",
		},
		{
			name:                 "jSONRPCCall ok, return error",
			responseError:        &rpc.ErrorObject{Code: 1, Message: "error"},
			expectedErrorContain: "server returns",
		},

		{
			name:                 "server returns wrong json",
			responseData:         "{",
			expectedErrorContain: "Unmarshal",
		},
	}

	for _, tc := range cases {
		client := OpNodeClient{}
		response := rpc.Response{
			Result: []byte(tc.responseData),
			Error:  tc.responseError,
		}
		jSONRPCCall = func(_, _ string, _ ...interface{}) (rpc.Response, error) {
			return response, tc.jSONRPCCallError
		}
		blockInfo, err := client.FinalizedL2Block()
		if tc.expectedErrorContain != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErrorContain)
		} else {
			require.NoError(t, err)
			require.NotNil(t, blockInfo)
			require.Equal(t, tc.expectedBlockInfo, blockInfo)
		}
	}
}
