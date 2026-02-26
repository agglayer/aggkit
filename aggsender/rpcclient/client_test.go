package rpcclient

import (
	"encoding/json"
	"math/big"
	"testing"

	"github.com/0xPolygon/cdk-rpc/rpc"
	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/agglayer/aggkit/aggsender/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/require"
)

func TestGetCertificateHeaderPerHeight(t *testing.T) {
	sut := NewClient("url")
	height := uint64(1)
	responseCert := types.Certificate{Header: &types.CertificateHeader{}}
	responseCertJSON, err := json.Marshal(responseCert)
	require.NoError(t, err)
	response := rpc.Response{
		Result: responseCertJSON,
	}
	jSONRPCCall = func(_, _ string, _ ...interface{}) (rpc.Response, error) {
		return response, nil
	}
	cert, err := sut.GetCertificateHeaderPerHeight(&height)
	require.NoError(t, err)
	require.NotNil(t, cert)
	require.Equal(t, responseCert, *cert)
}

func TestGetCertificateBridgeExits(t *testing.T) {
	sut := NewClient("url")
	height := uint64(42)
	responseExits := []*agglayertypes.BridgeExit{
		{
			LeafType:           0,
			DestinationNetwork: 1,
			DestinationAddress: common.HexToAddress("0xdeadbeef"),
			Amount:             big.NewInt(1000),
		},
	}
	responseExitsJSON, err := json.Marshal(responseExits)
	require.NoError(t, err)
	response := rpc.Response{
		Result: responseExitsJSON,
	}
	jSONRPCCall = func(_, _ string, _ ...interface{}) (rpc.Response, error) {
		return response, nil
	}
	exits, err := sut.GetCertificateBridgeExits(&height)
	require.NoError(t, err)
	require.Len(t, exits, 1)
	require.Equal(t, responseExits[0].DestinationNetwork, exits[0].DestinationNetwork)
	require.Equal(t, responseExits[0].DestinationAddress, exits[0].DestinationAddress)
}

func TestGetStatus(t *testing.T) {
	sut := NewClient("url")
	responseData := types.AggsenderInfo{}
	responseDataJSON, err := json.Marshal(responseData)
	require.NoError(t, err)
	response := rpc.Response{
		Result: responseDataJSON,
	}
	jSONRPCCall = func(_, _ string, _ ...interface{}) (rpc.Response, error) {
		return response, nil
	}
	result, err := sut.GetStatus()
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, responseData, *result)
}

func TestDebugSendCertificate(t *testing.T) {
	sut := NewClient("url")

	privateKey, err := crypto.GenerateKey()
	require.NoError(t, err)

	cert := &agglayertypes.Certificate{Height: 3}
	expectedHash := common.HexToHash("0xdeadbeef")
	expectedHashJSON, err := json.Marshal(expectedHash)
	require.NoError(t, err)

	response := rpc.Response{Result: expectedHashJSON}
	jSONRPCCall = func(_, _ string, _ ...interface{}) (rpc.Response, error) {
		return response, nil
	}

	certHash, err := sut.DebugSendCertificate(cert, privateKey)
	require.NoError(t, err)
	require.Equal(t, expectedHash, certHash)
}
