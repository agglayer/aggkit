package web3signerclient

import (
	"context"
	"os/exec"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestWeb3SignerClientUsingDockerNoKey(t *testing.T) {
	startWeb3SignerDocker(t)
	defer func() {
		stopWeb3SignerDocker(t)
	}()
	sut := NewWeb3SignerClient("http://localhost:9999")
	require.NotNil(t, sut)
	_, err := sut.SignHash(context.Background(), common.Address{}, common.Hash{})
	require.Error(t, err)
	addrs, err := sut.EthAccounts(context.Background())
	require.Error(t, err)
	require.Nil(t, addrs)
}

func startWeb3SignerDocker(t *testing.T) {
	t.Helper()
	stopWeb3SignerDocker(t)
	msg, err := exec.Command("bash", "-l", "-c", "docker run -d -p 9999:9000 --name web3signer consensys/web3signer:develop --http-listen-port=9000 eth1 --chain-id 123").CombinedOutput()
	require.NoError(t, err, string(msg))
}

func stopWeb3SignerDocker(t *testing.T) {
	t.Helper()
	_, err := exec.Command("bash", "-l", "-c", "docker stop web3signer; docker rm web3signer").CombinedOutput()
	if err != nil {
		t.Log(err)
	}
}
