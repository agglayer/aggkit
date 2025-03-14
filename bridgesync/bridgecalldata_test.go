package bridgesync

import (
	"context"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/etrog/polygonzkevmbridgev2"
	cfgtypes "github.com/agglayer/aggkit/config/types"
	"github.com/agglayer/aggkit/etherman"
	"github.com/agglayer/aggkit/reorgdetector"
	"github.com/agglayer/aggkit/test/contracts/transparentupgradableproxy"
	aggkittypes "github.com/agglayer/aggkit/types"
	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/require"
)

func TestBridgeCallData(t *testing.T) {
	ctx, cancelFn := context.WithCancel(context.Background())
	client, deployerAuth := startGeth(t, ctx, cancelFn)

	bridgeABI, err := polygonzkevmbridgev2.Polygonzkevmbridgev2MetaData.GetAbi()
	require.NoError(t, err)

	var (
		originNetwork = uint32(1)
		zeroAddr      = common.HexToAddress("0x0")
	)

	initBridgeCalldata, err := bridgeABI.Pack("initialize",
		originNetwork,
		zeroAddr,  // gasTokenAddressMainnet
		uint32(0), // gasTokenNetworkMainnet
		zeroAddr,  // global exit root manager
		zeroAddr,  // rollup manager
		[]byte{},  // gasTokenMetadata
	)
	require.NoError(t, err)

	bridgeAddr, deployBridgeTx, _, err := polygonzkevmbridgev2.DeployPolygonzkevmbridgev2(deployerAuth, client)
	require.NoError(t, err)
	_, err = waitForReceipt(ctx, client, deployBridgeTx.Hash(), 20)
	require.NoError(t, err)

	bridgeProxyAddr, deployProxyTx, _, err := transparentupgradableproxy.DeployTransparentupgradableproxy(
		deployerAuth,
		client,
		bridgeAddr,
		deployerAuth.From,
		initBridgeCalldata,
	)
	require.NoError(t, err)

	_, err = waitForReceipt(ctx, client, deployProxyTx.Hash(), 20)
	require.NoError(t, err)

	bridgeContract, err := polygonzkevmbridgev2.NewPolygonzkevmbridgev2(bridgeProxyAddr, client)
	require.NoError(t, err)

	dbPathBridgeSyncL1 := path.Join(t.TempDir(), "BridgeSyncL1.sqlite")

	const (
		waitForNewBlocksPeriod = time.Millisecond * 10
		initialBlock           = 0
		retryPeriod            = time.Millisecond * 30
		retriesCount           = 10
	)

	networkID, err := bridgeContract.NetworkID(nil)
	require.NoError(t, err)
	require.Equal(t, originNetwork, networkID)

	// Create User account and fund it
	userAuth := createAuth(t, ctx, "42b6e34dc21598a807dc19d7784c71b2a7a01f6480dc6f58258f78e539f1a1fa", client)

	nonce, err := client.PendingNonceAt(ctx, deployerAuth.From)
	require.NoError(t, err)

	gasPrice, err := client.SuggestGasPrice(ctx)
	require.NoError(t, err)

	fundAmount := big.NewInt(1e18)
	fundTx := types.NewTx(
		&types.LegacyTx{
			To:       &userAuth.From,
			Value:    fundAmount,
			Nonce:    nonce,
			Gas:      21000,
			GasPrice: gasPrice,
		})

	signedFundTx, err := deployerAuth.Signer(deployerAuth.From, fundTx)
	require.NoError(t, err)

	err = client.SendTransaction(ctx, signedFundTx)
	require.NoError(t, err)

	_, err = waitForReceipt(ctx, client, signedFundTx.Hash(), 20)
	require.NoError(t, err)

	userBalance, err := client.BalanceAt(ctx, userAuth.From, big.NewInt(int64(etherman.Latest)))
	require.NoError(t, err)
	require.True(t, userBalance.Cmp(fundAmount) >= 0)

	ethClient := &aggkittypes.DefaultEthClient{
		BaseEthereumClienter: client,
		RPCClienter:          client.Client(),
	}

	// Init the reorg detector and bridge syncer
	dbPathReorgDetectorL1 := path.Join(t.TempDir(), "ReorgDetectorL1.sqlite")
	reorgDetector, err := reorgdetector.New(client, reorgdetector.Config{
		DBPath:              dbPathReorgDetectorL1,
		CheckReorgsInterval: cfgtypes.Duration{Duration: time.Millisecond * 100},
		FinalizedBlock:      etherman.LatestBlock,
	}, reorgdetector.L1)
	require.NoError(t, err)
	go reorgDetector.Start(ctx) //nolint:errcheck

	bridgeSync, err := NewL1(ctx, dbPathBridgeSyncL1, bridgeProxyAddr, 1, etherman.LatestBlock, reorgDetector, ethClient,
		initialBlock, waitForNewBlocksPeriod, retryPeriod, retriesCount, originNetwork, false)
	require.NoError(t, err)
	go bridgeSync.Start(ctx)

	var (
		page     = uint32(1)
		pageSize = uint32(10)

		destinationNetwork = uint32(2)
		destinationAddr    = userAuth.From
		amount             = big.NewInt(1000)
	)

	bridgeAssetInput, err := bridgeABI.Pack("bridgeAsset",
		destinationNetwork, // destination network id
		destinationAddr,    // destination address
		amount,             // amount of tokens being bridged
		zeroAddr,           // token address
		false,              // update global exit root
		[]byte{})           // permit data
	require.NoError(t, err)

	nonce, err = client.PendingNonceAt(ctx, userAuth.From)
	require.NoError(t, err)

	gasPrice, err = client.SuggestGasPrice(ctx)
	require.NoError(t, err)

	gas, err := client.EstimateGas(ctx, ethereum.CallMsg{To: &bridgeProxyAddr, Data: bridgeAssetInput, Value: amount})
	require.NoError(t, err)

	bridgeAssetTx := types.NewTx(
		&types.LegacyTx{
			To:       &bridgeProxyAddr,
			Value:    amount,
			Nonce:    nonce,
			Data:     bridgeAssetInput,
			Gas:      gas,
			GasPrice: gasPrice,
		})

	signedBridgeAssetTx, err := userAuth.Signer(userAuth.From, bridgeAssetTx)
	require.NoError(t, err)

	err = client.SendTransaction(ctx, signedBridgeAssetTx)
	require.NoError(t, err)

	r, err := waitForReceipt(ctx, client, signedBridgeAssetTx.Hash(), 10)
	require.NoError(t, err)
	require.Len(t, r.Logs, 1)

	maxAttempts := 100
	attempt := 0

	// wait for bridge event to get indexed
	for attempt < maxAttempts {
		bridgeResponse, totalCount, err := bridgeSync.GetBridgesPaged(ctx, page, pageSize, nil)
		require.NoError(t, err)

		if len(bridgeResponse) > 0 {
			require.Equal(t, 1, totalCount)
			require.Equal(t, signedBridgeAssetTx.Data(), bridgeResponse[0].Calldata)
			break
		}

		attempt++
		time.Sleep(1 * time.Second)
	}

	require.NotEqual(t, maxAttempts, attempt, "Max attempts reached without getting a valid bridge response")
}
