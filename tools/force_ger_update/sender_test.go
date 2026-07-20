package force_ger_update

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/0xPolygon/cdk-contracts-tooling/contracts/aggchain-multisig/agglayerbridge"
	"github.com/0xPolygon/zkevm-ethtx-manager/ethtxmanager"
	ethtxtypes "github.com/0xPolygon/zkevm-ethtx-manager/types"
	aggoraclemocks "github.com/agglayer/aggkit/aggoracle/mocks"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const testPollInterval = time.Millisecond

var (
	testBridgeAddr = common.HexToAddress("0x1111111111111111111111111111111111111111")
	testDestAddr   = common.HexToAddress("0x2222222222222222222222222222222222222222")
	testSenderAddr = common.HexToAddress("0x3333333333333333333333333333333333333333")
)

func testConfig() ForceGERUpdateConfig {
	return ForceGERUpdateConfig{
		BridgeAddr:         testBridgeAddr,
		DestinationNetwork: 1,
		DestinationAddress: testDestAddr,
	}
}

// decodeBridgeMessageCalldata asserts data has the bridgeMessage selector and decodes its
// arguments via the real agglayerbridge ABI.
func decodeBridgeMessageCalldata(t *testing.T, data []byte) (destNetwork uint32, destAddr common.Address,
	forceUpdate bool, metadata []byte) {
	t.Helper()

	require.GreaterOrEqual(t, len(data), 4, "calldata must at least contain the selector")
	require.Equal(t, "240ff378", common.Bytes2Hex(data[:4]), "unexpected bridgeMessage selector")

	bridgeAbi, err := agglayerbridge.AgglayerbridgeMetaData.GetAbi()
	require.NoError(t, err)

	method, err := bridgeAbi.MethodById(data[:4])
	require.NoError(t, err)
	require.Equal(t, bridgeMessageFuncName, method.Name)

	args, err := method.Inputs.Unpack(data[4:])
	require.NoError(t, err)
	require.Len(t, args, 4)

	destNetwork, ok := args[0].(uint32)
	require.True(t, ok)
	destAddr, ok = args[1].(common.Address)
	require.True(t, ok)
	forceUpdate, ok = args[2].(bool)
	require.True(t, ok)
	metadata, ok = args[3].([]byte)
	require.True(t, ok)

	return destNetwork, destAddr, forceUpdate, metadata
}

func TestSendForcedGERUpdate_Success(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()

	txID := common.HexToHash("0xaaaa")

	var capturedData []byte
	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Run(func(_ context.Context, _ *common.Address, _ *big.Int, data []byte, _ uint64, _ *types.BlobTxSidecar) {
			capturedData = data
		}).
		Return(txID, nil).
		Once()

	ethTxMan.EXPECT().
		Result(mock.Anything, txID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusMined, MinedAtBlockNumber: nil}, nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, sender.SendForcedGERUpdate(ctx))

	require.NotNil(t, capturedData)
	require.Equal(t, "240ff378", common.Bytes2Hex(capturedData[:4]))

	destNetwork, destAddr, forceUpdate, metadata := decodeBridgeMessageCalldata(t, capturedData)
	require.Equal(t, cfg.DestinationNetwork, destNetwork)
	require.Equal(t, cfg.DestinationAddress, destAddr)
	require.True(t, forceUpdate)
	require.Empty(t, metadata)
}

func TestSendForcedGERUpdate_Failed(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()

	txID := common.HexToHash("0xbbbb")

	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Return(txID, nil).
		Once()

	ethTxMan.EXPECT().
		Result(mock.Anything, txID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusFailed}, nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = sender.SendForcedGERUpdate(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), txID.Hex())
}

func TestSendForcedGERUpdate_AlreadyExists(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()

	txID := common.HexToHash("0xcccc")

	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Return(txID, ethtxmanager.ErrAlreadyExists).
		Once()

	ethTxMan.EXPECT().
		Result(mock.Anything, txID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusMined}, nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, sender.SendForcedGERUpdate(ctx))
}

func TestSendForcedGERUpdate_DryRun(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()
	cfg.DryRun = true

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	require.NoError(t, sender.SendForcedGERUpdate(ctx))

	ethTxMan.AssertNotCalled(t, "Add",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	ethTxMan.AssertNotCalled(t, "Result", mock.Anything, mock.Anything)
}

func TestNewSender_DefaultsDestinationAddressToSenderFrom(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	ethTxMan.EXPECT().From().Return(testSenderAddr).Once()

	cfg := testConfig()
	cfg.DestinationAddress = common.Address{}

	sender, err := NewSender(cfg, ethTxMan)
	require.NoError(t, err)
	require.Equal(t, testSenderAddr, sender.destinationAddress)
}
