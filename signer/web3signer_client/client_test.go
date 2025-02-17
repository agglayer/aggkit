package web3signerclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewWeb3SignerClient(t *testing.T) {
	//t.Skip("skipping test")
	sut := NewWeb3SignerClient("http://localhost:9000")
	require.NotNil(t, sut)
}
