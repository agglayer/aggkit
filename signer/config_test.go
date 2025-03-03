package signer

import (
	"bytes"
	"testing"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

const (
	configLocal = `
	Signer = {Method = "local", Path = "/app/sequencer.keystore", Password = "test"}
	`
	configWeb3Signer = `
	Signer = {Method = "web3signer", URL = "http://localhost:8545", Address = "0x1234567890abcdef"}
	`

	configEmpty = `
	Signer = {}
	`
)

func TestUnmarshalLocalConfig(t *testing.T) {
	cfg := struct {
		Signer SignerConfig `jsonschema:"omitempty" mapstructure:"Signer"`
	}{}
	viper.SetConfigType("toml")
	err := viper.ReadConfig(bytes.NewBuffer([]byte(configLocal)))
	require.NoError(t, err)
	decodeHooks := []viper.DecoderConfigOption{
		// this allows arrays to be decoded from env var separated by ",", example: MY_VAR="value1,value2,value3"
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(), mapstructure.StringToSliceHookFunc(","))),
	}
	err = viper.Unmarshal(&cfg, decodeHooks...)
	require.NoError(t, err)
	require.Equal(t, MethodLocal, cfg.Signer.Method)
	require.Equal(t, "/app/sequencer.keystore", cfg.Signer.Config["path"])
	require.Equal(t, "test", cfg.Signer.Config["password"])
}

func TestUnmarshalWeb3SignerConfig(t *testing.T) {
	cfg := struct {
		Signer SignerConfig `jsonschema:"omitempty" mapstructure:"Signer"`
	}{}
	viper.SetConfigType("toml")
	err := viper.ReadConfig(bytes.NewBuffer([]byte(configWeb3Signer)))
	require.NoError(t, err)
	decodeHooks := []viper.DecoderConfigOption{
		// this allows arrays to be decoded from env var separated by ",", example: MY_VAR="value1,value2,value3"
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(), mapstructure.StringToSliceHookFunc(","))),
	}
	err = viper.Unmarshal(&cfg, decodeHooks...)
	require.NoError(t, err)
	require.Equal(t, MethodWeb3Signer, cfg.Signer.Method)
	require.Equal(t, "http://localhost:8545", cfg.Signer.Config["url"])
	require.Equal(t, "0x1234567890abcdef", cfg.Signer.Config["address"])
}

func TestUnmarshalEmptyConfig(t *testing.T) {
	cfg := struct {
		Signer SignerConfig `jsonschema:"omitempty" mapstructure:"Signer"`
	}{}
	viper.SetConfigType("toml")
	err := viper.ReadConfig(bytes.NewBuffer([]byte(configEmpty)))
	require.NoError(t, err)
	decodeHooks := []viper.DecoderConfigOption{
		// this allows arrays to be decoded from env var separated by ",", example: MY_VAR="value1,value2,value3"
		viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
			mapstructure.TextUnmarshallerHookFunc(), mapstructure.StringToSliceHookFunc(","))),
	}
	err = viper.Unmarshal(&cfg, decodeHooks...)
	require.NoError(t, err)
	require.Equal(t, "", cfg.Signer.Method.String())
	require.Equal(t, 0, len(cfg.Signer.Config))
}
