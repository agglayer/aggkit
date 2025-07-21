package helpers

import (
	"context"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/globalexitrootmanagerl2sovereignchain"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmbridgev2"
	"github.com/0xPolygon/cdk-contracts-tooling/contracts/pp/l2-sovereign-chain/polygonzkevmglobalexitrootv2"
	"github.com/agglayer/aggkit/aggoracle"
	"github.com/agglayer/aggkit/aggoracle/chaingersender"
	"github.com/agglayer/aggkit/bridgesync"
	cfgTypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/l1infotreesync"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/test/contracts/transparentupgradableproxy"
	aggkittypes "github.com/agglayer/aggkit/types"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient/simulated"
	"github.com/stretchr/testify/require"
)

const (
	rollupID           = uint32(1)
	syncBlockChunkSize = 10
)

type L2GERManagerContractType int

const (
	SovereignChainL2GERContract L2GERManagerContractType = iota
	LegacyL2GERContract
)

// CommonEnvironment contains common setup results used in both L1 and L2 network setups.
type CommonEnvironment struct {
	SimBackend     *simulated.Backend
	GERAddr        common.Address
	BridgeContract *polygonzkevmbridgev2.Polygonzkevmbridgev2
	BridgeAddr     common.Address
	Auth           *bind.TransactOpts
	ReorgDetector  *reorgdetector.ReorgDetector
	BridgeSync     *bridgesync.BridgeSync
}

// L1Environment contains simulated setup for L1 network.
type L1Environment struct {
	CommonEnvironment
	GERContract  *polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2
	InfoTreeSync *l1infotreesync.L1InfoTreeSync
}

// L2Environment contains simulated setup for L2 network.
type L2Environment struct {
	CommonEnvironment
	GERManagerSovereignSC *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain
	GERManagerLegacySC    *polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2
	AggoracleSender       aggoracle.ChainSender
	EthTxManagerMock      *EthTxManager
}

type EnvironmentConfig struct {
	L1RPCClient      aggkittypes.RPCClienter
	L2RPCClient      aggkittypes.RPCClienter
	L2GERManagerType L2GERManagerContractType
}

func DefaultEnvironmentConfig(l2GERManagerType L2GERManagerContractType) *EnvironmentConfig {
	return &EnvironmentConfig{
		L1RPCClient:      &aggkittypes.NoopRPCClient{},
		L2RPCClient:      &aggkittypes.NoopRPCClient{},
		L2GERManagerType: l2GERManagerType,
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
	l1Client, authL1, gerL1Addr, gerL1Contract, bridgeL1Addr, bridgeL1Contract := newSimulatedL1(t)

	// Reorg detector
	dbPathReorgDetectorL1 := path.Join(t.TempDir(), "ReorgDetectorL1.sqlite")
	rdL1, err := reorgdetector.New(l1Client.Client(), reorgdetector.Config{
		DBPath:              dbPathReorgDetectorL1,
		CheckReorgsInterval: cfgTypes.Duration{Duration: time.Millisecond * 100}, //nolint:mnd
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	go rdL1.Start(ctx) //nolint:errcheck

	const (
		l1InfoTreeSyncerRetries   = 3
		l1InfoTreeSyncerRetryFreq = time.Millisecond * 100
	)

	// L1 info tree sync
	dbPathL1InfoTreeSync := path.Join(t.TempDir(), "L1InfoTreeSync.sqlite")
	l1InfoTreeSync, err := l1infotreesync.New(
		ctx, dbPathL1InfoTreeSync,
		gerL1Addr, common.Address{},
		syncBlockChunkSize, aggkittypes.LatestBlock,
		rdL1, l1Client.Client(),
		time.Millisecond, 0, l1InfoTreeSyncerRetryFreq,
		l1InfoTreeSyncerRetries, l1infotreesync.FlagAllowWrongContractsAddrs,
		aggkittypes.SafeBlock,
		true,
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
	bridgeL1Sync, err := bridgesync.NewL1(
		ctx, dbPathBridgeSyncL1, bridgeL1Addr,
		syncBlockChunkSize, aggkittypes.LatestBlock, rdL1, testClient,
		initialBlock, waitForNewBlocksPeriod, retryPeriod,
		retriesCount, originNetwork, false, true)
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
		GERContract:  gerL1Contract,
		InfoTreeSync: l1InfoTreeSync,
	}
}

// L2Setup creates a new L2 environment.
func L2Setup(t *testing.T, cfg *EnvironmentConfig, l1Setup *L1Environment) *L2Environment {
	t.Helper()

	ctx := context.Background()

	var (
		l2Client              *simulated.Backend
		authL2                *bind.TransactOpts
		gerL2Addr             common.Address
		l2GERLegacySC         *polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2
		l2GERSovereignChainSC *globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain
		bridgeL2Addr          common.Address
		bridgeL2Contract      *polygonzkevmbridgev2.Polygonzkevmbridgev2
	)

	switch cfg.L2GERManagerType {
	case LegacyL2GERContract:
		l2Client, authL2, gerL2Addr, l2GERLegacySC,
			bridgeL2Addr, bridgeL2Contract = newSimulatedEVML2LegacyChain(t)

	case SovereignChainL2GERContract:
		l2Client, authL2, gerL2Addr, l2GERSovereignChainSC,
			bridgeL2Addr, bridgeL2Contract = newSimulatedEVML2SovereignChain(t)

	default:
		require.Failf(t, "unknown L2 GER manager type provided", "(l2 ger manager type %d)", int(cfg.L2GERManagerType))
	}

	var (
		sender           aggoracle.ChainSender
		ethTxManagerMock *EthTxManager
		err              error
	)

	if cfg.L2GERManagerType == SovereignChainL2GERContract {
		ethTxManagerMock = NewEthTxManMock(t, l2Client, authL2)
		const (
			gerCheckFrequency     = time.Millisecond * 50
			gerInjectionFrequency = time.Millisecond * 20
		)

		sender, err = chaingersender.NewEVMChainGERSender(
			log.GetDefaultLogger(), gerL2Addr, l2Client.Client(),
			ethTxManagerMock, 0, gerCheckFrequency,
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
		CheckReorgsInterval: cfgTypes.Duration{Duration: time.Millisecond * 100}, //nolint:mnd
		FinalizedBlock:      aggkittypes.FinalizedBlock,
	},
		reorgdetector.L2,
	)
	require.NoError(t, err)
	go rdL2.Start(ctx) //nolint:errcheck

	// Bridge sync
	dbPathL2BridgeSync := path.Join(t.TempDir(), "BridgeSyncL2.sqlite")
	testClient := NewTestClient(l2Client.Client(), WithRPCClienter(cfg.L2RPCClient))

	const (
		waitForNewBlocksPeriod = 10 * time.Millisecond
		originNetwork          = 1
		initialBlock           = 0
		retryPeriod            = 50 * time.Millisecond
		retriesCount           = 100
	)

	bridgeL2Sync, err := bridgesync.NewL2(
		ctx, dbPathL2BridgeSync, bridgeL2Addr, syncBlockChunkSize,
		aggkittypes.LatestBlock, rdL2, testClient,
		initialBlock, waitForNewBlocksPeriod, retryPeriod,
		retriesCount, originNetwork, false, true)
	require.NoError(t, err)

	go bridgeL2Sync.Start(ctx)

	l2Setup := &L2Environment{
		CommonEnvironment: CommonEnvironment{
			SimBackend:     l2Client,
			GERAddr:        gerL2Addr,
			BridgeContract: bridgeL2Contract,
			BridgeAddr:     bridgeL2Addr,
			Auth:           authL2,
			ReorgDetector:  rdL2,
			BridgeSync:     bridgeL2Sync,
		},
		GERManagerSovereignSC: l2GERSovereignChainSC,
		GERManagerLegacySC:    l2GERLegacySC,
		AggoracleSender:       sender,
		EthTxManagerMock:      ethTxManagerMock,
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

func newSimulatedL1(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2,
	common.Address,
	*polygonzkevmbridgev2.Polygonzkevmbridgev2,
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

	gerAddr, _, gerContract, err := polygonzkevmglobalexitrootv2.DeployPolygonzkevmglobalexitrootv2(
		setup.DeployerAuth, client.Client(),
		setup.UserAuth.From, setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	require.Equal(t, calculatedGERAddr, gerAddr)

	return client, setup.UserAuth, gerAddr, gerContract, setup.BridgeProxyAddr, setup.BridgeProxyContract
}

// newSimulatedEVML2SovereignChain creates a new simulated L2 environment with sovereign chain GER contract.
// It deploys the GlobalExitRootManagerL2SovereignChain contract and the PolygonZkEVMBridgeV2 contract.
func newSimulatedEVML2SovereignChain(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchain,
	common.Address,
	*polygonzkevmbridgev2.Polygonzkevmbridgev2,
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
	gerL2Addr, _, _, err := globalexitrootmanagerl2sovereignchain.DeployGlobalexitrootmanagerl2sovereignchain(
		setup.DeployerAuth, client.Client(), setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	// Prepare initialize data that are going to be called by the L2 GER proxy contract
	gerL2Abi, err := globalexitrootmanagerl2sovereignchain.Globalexitrootmanagerl2sovereignchainMetaData.GetAbi()
	require.NoError(t, err)
	require.NotNil(t, gerL2Abi)

	gerL2InitData, err := gerL2Abi.Pack("initialize", setup.UserAuth.From, setup.UserAuth.From)
	require.NoError(t, err)

	// Deploy L2 GER manager proxy contract
	gerProxyAddr, _, _, err := transparentupgradableproxy.DeployTransparentupgradableproxy(
		setup.DeployerAuth,
		client.Client(),
		gerL2Addr,
		setup.DeployerAuth.From,
		gerL2InitData,
	)
	require.NoError(t, err)
	client.Commit()

	// Create L2 GER manager contract binding
	gerL2Contract, err := globalexitrootmanagerl2sovereignchain.NewGlobalexitrootmanagerl2sovereignchain(
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

// newSimulatedEVML2LegacyChain creates a new simulated L2 environment with legacy GER contract.
// It deploys the PolygonZkEVMGlobalExitRootV2 contract and the PolygonZkEVMBridgeV2 contract.
func newSimulatedEVML2LegacyChain(t *testing.T) (
	*simulated.Backend,
	*bind.TransactOpts,
	common.Address,
	*polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2,
	common.Address,
	*polygonzkevmbridgev2.Polygonzkevmbridgev2,
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
	gerL2Addr, _, _, err := polygonzkevmglobalexitrootv2.DeployPolygonzkevmglobalexitrootv2(
		setup.DeployerAuth, client.Client(), setup.UserAuth.From, setup.BridgeProxyAddr)
	require.NoError(t, err)
	client.Commit()

	// Prepare initialize data that are going to be called by the L2 GER proxy contract
	gerL2Abi, err := polygonzkevmglobalexitrootv2.Polygonzkevmglobalexitrootv2MetaData.GetAbi()
	require.NoError(t, err)
	require.NotNil(t, gerL2Abi)

	gerL2InitData, err := gerL2Abi.Pack("initialize")
	require.NoError(t, err)

	// Deploy L2 GER manager proxy contract
	gerProxyAddr, _, _, err := transparentupgradableproxy.DeployTransparentupgradableproxy(
		setup.DeployerAuth,
		client.Client(),
		gerL2Addr,
		setup.DeployerAuth.From,
		gerL2InitData,
	)
	require.NoError(t, err)
	client.Commit()

	// Create L2 GER manager contract binding
	gerL2Contract, err := polygonzkevmglobalexitrootv2.NewPolygonzkevmglobalexitrootv2(
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
