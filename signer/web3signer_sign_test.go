package signer

import (
	"context"
	"fmt"
	"testing"

	web3signerclient "github.com/agglayer/aggkit/signer/web3signer_client"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestWeb3SignerClientExploratory(t *testing.T) {
	t.Skip("skipping test")
	sut := Web3SignerSign{
		client: web3signerclient.NewWeb3SignerClient("http://localhost:9000"),
	}
	res, err := sut.SignHash(context.Background(), common.Hash{})
	require.NoError(t, err)
	fmt.Print(res)
}
