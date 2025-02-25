package signer

import (
	"context"
	"fmt"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestSignerExploratory(t *testing.T) {
	t.Skip("This test is for exploratory purposes only")
	logger := log.WithFields("test", "test")
	ctx := context.TODO()
	localConfig := SignerConfig{
		Method: MethodLocal,
		Config: map[string]interface{}{
			FieldPath:     "../tmp/aggkit/local_config/sequencer.keystore",
			FieldPassword: "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m",
		},
	}

	local, err := NewSigner("test-local", logger, ctx, localConfig)
	require.NoError(t, err)
	require.NotNil(t, local)
	require.NoError(t, local.Initialize(ctx))
	require.NotNil(t, local.PublicAddress())
	hash := common.Hash{}
	sign, err := local.SignHash(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, sign)
	require.Equal(t, 65, len(sign))
	fmt.Print(sign)
	w3sConfig := SignerConfig{
		Method: MethodWeb3Signer,
		Config: map[string]interface{}{
			FieldURL: "http://localhost:9000",
		},
	}
	web3signer, err := NewSigner("test-w3s", logger, ctx, w3sConfig)
	require.NoError(t, err)
	require.NotNil(t, web3signer)
	require.NoError(t, web3signer.Initialize(ctx))
	require.NotNil(t, web3signer.PublicAddress())
	signW3s, err := web3signer.SignHash(ctx, hash)
	require.NoError(t, err)
	require.NotNil(t, signW3s)
	require.Equal(t, 65, len(signW3s))
	fmt.Print(signW3s)
}

func TestNewSigner(t *testing.T) {
	logger := log.WithFields("test", "test")
	ctx := context.TODO()
	t.Run("unknown signer method", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{Method: "unknown_method"})
		require.Error(t, err)
		require.Nil(t, sut)
	})
	t.Run("empty method is local", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{})
		require.NoError(t, err)
		require.NotNil(t, sut)
		require.Contains(t, sut.String(), MethodLocal)
	})

	t.Run("wrong local config", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Config: map[string]interface{}{
				FieldPath: 1234,
			},
		})
		require.Error(t, err)
		require.Nil(t, sut)
	})

	t.Run("wrong local config", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Config: map[string]interface{}{
				FieldPath: 1234,
			},
		})
		require.Error(t, err)
		require.Nil(t, sut)
	})

	t.Run("wrong web3signer config", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Method: MethodWeb3Signer,
			Config: map[string]interface{}{
				FieldAddress: 1234,
			},
		})
		require.Error(t, err)
		require.Nil(t, sut)
	})

	t.Run("wrong web3signer config2", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Method: MethodWeb3Signer,
			Config: map[string]interface{}{
				FieldAddress: "NOTHEXA",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), FieldAddress)
		require.Nil(t, sut)
	})

	t.Run("wrong web3signer config3", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Method: MethodWeb3Signer,
			Config: map[string]interface{}{
				FieldAddress: "0x71C7656EC7ab88b098defB751B7401B5f6d8976F",
				FieldURL:     1234,
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), FieldURL)
		require.Nil(t, sut)
	})

	t.Run("wrong web3signer missing URL", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Method: MethodWeb3Signer,
			Config: map[string]interface{}{
				FieldAddress: "0x71C7656EC7ab88b098defB751B7401B5f6d8976F",
			},
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), FieldURL)
		require.Nil(t, sut)
	})

	t.Run("web3signer config", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Method: MethodWeb3Signer,
			Config: map[string]interface{}{
				FieldAddress: "0x71C7656EC7ab88b098defB751B7401B5f6d8976F",
				FieldURL:     "http://localhost:9001",
			},
		})
		require.NoError(t, err)
		require.NotNil(t, sut)
	})
}
