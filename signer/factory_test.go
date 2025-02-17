package signer

import (
	"context"
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/require"
)

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

	t.Run("wrong web3singer config", func(t *testing.T) {
		sut, err := NewSigner("test", logger, ctx, SignerConfig{
			Method: MethodWeb3Signer,
			Config: map[string]interface{}{
				FieldAddress: 1234,
			},
		})
		require.Error(t, err)
		require.Nil(t, sut)
	})

	t.Run("wrong web3singer config2", func(t *testing.T) {
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

	t.Run("wrong web3singer config3", func(t *testing.T) {
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

	t.Run("wrong web3singer missing URL", func(t *testing.T) {
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

	t.Run("web3singer config", func(t *testing.T) {
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
