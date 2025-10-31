//nolint:lll
package config

// This values doesnt have a default value because depend on the
// environment / deployment
const DefaultMandatoryVars = `
L1URL = "http://localhost:8545"
L2URL = "http://localhost:8123"
OpNodeURL = ""

AggLayerURL = "https://agglayer-dev.polygon.technology"
AggchainProofURL = "http://localhost:5576"

SequencerPrivateKeyPath = "/etc/aggkit/sequencer.keystore"
SequencerPrivateKeyPassword = "test"

polygonBridgeAddr = "0x0000000000000000000000000000000000000000"

# This values can be override directly from genesis.json
rollupCreationBlockNumber = 0
rollupManagerCreationBlockNumber = 0
genesisBlockNumber = 0
[L1Config]
	URL = "{{L1URL}}"
	chainId = 0
	polygonZkEVMGlobalExitRootAddress = "0x0000000000000000000000000000000000000000"
	polygonRollupManagerAddress = "0x0000000000000000000000000000000000000000"
	polTokenAddress = "0x0000000000000000000000000000000000000000"
	polygonZkEVMAddress = "0x0000000000000000000000000000000000000000"

[L2Config]
	GlobalExitRootAddr = "0x0000000000000000000000000000000000000000"
	AggOracleCommitteeAddr = "0x0000000000000000000000000000000000000000"
`

// This doesnt below to config, but are the vars used
// to avoid repetition in config-files
const DefaultVars = `
PathRWData = "/tmp/aggkit"
RequireStorageContentCompatibility = true
GenerateAggchainProofTimeout = "1h"
# Default database query timeout
defaultDBQueryTimeout = "60s"
[L2RPC]
	Mode = "basic"
	URL = "{{L2URL}}"
	RetryMode = "backoff"
	MaxRetries = 5
	InitialBackoff = "2s"
	MaxBackoff = "10s"
	BackoffMultiplier = 2.0
`

// DefaultValues is the default configuration
const DefaultValues = `
AggsenderPrivateKey = "{Method =  \"local\", Path = \"{{SequencerPrivateKeyPath}}\", Password = \"{{SequencerPrivateKeyPassword}}\"}"

[Log]
Environment = "development" # "production" or "development"
Level = "info"
Outputs = ["stderr"]

[Common]
L2RPC = {{L2RPC}}

[L1NetworkConfig]
L1ChainID = {{L1Config.chainId}}
POLTokenAddr = "{{L1Config.polTokenAddress}}"
RollupAddr = "{{L1Config.polygonZkEVMAddress}}"
RollupManagerAddr = "{{L1Config.polygonRollupManagerAddress}}"
GlobalExitRootManagerAddr = "{{L1Config.polygonZkEVMGlobalExitRootAddress}}"
	[L1NetworkConfig.RPC]
		URL = "{{L1Config.URL}}"
		RetryMode = "backoff"
		MaxRetries = 5
		InitialBackoff = "2s"
		MaxBackoff = "10s"
		BackoffMultiplier = 2.0

[ReorgDetectorL1]
DBPath = "{{PathRWData}}/reorgdetectorl1.sqlite"
FinalizedBlock = "FinalizedBlock"

[ReorgDetectorL2]
DBPath = "{{PathRWData}}/reorgdetectorl2.sqlite"
FinalizedBlock = "LatestBlock"

[L1InfoTreeSync]
DBPath = "{{PathRWData}}/L1InfoTreeSync.sqlite"
GlobalExitRootAddr = "{{L1NetworkConfig.GlobalExitRootManagerAddr}}"
RollupManagerAddr = "{{L1NetworkConfig.RollupManagerAddr}}"
SyncBlockChunkSize = 100
URLRPCL1 = "{{L1URL}}"
WaitForNewBlocksPeriod = "100ms"
InitialBlock = {{genesisBlockNumber}}
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}

[AggOracle]
TargetChainType = "EVM"
URLRPCL1 = "{{L1URL}}"
WaitPeriodNextGER = "10s"
EnableAggOracleCommittee = false
	[AggOracle.EVMSender]
		GlobalExitRootL2 = "{{L2Config.GlobalExitRootAddr}}"
		AggOracleCommitteeAddr = "{{L2Config.AggOracleCommitteeAddr}}"
		GasOffset = 0
		WaitPeriodMonitorTx = "1s"
		[AggOracle.EVMSender.EthTxManager]
				FrequencyToMonitorTxs = "1s"
				WaitTxToBeMined = "2s"
				GetReceiptMaxTime = "250ms"
				GetReceiptWaitInterval = "1s"
				PrivateKeys = [
					{Method =  "local", Path = "/app/keystore/aggoracle.keystore", Password = "testonly"},
				]
				ForcedGas = 0
				GasPriceMarginFactor = 1
				MaxGasPriceLimit = 0
				StoragePath = "{{PathRWData}}/ethtxmanager-aggoracle.sqlite"
				ReadPendingL1Txs = false
				SafeStatusL1NumberOfBlocks = 5
				FinalizedStatusL1NumberOfBlocks = 10
				EstimateGasMaxRetries = 1
					[AggOracle.EVMSender.EthTxManager.Etherman]
						URL = "{{L2URL}}"
						MultiGasProvider = false
						# L1ChainID = 0 indicates it will be set at runtime
						# This field should be populated with L2ChainID
						L1ChainID = 0
						HTTPHeaders = []

[RPC]
Host = "0.0.0.0"
Port = 5576
ReadTimeout = "2s"
WriteTimeout = "2s"
MaxRequestsPerIPAndSecond = 10

[REST]
Host = "0.0.0.0"
Port = 5577
ReadTimeout = "2s"
WriteTimeout = "2s"
MaxRequestsPerIPAndSecond = 10

[BridgeL1Sync]
DBPath = "{{PathRWData}}/bridgel1sync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
BridgeAddr = "{{polygonBridgeAddr}}"
SyncBlockChunkSize = 100
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "3s"
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}
DBQueryTimeout = "{{defaultDBQueryTimeout}}"

[BridgeL2Sync]
DBPath = "{{PathRWData}}/bridgel2sync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
BridgeAddr = "{{polygonBridgeAddr}}"
SyncBlockChunkSize = 100
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "3s"
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}
DBQueryTimeout = "{{defaultDBQueryTimeout}}"

[L2GERSync]
DBPath = "{{PathRWData}}/l2gersync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
GlobalExitRootL2Addr = "{{L2Config.GlobalExitRootAddr}}"
SyncBlockChunkSize = 100
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "1s"
DownloadBufferSize = 100
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}

[AggSender]
StoragePath = "{{PathRWData}}/aggsender.sqlite"
CertificatesDir = "{{PathRWData}}/certificates/"
AggsenderPrivateKey = {{AggsenderPrivateKey}}
EpochNotificationPercentage = 50
MaxRetriesStoreCertificate = 3
DelayBetweenRetries = "30s"
# MaxSize of the certificate to 8Mb
MaxCertSize = 8388608
DryRun = false
EnableRPC = true
# PessimisticProof, AggchainProof or Auto
Mode = "Auto"
CheckStatusCertificateInterval = "5m"
RetryCertAfterInError = false
GlobalExitRootL2 = "{{L2Config.GlobalExitRootAddr}}"
SovereignRollupAddr = "{{L1Config.polygonZkEVMAddress}}"
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}
RequireNoFEPBlockGap = false
RequireOneBridgeInPPCertificate = false
RollupManagerAddr = "{{L1Config.polygonRollupManagerAddress}}"
RollupCreationBlockL1 = {{rollupCreationBlockNumber}}
MaxL2BlockNumber = 0
StopOnFinishedSendingAllCertificates = false
RequireCommitteeMembershipCheck = false
	[AggSender.RetriesToBuildAndSendCertificate]
		RetryMode = "delays"
		Delays = [ "1m", "1m", "2m", "5m", "5m", "8m" ]
		MaxRetries = 6 # 1+6 attempts, around 22m
	[AggSender.AgglayerClient]
		Cached = false
		[[AggSender.AgglayerClient.APIRateLimits]]
			MethodName = "SendCertificate"
			[AggSender.AgglayerClient.APIRateLimits.RateLimit]
				NumRequests = 15 # up to 15 requests per minute (avg ~1 request every 4s)
				Interval = "1m"
		[AggSender.AgglayerClient.ConfigurationCache]
			TTL = "5m"
			Capacity = 100
		[AggSender.AgglayerClient.GRPC]
			URL = "{{AggLayerURL}}"
			MinConnectTimeout = "5s"
			RequestTimeout = "300s"
			UseTLS = false
			[AggSender.AgglayerClient.GRPC.Retry]
				InitialBackoff = "1s"
				MaxBackoff = "10s"
				BackoffMultiplier = 2.0
				MaxAttempts = 20
	[AggSender.AggkitProverClient]
		URL = "{{AggchainProofURL}}"
		MinConnectTimeout = "5s"
		RequestTimeout = "{{GenerateAggchainProofTimeout}}"
		UseTLS = false
	[AggSender.OptimisticModeConfig]
		SovereignRollupAddr = "{{AggSender.SovereignRollupAddr}}"
		# By default use the same key that aggsender signs certs
		TrustedSequencerKey = {{AggSender.AggsenderPrivateKey}}
		OpNodeURL = "{{OpNodeURL}}"
		# TODO: For now set it to false, until it gets fixed on the contracts deployment end
		RequireKeyMatchTrustedSequencer = false
	[AggSender.ValidatorClient]
		URL = ""
		MinConnectTimeout = "5s"
		RequestTimeout = "30s"
		UseTLS = false
	# Overide a committee URL to point to a local service
	# [AggSender.CommitteeOverride]
	#	URLMapping = { "http://aggkit-001-aggsender-validator-001:5578" = "http://localhost:32954" }

	[AggSender.StorageRetainCertificatesPolicy]
		RetainCertificatesCount = 0 # 0 means keep all certificates
		KeepCertificatesHistory = true

[Prometheus]
Enabled = true
Host = "localhost"
Port = 9091

[AggchainProofGen]
SovereignRollupAddr = "{{L1Config.polygonZkEVMAddress}}"
GlobalExitRootL2 = "{{L2Config.GlobalExitRootAddr}}"
	[AggchainProofGen.AggkitProverClient]
		URL = "{{AggchainProofURL}}"
		MinConnectTimeout = "5s"
		UseTLS = false
		RequestTimeout = "{{GenerateAggchainProofTimeout}}"

[Profiling]
ProfilingHost = "localhost"
ProfilingPort = 6060
ProfilingEnabled = false

[Validator]
EnableRPC = false
# check SignerConfig in docs/common_config.md for more details
Signer = {{AggsenderPrivateKey}}
MaxCertSize = "{{AggSender.MaxCertSize}}"
MaxL2BlockNumber = "{{AggSender.MaxL2BlockNumber}}"
DelayBetweenRetries = "{{AggSender.DelayBetweenRetries}}"
# PessimisticProof, AggchainProof or Auto
Mode = "{{AggSender.Mode}}"
RequireCommitteeMembershipCheck = {{AggSender.RequireCommitteeMembershipCheck}}
[Validator.ServerConfig]
	Host = "0.0.0.0"
	Port = 5578
	EnableReflection = true
	MaxDecodingMessageSize = 26214400  # 25Mb
[Validator.LerQuerierConfig]
	RollupManagerAddr = "{{AggSender.RollupManagerAddr}}"
	RollupCreationBlockL1 = "{{AggSender.RollupCreationBlockL1}}"
[Validator.PPConfig]
	RequireOneBridgeInPPCertificate = "{{AggSender.RequireOneBridgeInPPCertificate}}"
[Validator.FEPConfig]
	SovereignRollupAddr = "{{AggSender.SovereignRollupAddr}}"
	RequireNoBlockGap = "{{AggSender.RequireNoFEPBlockGap}}"
	OpNodeURL = "{{OpNodeURL}}"
[Validator.AgglayerClient]
	Cached = true
	[Validator.AgglayerClient.ConfigurationCache]
		TTL = "5m"
		Capacity = 100
	[Validator.AgglayerClient.GRPC]
		URL = "{{AggSender.AgglayerClient.GRPC.URL}}"
		MinConnectTimeout = "{{AggSender.AgglayerClient.GRPC.MinConnectTimeout}}"
		RequestTimeout = "{{AggSender.AgglayerClient.GRPC.RequestTimeout}}"
		UseTLS = "{{AggSender.AgglayerClient.GRPC.UseTLS}}"
		[Validator.AgglayerClient.GRPC.Retry]
			InitialBackoff = "{{AggSender.AgglayerClient.GRPC.Retry.InitialBackoff}}"
			MaxBackoff = "{{AggSender.AgglayerClient.GRPC.Retry.MaxBackoff}}"
			BackoffMultiplier = "{{AggSender.AgglayerClient.GRPC.Retry.BackoffMultiplier}}"
			MaxAttempts = "{{AggSender.AgglayerClient.GRPC.Retry.MaxAttempts}}"
`
