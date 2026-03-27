package config

import (
	"flag"
	"fmt"
	"os"
	"testing"
	"time"

	aggsendertypes "github.com/agglayer/aggkit/aggsender/types"
	aggkitcommon "github.com/agglayer/aggkit/common"
	"github.com/agglayer/aggkit/config/types"
	ethermanconfig "github.com/agglayer/aggkit/etherman/config"
	"github.com/agglayer/aggkit/multidownloader"
	aggkittypes "github.com/agglayer/aggkit/types"
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
	AggsenderrollupAddr := "0x1cE29253F94Ae8564c182AD760C18FC2adce77c1"
	os.Setenv("CDK_AGGSENDER_ROLLUPMANAGERADDR", AggsenderrollupAddr)
	// Check issue https://github.com/agglayer/aggkit/issues/751
	os.Setenv("CDK_VALIDATOR_LERQUERIERCONFIG_ROLLUPMANAGERADDR", AggsenderrollupAddr)
	ctx := newCliContextConfigFlag(t, tmpFile.Name())
	cfg, err := Load(ctx)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	require.Equal(t, aggkittypes.FinalizedBlock, cfg.ReorgDetectorL1.FinalizedBlock)
	require.Equal(t, AggsenderrollupAddr, cfg.AggSender.RollupManagerAddr.String())
	require.Equal(t, cfg.AggSender.AgglayerClient.Cached, false)
	require.Equal(t, cfg.AggSender.AgglayerClient.GRPC.RequestTimeout.Duration, 300*time.Second)
	require.Equal(t, cfg.AggSender.AgglayerClient.GRPC.Retry.MaxAttempts, 20)
	require.Equal(t, cfg.AggSender.OptimisticModeConfig.SovereignRollupAddr, cfg.AggSender.SovereignRollupAddr)
	require.Equal(t, cfg.AggSender.OptimisticModeConfig.TrustedSequencerKey, cfg.AggSender.AggsenderPrivateKey)
	require.Equal(t, cfg.AggSender.OptimisticModeConfig.OpNodeURL, "")
	require.Equal(t, cfg.AggSender.RetriesToBuildAndSendCertificate.String(),
		"RetryPolicyConfig{Mode: delays, Config: RetryDelaysConfig{Delays: [1m0s 1m0s 2m0s 5m0s 5m0s 8m0s], MaxRetries: 6}}")
	require.Equal(t, cfg.L1InfoTreeSync.RequireStorageContentCompatibility, true)
	require.Equal(t, ethermanconfig.L2RPCClientConfig{
		RPCClientConfig: ethermanconfig.RPCClientConfig{
			URL: "http://localhost:8123",
			RetryPolicyGenericConfig: aggkitcommon.RetryPolicyGenericConfig{
				Mode:              aggkitcommon.RetryConfigModeBackoff,
				MaxRetries:        5,
				InitialBackoff:    types.NewDuration(2 * time.Second),
				MaxBackoff:        types.NewDuration(10 * time.Second),
				BackoffMultiplier: 2.0,
			},
		},
		Mode: ethermanconfig.RPCModeBasic,
	}, cfg.Common.L2RPC)
	require.Equal(t, cfg.Profiling.ProfilingEnabled, false)
	require.Equal(t, cfg.Profiling.ProfilingHost, "localhost")
	require.Equal(t, cfg.Profiling.ProfilingPort, 6060)
	require.Equal(t, cfg.Validator.EnableRPC, false)
	require.Equal(t, cfg.Validator.ServerConfig.EnableReflection, true)
	require.Equal(t, cfg.Validator.AgglayerClient.Cached, true)
	require.Equal(t, cfg.Validator.AgglayerClient.ConfigurationCache.Capacity, uint64(100))
	require.Equal(t, cfg.Validator.AgglayerClient.GRPC.MinConnectTimeout, cfg.AggSender.AgglayerClient.GRPC.MinConnectTimeout)
	require.Equal(t, cfg.Validator.AgglayerClient.GRPC.Retry.MaxAttempts, cfg.AggSender.AgglayerClient.GRPC.Retry.MaxAttempts)
	require.Equal(t, cfg.AggSender.RollupManagerAddr, cfg.Validator.LerQuerier.RollupManagerAddr)
	require.Equal(t, uint64(0), cfg.AggSender.UnsetClaimsMaxLogBlockRange)
	require.Equal(t, cfg.AggSender.UnsetClaimsMaxLogBlockRange, cfg.Validator.UnsetClaimsMaxLogBlockRange)
	require.Equal(t, aggsendertypes.AutoMode, cfg.AggSender.Mode)
	require.Equal(t, aggsendertypes.AutoMode, cfg.Validator.Mode)
	require.Equal(t, cfg.AggSender.StorageRetainCertificatesPolicy.String(), "retain all certificates, keep history: true")
	require.Equal(t, multidownloader.NewConfigDefault("l1", ""), cfg.L1Multidownloader)
	cfgL2Multidownloader := multidownloader.NewConfigDefault("l2", "")
	cfgL2Multidownloader.BlockFinality = aggkittypes.LatestBlock
	require.Equal(t, cfgL2Multidownloader, cfg.L2Multidownloader)
	require.Nil(t, cfg.L2NetworkConfig.InitialLER)
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

func TestL2NetworkConfigInitialLER(t *testing.T) {
	specificHash := "0xaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccddaabbccdd"
	zeroHash := "0x0000000000000000000000000000000000000000000000000000000000000000"

	tests := []struct {
		name          string
		toml          string
		expectNil     bool
		expectedValue string
	}{
		{
			name: "InitialLER set to a specific hash",
			toml: `
[L2NetworkConfig]
InitialLER = "` + specificHash + `"
`,
			expectNil:     false,
			expectedValue: specificHash,
		},
		{
			name: "InitialLER set to zero hash is valid and not nil",
			toml: `
[L2NetworkConfig]
InitialLER = "` + zeroHash + `"
`,
			expectNil:     false,
			expectedValue: zeroHash,
		},
		{
			name:      "InitialLER not set returns nil",
			toml:      ``,
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadFileFromString(tt.toml, ConfigType)
			require.NoError(t, err)
			require.NotNil(t, cfg)

			if tt.expectNil {
				require.Nil(t, cfg.L2NetworkConfig.InitialLER)
			} else {
				require.NotNil(t, cfg.L2NetworkConfig.InitialLER)
				require.Equal(t, tt.expectedValue, cfg.L2NetworkConfig.InitialLER.Hex())
			}
		})
	}
}

func TestLoadConfigWithDeprecatedFields(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "ut_config")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.Write([]byte(`

	polygonBridgeAddr = "0x0000000000000000000000000000000000000000"
	[Common]
	IsValidiumMode = true
	ContractVersions="banana"
	Translator = ""

	[L1NetworkConfig]
	URL = "http://localhost:8545"

	[L1Config]
	polygonBridgeAddr = "0x0000000000000000000000000000000000000000"

	[AggSender]
	BridgeMetaDataAsHash = true
	AggLayerUrl = "https://localhost:5575"
	UseAgglayerTLS = true
	AggchainProofURL = "http://localhost:5576"
	UseAggkitProverTLS = true
	GenerateAggchainProofTimeout = "1h"
	DelayBeetweenRetries = "1s"
	RequireValidatorCall = true
	[AggSender.MaxSubmitCertificateRate]
		NumRequests = 20
		Interval = "1h"

	[AggchainProofGen]
	AggchainProofUrl = "http://localhost:5577"
	GenerateAggchainProofTimeout = "1h"

	[NetworkConfig.L1]
	URL="{{L1URL}}"
	PolAddr="{{L1Config.polTokenAddress}}"
	ZkEVMAddr="{{L1Config.polygonZkEVMAddress}}"

	[Etherman]
	URL = "{{L1URL}}"
	[Etherman.EthermanConfig]
		URL = "{{L1URL}}"
		MultiGasProvider = false
		L1ChainID = {{L1NetworkConfig.L1ChainID}}
		HTTPHeaders = []
		[Etherman.EthermanConfig.Etherscan]
			ApiKey = ""
			Url = "https://api.etherscan.io/api?module=gastracker&action=gasoracle&apikey="

	[AggOracle]
	BlockFinality = "FinalizedBlock"
	URLRPCL1 = "http://localhost:8545"

	[L1InfoTreeSync]
	URLRPCL1 = "http://localhost:8545"

	[LastGERSync]
	SyncMode = "Legacy"
	DBPath = "{{PathRWData}}/l2gersync.sqlite"
`))
	require.NoError(t, err)
	ctx := newCliContextConfigFlag(t, tmpFile.Name())
	_, err = Load(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, bridgeMetadataAsHashHint)
	require.ErrorContains(t, err, bridgeAddrSetOnWrongSection)
	require.ErrorContains(t, err, aggsenderAgglayerClientHint)
	require.ErrorContains(t, err, aggsenderAggkitProverClientHint)
	require.ErrorContains(t, err, aggsenderAggkitProverClientHint)
	require.ErrorContains(t, err, aggsenderAgglayerClientUseTLSHint)
	require.ErrorContains(t, err, aggsenderAggkitProverClientUseTLSHint)
	require.ErrorContains(t, err, aggsenderUseRequestTimeoutHint)
	require.ErrorContains(t, err, aggchainProofGenUseRequestTimeoutHint)
	require.ErrorContains(t, err, contractVersionsDeprecatedHint)
	require.ErrorContains(t, err, isValidiumModeDeprecatedHint)
	require.ErrorContains(t, err, translatorDeprecatedHint)
	require.ErrorContains(t, err, ethermanDeprecatedHint)
	require.ErrorContains(t, err, networkConfigDeprecatedHint)
	require.ErrorContains(t, err, l1NetworkConfigUsePolTokenAddrHint)
	require.ErrorContains(t, err, l1NetworkConfigUseRollupAddrHint)
	require.ErrorContains(t, err, delayBetweenRetriesHint)
	require.ErrorContains(t, err, aggOracleBlockFinalityDeprecated)
	require.ErrorContains(t, err, lastGERSyncDeprecatedHint)
	require.ErrorContains(t, err, lastGERSyncSyncModeDeprecatedHint)
	require.ErrorContains(t, err, l1NetworkConfigURLDeprecatedHint)
	require.ErrorContains(t, err, requireValidatorCallDeprecatedHint)
	require.ErrorContains(t, err, maxSubmitCertificateRateDeprecatedHint)
	require.ErrorContains(t, err, urlRPCL1DeprecatedHint)
}
