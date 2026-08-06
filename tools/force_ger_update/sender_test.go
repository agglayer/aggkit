package force_ger_update

import (
	"context"
	"errors"
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

	ethTxMan.EXPECT().
		Remove(mock.Anything, txID).
		Return(nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	confirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.False(t, confirmedAt.IsZero(), "a genuinely mined tx must report a non-zero confirmation time")

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

	ethTxMan.EXPECT().
		Remove(mock.Anything, txID).
		Return(nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	confirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), txID.Hex())
	require.True(t, confirmedAt.IsZero())
}

func TestSendForcedGERUpdate_Evicted(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()

	txID := common.HexToHash("0xdddd")

	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Return(txID, nil).
		Once()

	ethTxMan.EXPECT().
		Result(mock.Anything, txID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusEvicted}, nil).
		Once()

	ethTxMan.EXPECT().
		Remove(mock.Anything, txID).
		Return(nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	confirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.True(t, confirmedAt.IsZero(), "an evicted tx never actually updated the GER, so it must not reset the caller's timer")
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

	ethTxMan.EXPECT().
		Remove(mock.Anything, txID).
		Return(nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	confirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.False(t, confirmedAt.IsZero())
}

// TestSendForcedGERUpdate_RepeatedCallsResubmitAfterCompletion is the regression test for the
// bug this fix addresses: every SendForcedGERUpdate call packs byte-for-byte identical calldata,
// so ethtxmanager.Add would deterministically derive the same id every time. Before this fix, a
// completed tx's record lingered forever in the ethtxmanager's monitoring DB, so every later call
// collided on Add with ErrAlreadyExists and just re-observed the first (already-terminal) tx
// forever — no new transaction was ever actually broadcast again, which looked like the tool
// "hanging" between sends and re-triggering a no-op send on every restart. This test drives two
// full SendForcedGERUpdate calls back-to-back and asserts the second one gets a fresh Add (no
// ErrAlreadyExists) with its own distinct id, proving the first tx's record was forgotten.
func TestSendForcedGERUpdate_RepeatedCallsResubmitAfterCompletion(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()

	firstID := common.HexToHash("0xeeee")
	secondID := common.HexToHash("0xffff")

	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Return(firstID, nil).
		Once()
	ethTxMan.EXPECT().
		Result(mock.Anything, firstID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusMined}, nil).
		Once()
	ethTxMan.EXPECT().
		Remove(mock.Anything, firstID).
		Return(nil).
		Once()

	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Return(secondID, nil).
		Once()
	ethTxMan.EXPECT().
		Result(mock.Anything, secondID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusMined}, nil).
		Once()
	ethTxMan.EXPECT().
		Remove(mock.Anything, secondID).
		Return(nil).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstConfirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.False(t, firstConfirmedAt.IsZero())

	secondConfirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.False(t, secondConfirmedAt.IsZero())
}

func TestSendForcedGERUpdate_DryRun(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()
	cfg.DryRun = true

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	confirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.True(t, confirmedAt.IsZero(), "dry-run never actually sends anything, so it must not reset the caller's timer")

	ethTxMan.AssertNotCalled(t, "Add",
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	ethTxMan.AssertNotCalled(t, "Result", mock.Anything, mock.Anything)
}

func TestSendForcedGERUpdate_RemoveFailureDoesNotFailSend(t *testing.T) {
	ethTxMan := aggoraclemocks.NewEthTxManager(t)
	cfg := testConfig()

	txID := common.HexToHash("0x1234")

	ethTxMan.EXPECT().
		Add(mock.Anything, &testBridgeAddr, common.Big0, mock.Anything, uint64(0), (*types.BlobTxSidecar)(nil)).
		Return(txID, nil).
		Once()
	ethTxMan.EXPECT().
		Result(mock.Anything, txID).
		Return(ethtxtypes.MonitoredTxResult{Status: ethtxtypes.MonitoredTxStatusMined}, nil).
		Once()
	ethTxMan.EXPECT().
		Remove(mock.Anything, txID).
		Return(errors.New("db locked")).
		Once()

	sender, err := NewSender(cfg, ethTxMan, WithPollInterval(testPollInterval))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	confirmedAt, err := sender.SendForcedGERUpdate(ctx)
	require.NoError(t, err)
	require.False(t, confirmedAt.IsZero(), "a Remove failure is best-effort and must not suppress the mined confirmation")
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
