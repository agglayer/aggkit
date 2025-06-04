package opnode

import (
	"fmt"
	"testing"

	"github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

const (
	responseOptimismSyncStatus = `
{"current_l1":{"hash":"0x27be7488f9e079c20de05cf84c1f34e92fc82c094bd4ceceaa9d20e7ba8a5c65","number":722,"parentHash":"0x03619f1e9bd4d6fa1bd2d01b6385d686bbdbcdac8d097eb3b471ff6269dcb38e","timestamp":1741258239},"current_l1_finalized":{"hash":"0x9b846c1af03cabdcec3c61012760bf5b4fe5729ff244550d2060d199658440f7","number":696,"parentHash":"0x82ebf2ac6d28de349d38578d6ccdc368c10f440c542ae3222fbaa28e3d505d72","timestamp":1741258083},"head_l1":{"hash":"0x48f52a60944197527fb5f4cb886c6a053024eb3a1b2d07c6dde848c6b4b6c40f","number":723,"parentHash":"0x27be7488f9e079c20de05cf84c1f34e92fc82c094bd4ceceaa9d20e7ba8a5c65","timestamp":1741258245},"safe_l1":{"hash":"0xe72f0a3cd7dcccde2eec8266c3bd86ae059ede7731231491e9614f5aae2c9561","number":704,"parentHash":"0xdf96fc65a16bf9714b5f519738c6f2b4ed923062e4a988aa9a7ad9d3ddf161b0","timestamp":1741258131},"finalized_l1":{"hash":"0x9b846c1af03cabdcec3c61012760bf5b4fe5729ff244550d2060d199658440f7","number":696,"parentHash":"0x82ebf2ac6d28de349d38578d6ccdc368c10f440c542ae3222fbaa28e3d505d72","timestamp":1741258083},"unsafe_l2":{"hash":"0x853c5fa5dc7ed99f13f6d5fed24d4f075dc03ce6b1fba9feda9d25d4e3abb3bd","number":2134,"parentHash":"0x2dfc5575c223feb14a44ce7bb1c37406ecd12c0aa04a3a7422e486f4e98f3764","timestamp":1741258253,"l1origin":{"hash":"0x03619f1e9bd4d6fa1bd2d01b6385d686bbdbcdac8d097eb3b471ff6269dcb38e","number":721},"sequenceNumber":0},"safe_l2":{"hash":"0xe98cbd9bfb054f5c06c20c987caf0237d3b0f64b9c18f8a1c4d9ae1be856b50f","number":2115,"parentHash":"0xf67dbe7c64cf864c0f02caed5f0ffecd7e7f11f917b059624a926820ba78fbc9","timestamp":1741258215,"l1origin":{"hash":"0x403dc11fb1084e3fd0584e8e6e3ae74de9719cdbf80b8e384f02bf1f78ed12d8","number":714},"sequenceNumber":0},"finalized_l2":{"hash":"0x017b1679006ccc9bca588063054bd455957da9254b745d7cacbe2d211f548f7d","number":2036,"parentHash":"0x1656b9a20dbdad0887bdbdf693645a33a90d0628384879b74f654f06475f2dc8","timestamp":1741258057,"l1origin":{"hash":"0x67955f5b1e5d3d2214197ec44a386f8cacb46ad8555c40bf2ed105fb1b2dd76d","number":687},"sequenceNumber":4},"pending_safe_l2":{"hash":"0xe98cbd9bfb054f5c06c20c987caf0237d3b0f64b9c18f8a1c4d9ae1be856b50f","number":2115,"parentHash":"0xf67dbe7c64cf864c0f02caed5f0ffecd7e7f11f917b059624a926820ba78fbc9","timestamp":1741258215,"l1origin":{"hash":"0x403dc11fb1084e3fd0584e8e6e3ae74de9719cdbf80b8e384f02bf1f78ed12d8","number":714},"sequenceNumber":0},"cross_unsafe_l2":{"hash":"0x853c5fa5dc7ed99f13f6d5fed24d4f075dc03ce6b1fba9feda9d25d4e3abb3bd","number":2134,"parentHash":"0x2dfc5575c223feb14a44ce7bb1c37406ecd12c0aa04a3a7422e486f4e98f3764","timestamp":1741258253,"l1origin":{"hash":"0x03619f1e9bd4d6fa1bd2d01b6385d686bbdbcdac8d097eb3b471ff6269dcb38e","number":721},"sequenceNumber":0},"local_safe_l2":{"hash":"0xe98cbd9bfb054f5c06c20c987caf0237d3b0f64b9c18f8a1c4d9ae1be856b50f","number":2115,"parentHash":"0xf67dbe7c64cf864c0f02caed5f0ffecd7e7f11f917b059624a926820ba78fbc9","timestamp":1741258215,"l1origin":{"hash":"0x403dc11fb1084e3fd0584e8e6e3ae74de9719cdbf80b8e384f02bf1f78ed12d8","number":714},"sequenceNumber":0}}
`
	responseOptimismOutputAtBlock = `
{"version":"0x0000000000000000000000000000000000000000000000000000000000000000","outputRoot":"0x6628e5718f27087e5260abb5f311de11684b508117f8d7f825c55f47864eb4b5","blockRef":{"hash":"0xe7128f7db5b884be3d28b0090aa6ca8218d1041e72454f8b01866cd9569c2fa7","number":71014,"parentHash":"0xd6775450157dff2a5aca3f5f8572d6220d57aea51877ece5627cab52b1b24f22","timestamp":1749039155,"l1origin":{"hash":"0x6a43026371318524505e5ae64b5b24098d66b68632069b9a1e27f5673bf13576","number":71067},"sequenceNumber":0},"withdrawalStorageRoot":"0x8ed4baae3a927be3dea54996b4d5899f8c01e7594bf50b17dc1e741388ce3d12","stateRoot":"0xed3333ee703cfa3bd9101a6777d8e3214c25699a1bb04dd0704afa60a6f90fec","syncStatus":{"current_l1":{"hash":"0x887bf7743f7e0d656111386df191363232f514dddba85220de4acbb2ef9ea659","number":71122,"parentHash":"0xe64cfb121d7c14bb79b46b0b96d57ce7e61ae488ffb76abb0c00648c7c610f4e","timestamp":1749039194},"current_l1_finalized":{"hash":"0xeb5c4cbbcbd0dbaa7e5ff05f8dbda6718bdf5ed06d3abe8cfc4c41abd6e619cf","number":71099,"parentHash":"0xa9cf988ae3987e2fa3f75ce9849a1d0171e95e9a643da332115203e8a1ab92fb","timestamp":1749039171},"head_l1":{"hash":"0xeda5f71cc0ead514711c63e6911d2bfe08e6f13d9ad081d7b1bd7907b2433861","number":71123,"parentHash":"0x887bf7743f7e0d656111386df191363232f514dddba85220de4acbb2ef9ea659","timestamp":1749039195},"safe_l1":{"hash":"0x1cff950e8370f3e7557e04eb76a8436ad3288d18ea836389baf76a0e65f80b3c","number":71107,"parentHash":"0x64c4069751f07c6ac687eca73f2f6d3d0c3f19ec9d68803d2a4d31c6ba1cc945","timestamp":1749039179},"finalized_l1":{"hash":"0xeb5c4cbbcbd0dbaa7e5ff05f8dbda6718bdf5ed06d3abe8cfc4c41abd6e619cf","number":71099,"parentHash":"0xa9cf988ae3987e2fa3f75ce9849a1d0171e95e9a643da332115203e8a1ab92fb","timestamp":1749039171},"unsafe_l2":{"hash":"0xb11c2bceaa7d03230b592df7fc68dcf9846cab05c31709928147ef8f57f1f3b8","number":71065,"parentHash":"0xf453a3d91ad41a585765777f2d256ec4a2e7e2a37308b261f30e63c92fd863e4","timestamp":1749039206,"l1origin":{"hash":"0x70b830f2982c00e4484411bf9e0e0ff24ca4a2bb5544918294fb05a71476cdd3","number":71118},"sequenceNumber":0},"safe_l2":{"hash":"0x938b3a5cd4c695e4300512342ae4e77a688504dd918ae4592c2b7cd2bc732619","number":71039,"parentHash":"0xfa53eeb910d41f49fd4774d04908316edea458b83c305b0aa75430999ac6761b","timestamp":1749039180,"l1origin":{"hash":"0xa6829f4ba03fe2449787cf2ca7f76f9b2e628a3c6361bf16b5bbcca983f5137b","number":71092},"sequenceNumber":0},"finalized_l2":{"hash":"0xe7128f7db5b884be3d28b0090aa6ca8218d1041e72454f8b01866cd9569c2fa7","number":71014,"parentHash":"0xd6775450157dff2a5aca3f5f8572d6220d57aea51877ece5627cab52b1b24f22","timestamp":1749039155,"l1origin":{"hash":"0x6a43026371318524505e5ae64b5b24098d66b68632069b9a1e27f5673bf13576","number":71067},"sequenceNumber":0},"pending_safe_l2":{"hash":"0x938b3a5cd4c695e4300512342ae4e77a688504dd918ae4592c2b7cd2bc732619","number":71039,"parentHash":"0xfa53eeb910d41f49fd4774d04908316edea458b83c305b0aa75430999ac6761b","timestamp":1749039180,"l1origin":{"hash":"0xa6829f4ba03fe2449787cf2ca7f76f9b2e628a3c6361bf16b5bbcca983f5137b","number":71092},"sequenceNumber":0},"cross_unsafe_l2":{"hash":"0xb11c2bceaa7d03230b592df7fc68dcf9846cab05c31709928147ef8f57f1f3b8","number":71065,"parentHash":"0xf453a3d91ad41a585765777f2d256ec4a2e7e2a37308b261f30e63c92fd863e4","timestamp":1749039206,"l1origin":{"hash":"0x70b830f2982c00e4484411bf9e0e0ff24ca4a2bb5544918294fb05a71476cdd3","number":71118},"sequenceNumber":0},"local_safe_l2":{"hash":"0x938b3a5cd4c695e4300512342ae4e77a688504dd918ae4592c2b7cd2bc732619","number":71039,"parentHash":"0xfa53eeb910d41f49fd4774d04908316edea458b83c305b0aa75430999ac6761b","timestamp":1749039180,"l1origin":{"hash":"0xa6829f4ba03fe2449787cf2ca7f76f9b2e628a3c6361bf16b5bbcca983f5137b","number":71092},"sequenceNumber":0}}}`
)

func TestExploratory(t *testing.T) {
	t.Skip("exploratory test")
	client := OpNodeClient{
		url: "http://localhost:32782",
	}
	blockInfo, err := client.FinalizedL2Block()
	require.NoError(t, err)
	require.NotNil(t, blockInfo)
	fmt.Printf("BlockInfo: %+v\n", blockInfo)

	hash, err := client.OutputAtBlockRoot(blockInfo.Number)
	require.NoError(t, err)
	fmt.Printf("OutputAtBlockRoot(%d): %+v\n", blockInfo.Number, hash)
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
func TestOutputAtBlockRoot(t *testing.T) {
	cases := []struct {
		name                 string
		blockNumber          uint64
		jSONRPCCallError     error
		responseData         string
		responseError        *rpc.ErrorObject
		expectedHash         common.Hash
		expectedErrorContain string
	}{
		{
			name:         "happy path",
			blockNumber:  291,
			responseData: responseOptimismOutputAtBlock,
			expectedHash: common.HexToHash("0x6628e5718f27087e5260abb5f311de11684b508117f8d7f825c55f47864eb4b5"),
		},
		{
			name:                 "response null",
			blockNumber:          291,
			responseData:         "null",
			expectedErrorContain: "not found",
		},
		{
			name:                 "jSONRPCCall fails",
			blockNumber:          291,
			jSONRPCCallError:     fmt.Errorf("error"),
			expectedErrorContain: "jSONRPCCall",
		},
		{
			name:                 "jSONRPCCall ok, return error",
			blockNumber:          291,
			responseError:        &rpc.ErrorObject{Code: 1, Message: "error"},
			expectedErrorContain: "server returns",
		},
		{
			name:                 "server returns wrong json",
			blockNumber:          291,
			responseData:         "{",
			expectedErrorContain: "Unmarshal",
		},
		{
			name:                 "outputRoot not found",
			blockNumber:          291,
			responseData:         `{"otherKey": "value"}`,
			expectedErrorContain: "outputRoot not found",
		},
		{
			name:                 "outputRoot is not a string",
			blockNumber:          291,
			responseData:         `{"outputRoot": 123}`,
			expectedErrorContain: "outputRoot is not a string",
		},
	}

	for _, tc := range cases {
		t.Log("Running test case:", tc.name)
		client := OpNodeClient{}
		response := rpc.Response{
			Result: []byte(tc.responseData),
			Error:  tc.responseError,
		}
		jSONRPCCall = func(_, _ string, _ ...interface{}) (rpc.Response, error) {
			return response, tc.jSONRPCCallError
		}
		hash, err := client.OutputAtBlockRoot(tc.blockNumber)
		if tc.expectedErrorContain != "" {
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErrorContain)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.expectedHash, hash)
		}
	}
}
