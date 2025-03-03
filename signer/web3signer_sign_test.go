package signer

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/signer/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestFailsCantSetAddressToUse(t *testing.T) {
	mockWeb3SignerClient := mocks.NewWeb3SignerClienter(t)
	ctx := context.TODO()
	logger := log.WithFields("test", "test")
	sut := NewWeb3SignerSign("name", logger, mockWeb3SignerClient, common.Address{})
	mockWeb3SignerClient.EXPECT().EthAccounts(ctx).Return([]common.Address{}, nil)
	err := sut.Initialize(ctx)
	require.Error(t, err)
}

func TestInitialize(t *testing.T) {
	sut := Web3SignerSign{}
	ctx := context.TODO()
	err := sut.Initialize(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "client is nil")
	sut = Web3SignerSign{
		client: mocks.NewWeb3SignerClienter(t),
	}
	err = sut.Initialize(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "logger is nil")
}

func TestFailsSetAddressToUse(t *testing.T) {
	mockWeb3SignerClient := mocks.NewWeb3SignerClienter(t)
	ctx := context.TODO()
	logger := log.WithFields("test", "test")
	sut := NewWeb3SignerSign("name", logger, mockWeb3SignerClient, common.Address{})
	mockWeb3SignerClient.EXPECT().EthAccounts(ctx).Return([]common.Address{
		common.HexToAddress("0x1234"),
	}, nil)
	mockWeb3SignerClient.EXPECT().SignHash(ctx, common.HexToAddress("0x1234"), mock.Anything).Return([]byte{}, nil).Once()
	err := sut.Initialize(ctx)
	require.NoError(t, err)
	signData := []byte{0x01, 0x02, 0x03}
	mockWeb3SignerClient.EXPECT().SignHash(ctx, mock.Anything, mock.Anything).Return(signData, nil)
	sign, err := sut.SignHash(ctx, common.HexToHash("0x1234"))
	require.NoError(t, err)
	require.NotNil(t, sign)
	require.Equal(t, signData, sign)
}
