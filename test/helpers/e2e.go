package helpers

import (
	"context"
	"math/big"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerger"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayergerl2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayermanager"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/aggoraclecommittee"
	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/bridgesync"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/test/contracts/proxy"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

const (
	rollupID              = uint32(1)
	syncBlockChunkSize    = 10
	defaultDBQueryTimeout = 60 * time.Second
)

type L2GERManagerContractType int

const (
	SovereignChainL2GERContract L2GERManagerContractType = iota
	LegacyL2GERContract
)

type AggoraclecommitteeConfig struct {
	EnableAggOracleCommittee bool
	Quorum                   uint64
}

// CommonEnvironment contains common setup results used in both L1 and L2 network setups.
type CommonEnvironment struct {
	SimBackend             *simulated.Backend
	GERAddr                common.Address
	AggOracleCommitteeAddr common.Address
	BridgeContract         *agglayerbridge.Agglayerbridge
	BridgeAddr             common.Address
	Auth                   *bind.TransactOpts
	ReorgDetector          *reorgdetector.ReorgDetector
	BridgeSync             *bridgesync.BridgeSync
}

// L1Environment contains simulated setup for L1 network.
type L1Environment struct {
	CommonEnvironment
	GERContract             *agglayerger.Agglayerger
	AgglayerManagerContract *agglayermanager.Agglayermanager
	InfoTreeSync            *l1infotreesync.L1InfoTreeSync
}

// L2Environment contains simulated setup for L2 network.
type L2Environment struct {
	CommonEnvironment
	GERManagerSovereignSC      *agglayergerl2.Agglayergerl2
	GERManagerLegacySC         *agglayerger.Agglayerger
	AggOracleCommitteeContract *aggoraclecommittee.Aggoraclecommittee
	AggoracleSender            aggoracle.ChainSender
	EthTxManagerMock           *EthTxManager
}

type EnvironmentConfig struct {
	L1RPCClient           aggkittypes.RPCClienter
	L2RPCClient           aggkittypes.RPCClienter
	L2GERManagerType      L2GERManagerContractType
	AggOracleCommitteeCfg AggoraclecommitteeConfig
}

func DefaultEnvironmentConfig(l2GERManagerType L2GERManagerContractType) *EnvironmentConfig {
	return &EnvironmentConfig{
		L1RPCClient:      &aggkittypes.NoopRPCClient{},
		L2RPCClient:      &aggkittypes.NoopRPCClient{},
		L2GERManagerType: l2GERManagerType,
		AggOracleCommitteeCfg: AggoraclecommitteeConfig{
			EnableAggOracleCommittee: false,
			Quorum:                   1,
		},
	}
}

// NewSimulatedEVMEnvironment creates a new simulated environment with EVM L1 and L2 chains.
func NewSimulatedEVMEnvironment(t *testing.T, cfg *EnvironmentConfig) (*L1Environment, *L2Environment) {
	t.Helper()

	// Setup L1
	l1Setup := L1Setup(t, cfg)

	// Setup L2 EVM
	l2Setup := L2Setup(t, cfg, l1Setup)

	return l1Setup, l2Setup
}

// L1Setup creates a new L1 environment.
func L1Setup(t *testing.T, cfg *EnvironmentConfig) *L1Environment {
	t.Helper()

	ctx := context.Background()

	// Simulated L1
	l1Client, authL1,
		gerL1Addr, gerL1Contract,
		bridgeL1Addr, bridgeL1Contract,
		agglayerManagerContract := NewSimulatedL1(t)

	// Reorg detector
	dbPathReorgDetectorL1 := path.Join(t.TempDir(), "ReorgDetectorL1.sqlite")
	rdL1, err := reorgdetector.New(l1Client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorgDetectorL1,
		CheckReorgsInterval: cfgtypes.Duration{Duration: time.Millisecond * 100}, //nolint:mnd
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	go rdL1.Start(ctx) //nolint:errcheck

	// L1 info tree sync
	dbPathL1InfoTreeSync := path.Join(t.TempDir(), "L1InfoTreeSync.sqlite")

	const (
		l1InfoTreeSyncerRetries   = 3
		l1InfoTreeSyncerRetryFreq = time.Millisecond * 100
	)

	l1InfoTreeSyncCfg := l1infotreesync.Config{
		DBPath:                             dbPathL1InfoTreeSync,
		InitialBlock:                       0,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		GlobalExitRootAddr:                 gerL1Addr,
		RollupManagerAddr:                  common.Address{},
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(l1InfoTreeSyncerRetryFreq),
		MaxRetryAttemptsAfterError:         l1InfoTreeSyncerRetries,
		RequireStorageContentCompatibility: true,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(time.Millisecond),
	}
	l1InfoTreeSync, err := l1infotreesync.New(
		ctx,
		l1InfoTreeSyncCfg,
		aggkittypes.LatestBlock,
		l1Client.Client(),
		l1infotreesync.FlagAllowWrongContractsAddrs,
		aggkittypes.SafeBlock,
	)
	require.NoError(t, err)

	go l1InfoTreeSync.Start(ctx)

	const (
		waitForNewBlocksPeriod = time.Millisecond * 10
		originNetwork          = 1
		initialBlock           = 0
		retryPeriod            = time.Millisecond * 30
		retriesCount           = 10
	)

	// Bridge sync
	testClient := NewTestClient(l1Client.Client(), WithRPCClienter(cfg.L1RPCClient))
	dbPathBridgeSyncL1 := path.Join(t.TempDir(), "BridgeSyncL1.sqlite")

	bridgeSyncCfg := bridgesync.Config{
		DBPath:                             dbPathBridgeSyncL1,
		BridgeAddr:                         bridgeL1Addr,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryPeriod),
		MaxRetryAttemptsAfterError:         retriesCount,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(defaultDBQueryTimeout),
	}
	bridgeL1Sync, err := bridgesync.NewL1(ctx, bridgeSyncCfg, rdL1, testClient, originNetwork, false)
	require.NoError(t, err)

	go bridgeL1Sync.Start(ctx)

	return &L1Environment{
		CommonEnvironment: CommonEnvironment{
			SimBackend:     l1Client,
			GERAddr:        gerL1Addr,
			BridgeContract: bridgeL1Contract,
			BridgeAddr:     bridgeL1Addr,
			Auth:           authL1,
			ReorgDetector:  rdL1,
			BridgeSync:     bridgeL1Sync,
		},
		GERContract:             gerL1Contract,
		AgglayerManagerContract: agglayerManagerContract,
		InfoTreeSync:            l1InfoTreeSync,
	}
}

// L2Setup creates a new L2 environment.
func L2Setup(t *testing.T, cfg *EnvironmentConfig, l1Setup *L1Environment) *L2Environment {
	t.Helper()

	ctx := context.Background()

	var (
		l2Client                   *simulated.Backend
		authL2                     *bind.TransactOpts
		gerL2Addr                  common.Address
		l2GERLegacySC              *agglayerger.Agglayerger
		l2GERSovereignChainSC      *agglayergerl2.Agglayergerl2
		bridgeL2Addr               common.Address
		bridgeL2Contract           *agglayerbridge.Agglayerbridge
		aggOracleCommitteeAddr     common.Address
		aggOracleCommitteeContract *aggoraclecommittee.Aggoraclecommittee
	)

	switch cfg.L2GERManagerType {
	case LegacyL2GERContract:
		l2Client, authL2, gerL2Addr, l2GERLegacySC,
			bridgeL2Addr, bridgeL2Contract = newSimulatedEVML2LegacyChain(t)

	case SovereignChainL2GERContract:
		l2Client, authL2, gerL2Addr, l2GERSovereignChainSC,
			bridgeL2Addr, bridgeL2Contract,
			aggOracleCommitteeAddr, aggOracleCommitteeContract = newSimulatedEVML2SovereignChain(t, cfg.AggOracleCommitteeCfg)

	default:
		require.Failf(t, "unknown L2 GER manager type provided", "(l2 ger manager type %d)", int(cfg.L2GERManagerType))
	}

	var (
		sender           aggoracle.ChainSender
		ethTxManagerMock *EthTxManager
		err              error
	)

	if cfg.L2GERManagerType == SovereignChainL2GERContract {
		l2ClientWithMutex := &SimulatedBackendWithMutex{
			Backend: l2Client,
			Mutex:   sync.RWMutex{},
		}
		ethTxManagerMock = NewEthTxManMock(t, l2ClientWithMutex, authL2)
		const (
			gerCheckFrequency     = time.Millisecond * 50
			gerInjectionFrequency = time.Millisecond * 20
		)

		evmSenderCfg := chaingersender.EVMConfig{
			GlobalExitRootL2Addr:   gerL2Addr,
			AggOracleCommitteeAddr: aggOracleCommitteeAddr,
			WaitPeriodMonitorTx:    cfgtypes.NewDuration(gerCheckFrequency),
		}
		l2GERManager, err := agglayergerl2.NewAgglayergerl2(
			gerL2Addr, l2Client.Client())
		if err != nil {
			log.Fatalf("failed to create binding for GER L2 manager (SC address: %s): %w", gerL2Addr, err)
		}
		sender, err = chaingersender.NewEVMChainGERSender(
			log.GetDefaultLogger(), evmSenderCfg, l2Client.Client(), l2GERManager,
			ethTxManagerMock, cfg.AggOracleCommitteeCfg.EnableAggOracleCommittee,
		)
		require.NoError(t, err)

		oracle, err := aggoracle.New(
			log.GetDefaultLogger(), sender,
			l1Setup.SimBackend.Client(), l1Setup.InfoTreeSync,
			gerInjectionFrequency,
		)
		require.NoError(t, err)
		go oracle.Start(ctx)
	}

	// Reorg detector
	dbPathReorgL2 := path.Join(t.TempDir(), "ReorgDetectorL2.sqlite")
	rdL2, err := reorgdetector.New(l2Client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorgL2,
		CheckReorgsInterval: cfgtypes.Duration{Duration: time.Millisecond * 100}, //nolint:mnd
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	},
		reorgdetector.L2,
	)
	require.NoError(t, err)
	go rdL2.Start(ctx) //nolint:errcheck

	// Bridge sync
	dbPathBridgeSyncL2 := path.Join(t.TempDir(), "BridgeSyncL2.sqlite")
	testClient := NewTestClient(l2Client.Client(), WithRPCClienter(cfg.L2RPCClient))

	const (
		waitForNewBlocksPeriod = 10 * time.Millisecond
		originNetwork          = 1
		initialBlock           = 0
		retryPeriod            = 50 * time.Millisecond
		retriesCount           = 100
	)

	bridgeSyncCfg := bridgesync.Config{
		DBPath:                             dbPathBridgeSyncL2,
		BridgeAddr:                         bridgeL2Addr,
		BlockFinality:                      aggkittypes.LatestBlock,
		SyncBlockChunkSize:                 syncBlockChunkSize,
		InitialBlockNum:                    initialBlock,
		WaitForNewBlocksPeriod:             cfgtypes.NewDuration(waitForNewBlocksPeriod),
		RetryAfterErrorPeriod:              cfgtypes.NewDuration(retryPeriod),
		MaxRetryAttemptsAfterError:         retriesCount,
		RequireStorageContentCompatibility: true,
		DBQueryTimeout:                     cfgtypes.NewDuration(defaultDBQueryTimeout),
	}
	bridgeL2Sync, err := bridgesync.NewL2(ctx, bridgeSyncCfg, rdL2, testClient, originNetwork, false)
	require.NoError(t, err)

	go bridgeL2Sync.Start(ctx)

	l2Setup := &L2Environment{
		CommonEnvironment: CommonEnvironment{
			SimBackend:             l2Client,
			GERAddr:                gerL2Addr,
			AggOracleCommitteeAddr: aggOracleCommitteeAddr,
			BridgeContract:         bridgeL2Contract,
			BridgeAddr:             bridgeL2Addr,
			Auth:                   authL2,
			ReorgDetector:          rdL2,
			BridgeSync:             bridgeL2Sync,
		},
		GERManagerSovereignSC:      l2GERSovereignChainSC,
		GERManagerLegacySC:         l2GERLegacySC,
		AggOracleCommitteeContract: aggOracleCommitteeContract,
		AggoracleSender:            sender,
		EthTxManagerMock:           ethTxManagerMock,
	}

	switch cfg.L2GERManagerType {
	case SovereignChainL2GERContract:
		require.NotNil(t, l2Setup.GERManagerSovereignSC)
		require.NotNil(t, l2Setup.AggoracleSender)
	case LegacyL2GERContract:
		require.NotNil(t, l2Setup.GERManagerLegacySC)
	}

	return l2Setup
}

func NewSimulatedL1(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*agglayerger.Agglayerger,
	common.Address,
	*agglayerbridge.Agglayerbridge,
	*agglayermanager.Agglayermanager,
) {
	t.Helper()

	deployerAuth, err := CreateAccount(big.NewInt(chainID))
	require.NoError(t, err)

	client, setup := NewSimulatedBackend(t, nil, deployerAuth)

	ctx := context.Background()
	nonce, err := client.Client().PendingNonceAt(ctx, setup.DeployerAuth.From)
	require.NoError(t, err)

	// DeployBridge function sends two transactions (bridge and proxy contract deployment)
	calculatedGERAddr := crypto.CreateAddress(setup.DeployerAuth.From, nonce+2) //nolint:mnd

	err = setup.DeployBridge(client, calculatedGERAddr, 0)
	require.NoError(t, err)

	gerAddr, _, gerContract, err := agglayerger.DeployAgglayerger(
		setup.DeployerAuth, client.Client(),
		setup.UserAuth.From, setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	_, agglayerManagerSC, err := setup.DeployAgglayerManager(
		client, calculatedGERAddr, calculatedGERAddr, setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	require.Equal(t, calculatedGERAddr, gerAddr)

	return client, setup.UserAuth, gerAddr, gerContract,
		setup.BridgeProxyAddr, setup.BridgeProxyContract, agglayerManagerSC
}

func newSimulatedEVML2SovereignChain(t *testing.T, aggOracleCommitteeConfig AggoraclecommitteeConfig) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*agglayergerl2.Agglayergerl2,
	common.Address,
	*agglayerbridge.Agglayerbridge,
	common.Address,
	*aggoraclecommittee.Aggoraclecommittee,
) {
	t.Helper()

	const aggOracleCommitteeNonce = 4

	deployerAuth, err := CreateAccount(big.NewInt(chainID))
	require.NoError(t, err)

	premineBalance, ok := new(big.Int).SetString(defaultBalance, base10)
	require.True(t, ok)

	const deployedContractsCount = 3
	l2BridgeProxyAddr := crypto.CreateAddress(deployerAuth.From, deployedContractsCount)

	genesisAllocMap := map[common.Address]types.Account{
		l2BridgeProxyAddr: {Balance: premineBalance},
	}
	var (
		precalculatedAggOracleCommitteeAddr      common.Address
		precalculatedAggOracleCommitteeProxyAddr common.Address
	)

	if aggOracleCommitteeConfig.EnableAggOracleCommittee {
		// Create aggoracle committee address from deployerAuth.From and nonce = 4
		precalculatedAggOracleCommitteeAddr = crypto.CreateAddress(deployerAuth.From, aggOracleCommitteeNonce)
		precalculatedAggOracleCommitteeProxyAddr = crypto.CreateAddress(deployerAuth.From, aggOracleCommitteeNonce+1)
		genesisAllocMap[precalculatedAggOracleCommitteeAddr] = types.Account{Balance: premineBalance}
		genesisAllocMap[precalculatedAggOracleCommitteeProxyAddr] = types.Account{Balance: premineBalance}
	}

	client, setup := NewSimulatedBackend(
		t,
		genesisAllocMap,
		deployerAuth,
	)

	// Deploy L2 GER manager contract
	gerL2Addr, _, _, err := agglayergerl2.DeployAgglayergerl2(
		setup.DeployerAuth, client.Client(), setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	// Prepare initialize data that are going to be called by the L2 GER proxy contract
	gerL2Abi, err := agglayergerl2.Agglayergerl2MetaData.GetAbi()
	require.NoError(t, err)
	require.NotNil(t, gerL2Abi)

	var gerL2InitData []byte
	if aggOracleCommitteeConfig.EnableAggOracleCommittee {
		// Use AggOracleCommitteeProxyAddr for initialization when EnableAggOracleCommittee is true
		// The committee proxy serves as the _globalExitRootUpdater and _globalExitRootRemover
		gerL2InitData, err = gerL2Abi.Pack(
			"initialize",
			precalculatedAggOracleCommitteeProxyAddr,
			precalculatedAggOracleCommitteeProxyAddr)
		require.NoError(t, err)
	} else {
		gerL2InitData, err = gerL2Abi.Pack("initialize", setup.UserAuth.From, setup.UserAuth.From)
		require.NoError(t, err)
	}

	// Deploy L2 GER manager proxy contract
	gerProxyAddr, _, _, err := proxy.DeployProxy(
		setup.DeployerAuth,
		client.Client(),
		gerL2Addr,
		setup.DeployerAuth.From,
		gerL2InitData,
	)
	require.NoError(t, err)
	client.Commit()

	// Create L2 GER manager contract binding
	gerL2Contract, err := agglayergerl2.NewAgglayergerl2(
		gerProxyAddr, client.Client())
	require.NoError(t, err)

	err = setup.DeployBridge(client, gerProxyAddr, rollupID)
	require.NoError(t, err)
	require.Equal(t, l2BridgeProxyAddr, setup.BridgeProxyAddr)

	bridgeGERAddr, err := setup.BridgeProxyContract.GlobalExitRootManager(nil)
	require.NoError(t, err)
	require.Equal(t, gerProxyAddr, bridgeGERAddr)

	// Deploy AggOracleCommittee contract if EnableAggOracleCommittee is true
	var (
		aggOracleCommitteeProxyAddr common.Address
		aggOracleCommitteeContract  *aggoraclecommittee.Aggoraclecommittee
	)

	if aggOracleCommitteeConfig.EnableAggOracleCommittee {
		// Deploy AggOracleCommittee contract
		aggOracleCommitteeAddr, _, _, err := aggoraclecommittee.DeployAggoraclecommittee(
			setup.DeployerAuth, client.Client(), gerProxyAddr)
		require.NoError(t, err)
		client.Commit()

		// Prepare initialize data that are going to be called by the aggoracle committee contract
		aggOracleCommitteeAbi, err := aggoraclecommittee.AggoraclecommitteeMetaData.GetAbi()
		require.NoError(t, err)
		require.NotNil(t, aggOracleCommitteeAbi)

		// aggOracleMembers are the addresses of the aggoracle committee members
		aggOracleMembers := []common.Address{setup.UserAuth.From}

		// add other aggoracle committee members to the aggOracleMembers slice based on the quorum
		for i := 0; i < int(aggOracleCommitteeConfig.Quorum)-1; i++ {
			aggOracleMembers = append(
				aggOracleMembers,
				crypto.CreateAddress(setup.DeployerAuth.From, aggOracleCommitteeNonce+uint64(i+1)),
			)
		}

		aggOracleCommitteeInitData, err := aggOracleCommitteeAbi.Pack(
			"initialize",
			setup.DeployerAuth.From,
			aggOracleMembers,
			aggOracleCommitteeConfig.Quorum,
		)
		require.NoError(t, err)

		// Deploy a proxy contract for the aggoracle committee
		aggOracleCommitteeProxyAddr, _, _, err = proxy.DeployProxy(
			setup.DeployerAuth,
			client.Client(),
			aggOracleCommitteeAddr,
			setup.DeployerAuth.From,
			aggOracleCommitteeInitData,
		)
		require.NoError(t, err)
		client.Commit()

		// Create aggoracle committee contract binding
		aggOracleCommitteeContract, err = aggoraclecommittee.NewAggoraclecommittee(
			aggOracleCommitteeProxyAddr, client.Client())
		require.NoError(t, err)
	}

	return client, setup.UserAuth, gerProxyAddr, gerL2Contract,
		setup.BridgeProxyAddr, setup.BridgeProxyContract,
		aggOracleCommitteeProxyAddr, aggOracleCommitteeContract
}

// newSimulatedEVML2LegacyChain creates a new simulated L2 environment with legacy GER contract.
// It deploys the PolygonZkEVMGlobalExitRootV2 contract and the PolygonZkEVMBridgeV2 contract.
func newSimulatedEVML2LegacyChain(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*agglayerger.Agglayerger,
	common.Address,
	*agglayerbridge.Agglayerbridge,
) {
	t.Helper()

	deployerAuth, err := CreateAccount(big.NewInt(chainID))
	require.NoError(t, err)

	premineBalance, ok := new(big.Int).SetString(defaultBalance, base10)
	require.True(t, ok)

	const deployedContractsCount = 3
	l2BridgeProxyAddr := crypto.CreateAddress(deployerAuth.From, deployedContractsCount)

	genesisAllocMap := map[common.Address]types.Account{
		l2BridgeProxyAddr: {Balance: premineBalance},
	}
	client, setup := NewSimulatedBackend(t, genesisAllocMap, deployerAuth)

	// Deploy L2 GER manager contract
	gerL2Addr, _, _, err := agglayerger.DeployAgglayerger(
		setup.DeployerAuth, client.Client(), setup.UserAuth.From, setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	// Prepare initialize data that are going to be called by the L2 GER proxy contract
	gerL2Abi, err := agglayerger.AgglayergerMetaData.GetAbi()
	require.NoError(t, err)
	require.NotNil(t, gerL2Abi)

	gerL2InitData, err := gerL2Abi.Pack("initialize")
	require.NoError(t, err)

	// Deploy L2 GER manager proxy contract
	gerProxyAddr, _, _, err := proxy.DeployProxy(
		setup.DeployerAuth,
		client.Client(),
		gerL2Addr,
		setup.DeployerAuth.From,
		gerL2InitData,
	)
	require.NoError(t, err)
	client.Commit()

	// Create L2 GER manager contract binding
	gerL2Contract, err := agglayerger.NewAgglayerger(
		gerProxyAddr, client.Client())
	require.NoError(t, err)

	err = setup.DeployBridge(client, gerProxyAddr, rollupID)
	require.NoError(t, err)
	require.Equal(t, l2BridgeProxyAddr, setup.BridgeProxyAddr)

	bridgeGERAddr, err := setup.BridgeProxyContract.GlobalExitRootManager(nil)
	require.NoError(t, err)
	require.Equal(t, gerProxyAddr, bridgeGERAddr)

	return client, setup.UserAuth, gerProxyAddr, gerL2Contract, setup.BridgeProxyAddr, setup.BridgeProxyContract
}

func WaitForSyncerToCatchUp(ctx context.Context, t *testing.T, syncer Processorer, client *simulated.Backend) {
	t.Helper()
	for {
		lastBlockNum, err := client.Client().BlockNumber(ctx)
		require.NoError(t, err)
		RequireProcessorUpdated(t, syncer, lastBlockNum, nil)
		time.Sleep(time.Second / 2)
		lastBlockNum2, err := client.Client().BlockNumber(ctx)
		require.NoError(t, err)
		if lastBlockNum == lastBlockNum2 {
			return
		}
	}
}
