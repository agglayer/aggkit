//nolint:lll
package config

// This values doesnt have a default value because depend on the
// environment / deployment
const DefaultMandatoryVars = `
L1URL = "http://localhost:8545"
L2URL = "http://localhost:8123"
OpNodeURL = "http://localhost:8080"


AggLayerURL = "https://agglayer-dev.polygon.technology"
AggchainProofURL = "http://localhost:5576"


ForkId = 9
ContractVersions = "elderberry"
IsValidiumMode = false
NetworkID = 1

L2Coinbase = "0xfa3b44587990f97ba8b6ba7e230a5f0e95d14b3d"
SequencerPrivateKeyPath = "/app/sequencer.keystore"
SequencerPrivateKeyPassword = "test"

WitnessURL = "http://localhost:8123"

# Who send Proof to L1? AggLayer addr, or aggregator addr?
SenderProofToL1Addr = "0x0000000000000000000000000000000000000000"
polygonBridgeAddr = "0x0000000000000000000000000000000000000000"


# This values can be override directly from genesis.json
rollupCreationBlockNumber = 0
rollupManagerCreationBlockNumber = 0
genesisBlockNumber = 0
[L1Config]
	chainId = 0
	polygonZkEVMGlobalExitRootAddress = "0x0000000000000000000000000000000000000000"
	polygonRollupManagerAddress = "0x0000000000000000000000000000000000000000"
	polTokenAddress = "0x0000000000000000000000000000000000000000"
	polygonZkEVMAddress = "0x0000000000000000000000000000000000000000"
	AggchainFEPAddr = "0x0000000000000000000000000000000000000000"


[L2Config]
	GlobalExitRootAddr = "0x0000000000000000000000000000000000000000"

`

// This doesnt below to config, but are the vars used
// to avoid repetition in config-files
const DefaultVars = `
PathRWData = "/tmp/aggkit"
L1URLSyncChunkSize = 100
RequireStorageContentCompatibility = true
L2RPC = "{ Mode= \"basic\", URL= \"{{L2URL}}\" }"
GenerateAggchainProofTimeout = "1h"
`

// DefaultValues is the default configuration
const DefaultValues = `
ForkUpgradeBatchNumber = 0
ForkUpgradeNewForkId = 0
AggsenderPrivateKey = "{Method =  \"local\", Path = \"{{SequencerPrivateKeyPath}}\", Password = \"{{SequencerPrivateKeyPassword}}\"}"

[Log]
Environment = "development" # "production" or "development"
Level = "info"
Outputs = ["stderr"]

[Etherman]
	URL = "{{L1URL}}"
	ForkIDChunkSize = {{L1URLSyncChunkSize}}
	[Etherman.EthermanConfig]
		URL = "{{L1URL}}"
		MultiGasProvider = false
		L1ChainID = {{NetworkConfig.L1.L1ChainID}}
		HTTPHeaders = []
		[Etherman.EthermanConfig.Etherscan]
			ApiKey = ""
			Url = "https://api.etherscan.io/api?module=gastracker&action=gasoracle&apikey="

[Common]
NetworkID = {{NetworkID}}
IsValidiumMode = {{IsValidiumMode}}
ContractVersions = "{{ContractVersions}}"
L2RPC = {{L2RPC}}

[ReorgDetectorL1]
DBPath = "{{PathRWData}}/reorgdetectorl1.sqlite"
FinalizedBlock = "FinalizedBlock"

[ReorgDetectorL2]
DBPath = "{{PathRWData}}/reorgdetectorl2.sqlite"
FinalizedBlock = "LatestBlock"

[L1InfoTreeSync]
DBPath = "{{PathRWData}}/L1InfoTreeSync.sqlite"
GlobalExitRootAddr = "{{NetworkConfig.L1.GlobalExitRootManagerAddr}}"
RollupManagerAddr = "{{NetworkConfig.L1.RollupManagerAddr}}"
SyncBlockChunkSize = 100
BlockFinality = "LatestBlock"
URLRPCL1 = "{{L1URL}}"
WaitForNewBlocksPeriod = "100ms"
InitialBlock = {{genesisBlockNumber}}
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}

[AggOracle]
TargetChainType = "EVM"
URLRPCL1 = "{{L1URL}}"
BlockFinality = "FinalizedBlock"
WaitPeriodNextGER = "10s"
	[AggOracle.EVMSender]
		GlobalExitRootL2 = "{{L2Config.GlobalExitRootAddr}}"
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

[ClaimSponsor]
DBPath = "{{PathRWData}}/claimsponsor.sqlite"
Enabled = false
SenderAddr = "0xfa3b44587990f97ba8b6ba7e230a5f0e95d14b3d"
BridgeAddrL2 = "0xB7098a13a48EcE087d3DA15b2D28eCE0f89819B8"
MaxGas = 200000
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitTxToBeMinedPeriod = "3s"
WaitOnEmptyQueue = "3s"
GasOffset = 0
	[ClaimSponsor.EthTxManager]
		FrequencyToMonitorTxs = "1s"
		WaitTxToBeMined = "2s"
		GetReceiptMaxTime = "250ms"
		GetReceiptWaitInterval = "1s"
		PrivateKeys = [
			{Path = "/etc/aggkit/claimtxmanager.keystore", Password = "pSnv6Dh5s9ahuzGzH9RoCDrKAMddaX3m"},
		]
		ForcedGas = 0
		GasPriceMarginFactor = 1
		MaxGasPriceLimit = 0
		StoragePath = "{{PathRWData}}/ethtxmanager-claimsponsor.sqlite"
		ReadPendingL1Txs = false
		SafeStatusL1NumberOfBlocks = 5
		FinalizedStatusL1NumberOfBlocks = 10
			[ClaimSponsor.EthTxManager.Etherman]
				URL = "{{L2URL}}"
				MultiGasProvider = false
				# L1ChainID = 0 indicates it will be set at runtime
				# This field should be populated with L2ChainID 
				L1ChainID = 0
				HTTPHeaders = []

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

[LastGERSync]
DBPath = "{{PathRWData}}/lastgersync.sqlite"
BlockFinality = "LatestBlock"
InitialBlockNum = 0
GlobalExitRootL2Addr = "{{L2Config.GlobalExitRootAddr}}"
RetryAfterErrorPeriod = "1s"
MaxRetryAttemptsAfterError = -1
WaitForNewBlocksPeriod = "1s"
DownloadBufferSize = 100
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}
SyncMode = "FEP"

[NetworkConfig.L1]
L1ChainID = {{L1Config.chainId}}
PolAddr = "{{L1Config.polTokenAddress}}"
ZkEVMAddr = "{{L1Config.polygonZkEVMAddress}}"
RollupManagerAddr = "{{L1Config.polygonRollupManagerAddress}}"
GlobalExitRootManagerAddr = "{{L1Config.polygonZkEVMGlobalExitRootAddress}}"


[AggSender]
StoragePath = "{{PathRWData}}/aggsender.sqlite"
AggsenderPrivateKey = {{AggsenderPrivateKey}}
BlockFinality = "LatestBlock"
EpochNotificationPercentage = 50
MaxRetriesStoreCertificate = 3
DelayBeetweenRetries = "60s"
KeepCertificatesHistory = true
# MaxSize of the certificate to 8Mb
MaxCertSize = 8388608
DryRun = false
EnableRPC = true
# PessimisticProof or AggchainProver
Mode = "PessimisticProof"
CheckStatusCertificateInterval = "5m"
RetryCertAfterInError = false
GlobalExitRootL2 = "{{L2Config.GlobalExitRootAddr}}"
SovereignRollupAddr = "{{L1Config.polygonZkEVMAddress}}"
RequireStorageContentCompatibility = {{RequireStorageContentCompatibility}}
RequireNoFEPBlockGap = true
	[AggSender.AgglayerClient]
		URL = "{{AggLayerURL}}"
		MinConnectTimeout = "5s"
		RequestTimeout = "300s" 
		UseTLS = false
		[AggSender.AgglayerClient.Retry]
			InitialBackoff = "1s"
			MaxBackoff = "10s"
			BackoffMultiplier = 2.0
			MaxAttempts = 16
	[AggSender.AggkitProverClient]
		URL = "{{AggchainProofURL}}"
		MinConnectTimeout = "5s"
		RequestTimeout = "{{GenerateAggchainProofTimeout}}"
		UseTLS = false
	[AggSender.MaxSubmitCertificateRate]
		NumRequests = 20
		Interval = "1h"
	[AggSender.OptimisticModeConfig]
		SovereignRollupAddr = "{{AggSender.SovereignRollupAddr}}"
		# By default use the same key that aggsender signs certs
		TrustedSequencerKey = {{AggSender.AggsenderPrivateKey}}
		OpNodeURL = "{{OpNodeURL}}"
		# TODO: For now set it to false, until it gets fixed on the contracts deployment end
		RequireKeyMatchTrustedSequencer = false
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
		[AggchainProofGen.AggkitProverClient.Retry]
			InitialBackoff = "1s"
			MaxBackoff = "10s"
			BackoffMultiplier = 2.0
			MaxAttempts = 1

[Profiling]
ProfilingHost = "localhost"
ProfilingPort = 6060
ProfilingEnabled = false
`
