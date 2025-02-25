package signer

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"

	"github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

// To keep compatibility with previous version, an empty config file
// meant there is no privateKey (nil), so the idea is keep the same behavior
func TestNewKeyStoreFileConfigEmpty(t *testing.T) {
	cfg, err := NewLocalConfig(SignerConfig{})
	require.NoError(t, err)
	require.Equal(t, "", cfg.Path)
	require.Equal(t, "", cfg.Password)
}

func TestNewLocalSignerConfig(t *testing.T) {
	cfg := NewLocalSignerConfig("/app/sequencer.keystore", "test")
	require.Equal(t, MethodLocal, cfg.Method)
	require.Equal(t, "/app/sequencer.keystore", cfg.Config[FieldPath])
	require.Equal(t, "test", cfg.Config[FieldPassword])

	localCfg, err := NewLocalConfig(cfg)
	require.NoError(t, err)
	require.Equal(t, "/app/sequencer.keystore", localCfg.Path)
	require.Equal(t, "test", localCfg.Password)
}

func TestNewLocalSignerConfigWrongData(t *testing.T) {
	cfg := NewLocalSignerConfig("/app/sequencer.keystore", "test")
	cfg.Config[FieldPath] = 123
	_, err := NewLocalConfig(cfg)
	require.Error(t, err)
}

func TestNewLocalSign(t *testing.T) {
	sut := NewLocalSign("name", nil, types.KeystoreFileConfig{})
	require.NotNil(t, sut)
	require.Equal(t, "name", sut.name)
	require.Nil(t, sut.logger)
	require.Equal(t, "", sut.file.Path)
}

func TestNewLocalSignFromPrivateKey(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	sut := NewLocalSignFromPrivateKey("name", nil, privateKey)
	require.NotNil(t, sut)
	err = sut.Initialize(context.Background())
	require.NoError(t, err)
	pubAddr := sut.PublicAddress()
	require.NotNil(t, pubAddr)
	str := sut.String()
	require.NotEmpty(t, str)
	_, err = sut.SignHash(context.Background(), common.Hash{})
	require.NoError(t, err)
}

func TestNewLocalSignEmpty(t *testing.T) {
	logger := log.WithFields("test", "test")
	sut := NewLocalSign("name", logger, types.KeystoreFileConfig{})
	err := sut.Initialize(context.Background())
	require.NoError(t, err)
	pubAddr := sut.PublicAddress()
	require.Equal(t, common.Address{}, pubAddr)
	_, err = sut.SignHash(context.Background(), common.Hash{})
	require.Error(t, err)
	require.NotEmpty(t, sut.String())
}
