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
	repsponseOptimismOutputAtBlock = `{"jsonrpc":"2.0","id":0,"result":{"version":"0x0000000000000000000000000000000000000000000000000000000000000000","outputRoot":"0xe8c88e8022c44c9f1e029e1ede31f0a328b612f2096d7250156676ddb0c3c19d","blockRef":{"hash":"0x0453b9bfea86b4e6f31d6ace2baba5165c7df329eec57fbaf9da261af937756b","number":291,"parentHash":"0xbb14692e900835703350f060dc0809a872875c5dd76d72bb884b88c0a11ed523","timestamp":1747642279,"l1origin":{"hash":"0x2483bf80023480d1e56919fcf21101f0668194ae35ea961c0b26ed084802f441","number":248},"sequenceNumber":6},"withdrawalStorageRoot":"0x8ed4baae3a927be3dea54996b4d5899f8c01e7594bf50b17dc1e741388ce3d12","stateRoot":"0x79283e9c304a4af82f4f18240bd87e79ac5eb106e10e5f7d96cb822de6643b09","syncStatus":{"current_l1":{"hash":"0xfd6f7de2cce9b014ad2c7433b653fde27aba744b8b2c425fd9c0348fce343f89","number":3634,"parentHash":"0x57caf4dc90b33a55e2c3554ea8e28b4434f37330ddfeb9d61c148fcb5fc45029","timestamp":1747649034},"current_l1_finalized":{"hash":"0x69a3ea6f155601362d8738c84b8a5ad9cebebf098d2402bcaacbcead42a9adbc","number":3551,"parentHash":"0xb0a00b979b6ce2663236107da76bbdc11eb74d0d3ef072b0ae61927e43260596","timestamp":1747648868},"head_l1":{"hash":"0xe5f66ddbbadfadad760d82b56d0d6b6877f2f15088b206914de760b12c6e6330","number":3635,"parentHash":"0xfd6f7de2cce9b014ad2c7433b653fde27aba744b8b2c425fd9c0348fce343f89","timestamp":1747649036},"safe_l1":{"hash":"0x20dd22a386d8e786222c0871a42a98b4b44ea1f8bc82b530e87677f043a25f09","number":3559,"parentHash":"0xecda021b42ae16746ef090d9232a6b794f8d4e347cce8cf05ca18f9a6117bbe9","timestamp":1747648884},"finalized_l1":{"hash":"0x69a3ea6f155601362d8738c84b8a5ad9cebebf098d2402bcaacbcead42a9adbc","number":3551,"parentHash":"0xb0a00b979b6ce2663236107da76bbdc11eb74d0d3ef072b0ae61927e43260596","timestamp":1747648868},"unsafe_l2":{"hash":"0xb24e65bff50bef158296aad99b5fad5baa1cfbe32186dea717757518fe110d04","number":7051,"parentHash":"0x48de07e23895f79e5914ecf3a5410e54d1cb61f92925c358a3ffbdf2f32c1e39","timestamp":1747649039,"l1origin":{"hash":"0x32eedddd3b8d4ca8fc83bc89bb40f5431ed165aa1770a278cb5e02811235cbec","number":3629},"sequenceNumber":0},"safe_l2":{"hash":"0x1c451edefb32b093b250a6b7eb8a7d9a5f1f052f65c346390b6f74f7567acb76","number":7031,"parentHash":"0xe443ba289a7cee7fc135e4832296f5d2cc4a4affd899d4b6e02495b575dd23cf","timestamp":1747649019,"l1origin":{"hash":"0x3996bcaa867cc9f7b99f33be04d272705a2b03f6a91295fdb551477e76098f13","number":3621},"sequenceNumber":0},"finalized_l2":{"hash":"0xb1a18eb4c02db7acbda80ed0bf8ab72d2213684eb261578b924dba5d5fef3720","number":6862,"parentHash":"0x25d8cd8c3101031eb71af7d7ee900fb8dbfc99607dd6c616294e5bf30fa26750","timestamp":1747648850,"l1origin":{"hash":"0x62d24093470d3e6cbe6a296ad45984f54f5242193d44fb887111cade3b812bed","number":3536},"sequenceNumber":0},"pending_safe_l2":{"hash":"0x1c451edefb32b093b250a6b7eb8a7d9a5f1f052f65c346390b6f74f7567acb76","number":7031,"parentHash":"0xe443ba289a7cee7fc135e4832296f5d2cc4a4affd899d4b6e02495b575dd23cf","timestamp":1747649019,"l1origin":{"hash":"0x3996bcaa867cc9f7b99f33be04d272705a2b03f6a91295fdb551477e76098f13","number":3621},"sequenceNumber":0},"cross_unsafe_l2":{"hash":"0xb24e65bff50bef158296aad99b5fad5baa1cfbe32186dea717757518fe110d04","number":7051,"parentHash":"0x48de07e23895f79e5914ecf3a5410e54d1cb61f92925c358a3ffbdf2f32c1e39","timestamp":1747649039,"l1origin":{"hash":"0x32eedddd3b8d4ca8fc83bc89bb40f5431ed165aa1770a278cb5e02811235cbec","number":3629},"sequenceNumber":0},"local_safe_l2":{"hash":"0x1c451edefb32b093b250a6b7eb8a7d9a5f1f052f65c346390b6f74f7567acb76","number":7031,"parentHash":"0xe443ba289a7cee7fc135e4832296f5d2cc4a4affd899d4b6e02495b575dd23cf","timestamp":1747649019,"l1origin":{"hash":"0x3996bcaa867cc9f7b99f33be04d272705a2b03f6a91295fdb551477e76098f13","number":3621},"sequenceNumber":0}}}}`
)

func TestExploratory(t *testing.T) {
	//t.Skip("exploratory test")
	client := OpNodeClient{
		url: "http://localhost:32783",
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
