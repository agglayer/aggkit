package config

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/agglayer/aggkit/etherman"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v2"
)

func TestLExploratorySetConfigFlag(t *testing.T) {
	value := []string{"config.json", "another_config.json"}
	ctx := newCliContextConfigFlag(t, value...)
	configFilePath := ctx.StringSlice(FlagCfg)
	require.Equal(t, value, configFilePath)
}

func TestLoadDefaultConfig(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "ut_config")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write([]byte(DefaultMandatoryVars))
	require.NoError(t, err)
	ctx := newCliContextConfigFlag(t, tmpFile.Name())
	cfg, err := Load(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, etherman.FinalizedBlock, cfg.ReorgDetectorL1.FinalizedBlock)
	require.Equal(t, cfg.AggSender.MaxSubmitCertificateRate.NumRequests, 20)
	require.Equal(t, cfg.AggSender.MaxSubmitCertificateRate.Interval.Duration, time.Hour)
	require.Equal(t, cfg.AggSender.RequireNoFEPBlockGap, true)
	require.Equal(t, cfg.AggSender.OptimisticModeConfig.SovereignRollupAddr, cfg.AggSender.SovereignRollupAddr)
	require.Equal(t, cfg.AggSender.OptimisticModeConfig.TrustedSequencerKey, cfg.AggSender.AggsenderPrivateKey)
	require.Equal(t, cfg.AggSender.OptimisticModeConfig.OpNodeURL, "http://localhost:8080")
	require.Equal(t, cfg.L1InfoTreeSync.RequireStorageContentCompatibility, true)
	require.Equal(t, ethermanconfig.RPCClientConfig{Mode: ethermanconfig.RPCModeBasic, URL: "http://localhost:8123"}, cfg.Common.L2RPC)
	require.Equal(t, cfg.Profiling.ProfilingEnabled, false)
	require.Equal(t, cfg.Profiling.ProfilingHost, "localhost")
	require.Equal(t, cfg.Profiling.ProfilingPort, 6060)
	t.Logf("cfg.AggSender.OptimisticModeConfig.TrustedSequencerKey: %+v", cfg.AggSender.OptimisticModeConfig.TrustedSequencerKey)
}

func TestLoadConfigWithSaveConfigFile(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "ut_config")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write([]byte(DefaultVars + "\n"))
	require.NoError(t, err)
	fmt.Printf("file: %s\n", tmpFile.Name())
	ctx := newCliContextConfigFlag(t, tmpFile.Name())
	dir, err := os.MkdirTemp("", "ut_test_save_config")
	require.NoError(t, err)
	defer os.RemoveAll(dir)

	err = ctx.Set(FlagSaveConfigPath, dir)
	require.NoError(t, err)
	cfg, err := Load(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	_, err = os.Stat(dir + "/" + SaveConfigFileName)
	require.NoError(t, err)
}

func TestLoadConfigWithInvalidFilename(t *testing.T) {
	ctx := newCliContextConfigFlag(t, "invalid_file")
	cfg, err := Load(ctx)
	require.Error(t, err)
	require.Nil(t, cfg)
}

func newCliContextConfigFlag(t *testing.T, values ...string) *cli.Context {
	t.Helper()
	flagSet := flag.NewFlagSet("test", flag.ContinueOnError)
	var configFilePaths cli.StringSlice
	flagSet.Var(&configFilePaths, FlagCfg, "")
	flagSet.Bool(FlagAllowDeprecatedFields, false, "")
	flagSet.String(FlagSaveConfigPath, "", "")
	for _, value := range values {
		err := flagSet.Parse([]string{"--" + FlagCfg, value})
		require.NoError(t, err)
	}
	return cli.NewContext(nil, flagSet, nil)
}

func TestLoadConfigWithDeprecatedFields(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "ut_config")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write([]byte(`
	[Common]
	IsValidiumMode = true
	Translator = ""

	[L1Config]
	polygonBridgeAddr = "0x0000000000000000000000000000000000000000"

	[AggSender]
	BridgeMetaDataAsHash = true
	AggLayerUrl = "https://localhost:5575"
	UseAgglayerTLS = true
	AggchainProofURL = "http://localhost:5576"
	UseAggkitProverTLS = true
	GenerateAggchainProofTimeout = "1h"

	[AggchainProofGen]
	AggchainProofUrl = "http://localhost:5577"
	GenerateAggchainProofTimeout = "1h"
`))
	require.NoError(t, err)
	ctx := newCliContextConfigFlag(t, tmpFile.Name())
	_, err = Load(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), bridgeMetadataAsHashHint)
	require.Contains(t, err.Error(), bridgeAddrSetOnWrongSection)
	require.Contains(t, err.Error(), aggsenderAgglayerClientHint)
	require.Contains(t, err.Error(), aggsenderAggkitProverClientHint)
	require.Contains(t, err.Error(), aggsenderAggkitProverClientHint)
	require.Contains(t, err.Error(), aggsenderAgglayerClientUseTLSHint)
	require.Contains(t, err.Error(), aggsenderAggkitProverClientUseTLSHint)
	require.Contains(t, err.Error(), aggsenderUseRequestTimeoutHint)
	require.Contains(t, err.Error(), aggchainProofGenUseRequestTimeoutHint)
	require.Contains(t, err.Error(), translatorDeprecatedHint)
	require.Contains(t, err.Error(), isValidiumModeDeprecatedHint)
}
