package exit_certificate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	agglayertypes "github.com/agglayer/aggkit/agglayer/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/require"
)

func TestIsContextCanceled(t *testing.T) {
	t.Parallel()
	require.False(t, isContextCanceled(nil))
	require.False(t, isContextCanceled(errors.New("other")))
	require.True(t, isContextCanceled(context.Canceled))
	require.True(t, isContextCanceled(fmt.Errorf("wrapped: %w", context.Canceled)))
}

func TestIsTransientForkError(t *testing.T) {
	t.Parallel()
	require.False(t, isTransientForkError(nil))
	require.False(t, isTransientForkError(context.Canceled))
	// a genuine revert is never transient, even if it mentions a marker word
	require.False(t, isTransientForkError(errors.New("execution reverted: connection")))

	for _, msg := range []string{
		"Fork Error: backend unreachable",
		"transport closed",
		"dispatch failure",
		"request timeout",
		"connection reset by peer",
		"unexpected EOF",
	} {
		require.True(t, isTransientForkError(errors.New(msg)), msg)
	}
	require.False(t, isTransientForkError(errors.New("invalid opcode")))
}

func TestParseBridgeEventLogRoundTrip(t *testing.T) {
	t.Parallel()
	want := bridgeEventLog{
		LeafType:           1,
		OriginNetwork:      5,
		OriginAddress:      common.HexToAddress("0xorigin"),
		DestinationNetwork: 0,
		DestinationAddress: common.HexToAddress("0xdest"),
		Amount:             big.NewInt(123456),
		Metadata:           []byte{0xde, 0xad},
		DepositCount:       9,
	}
	data, err := bridgeABI.Events["BridgeEvent"].Inputs.Pack(
		want.LeafType, want.OriginNetwork, want.OriginAddress, want.DestinationNetwork,
		want.DestinationAddress, want.Amount, want.Metadata, want.DepositCount,
	)
	require.NoError(t, err)
	hexData := "0x" + common.Bytes2Hex(data)

	// non-matching topic → matched=false, no error
	got, matched, err := parseBridgeEventLog([]string{common.HexToHash("0xdead").Hex()}, hexData)
	require.NoError(t, err)
	require.False(t, matched)
	require.Nil(t, got)

	// empty topics → matched=false
	_, matched, err = parseBridgeEventLog(nil, hexData)
	require.NoError(t, err)
	require.False(t, matched)

	// matching topic → decoded
	got, matched, err = parseBridgeEventLog([]string{bridgeEventTopicHash.Hex()}, hexData)
	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, want.LeafType, got.LeafType)
	require.Equal(t, want.OriginNetwork, got.OriginNetwork)
	require.Equal(t, want.OriginAddress, got.OriginAddress)
	require.Equal(t, want.DestinationAddress, got.DestinationAddress)
	require.Equal(t, want.Amount, got.Amount)
	require.Equal(t, want.Metadata, got.Metadata)
	require.Equal(t, want.DepositCount, got.DepositCount)

	// matching topic but garbage data → error
	_, _, err = parseBridgeEventLog([]string{bridgeEventTopicHash.Hex()}, "0xzznothex")
	require.Error(t, err)
}

func TestReplayedLeafFromReceipt(t *testing.T) {
	t.Parallel()
	ev := bridgeEventLog{
		LeafType: 0, OriginNetwork: 1, OriginAddress: common.HexToAddress("0xo"),
		DestinationNetwork: 0, DestinationAddress: common.HexToAddress("0xd"),
		Amount: big.NewInt(77), Metadata: []byte{0x01}, DepositCount: 4,
	}
	data, err := bridgeABI.Events["BridgeEvent"].Inputs.Pack(
		ev.LeafType, ev.OriginNetwork, ev.OriginAddress, ev.DestinationNetwork,
		ev.DestinationAddress, ev.Amount, ev.Metadata, ev.DepositCount,
	)
	require.NoError(t, err)

	txHash := common.HexToHash("0xtx")
	logs := []rpcLog{
		{Topics: []string{common.HexToHash("0xunrelated").Hex()}, Data: "0x"}, // skipped
		{
			Topics:      []string{bridgeEventTopicHash.Hex()},
			Data:        "0x" + common.Bytes2Hex(data),
			BlockNumber: "0x10",
			LogIndex:    "0x2",
		},
	}
	leaf, err := replayedLeafFromReceipt(logs, txHash)
	require.NoError(t, err)
	require.Equal(t, ev.DepositCount, leaf.DepositCount)
	require.Equal(t, txHash, leaf.TxHash)
	require.Equal(t, uint64(16), leaf.BlockNum)
	require.Equal(t, uint64(2), leaf.BlockPos)
	require.Equal(t, ev.Amount, leaf.Amount)

	// no BridgeEvent present → error
	_, err = replayedLeafFromReceipt([]rpcLog{{Topics: []string{common.HexToHash("0xnope").Hex()}}}, txHash)
	require.Error(t, err)
}

func TestDecodeRevertData(t *testing.T) {
	t.Parallel()
	// invalid hex / too short → returns input verbatim
	require.Equal(t, "0xzz", decodeRevertData("0xzz"))
	require.Equal(t, "0x01", decodeRevertData("0x01"))

	// unknown selector
	out := decodeRevertData("0xdeadbeef")
	require.Contains(t, out, "unknown selector")

	// known error: LocalBalanceTreeUnderflow(uint32,address,uint256,uint256)
	args := make([]byte, 4*32)
	args[31] = 3 // network=3 in the first word
	payload := append([]byte{0x14, 0x60, 0x3c, 0x01}, args...)
	out = decodeRevertData("0x" + common.Bytes2Hex(payload))
	require.Contains(t, out, "LocalBalanceTreeUnderflow")
	require.Contains(t, out, "network=3")

	// known selector but truncated args → falls back to sig + raw
	short := append([]byte{0x14, 0x60, 0x3c, 0x01}, 0x00)
	out = decodeRevertData("0x" + common.Bytes2Hex(short))
	require.Contains(t, out, "LocalBalanceTreeUnderflow")
}

func TestLogReplayProgress(t *testing.T) {
	t.Parallel()
	// purely exercises the logging/eta math; just must not panic
	require.NotPanics(t, func() {
		logReplayProgress(5, 10, time.Now().Add(-2*time.Second))
		logReplayProgress(0, 10, time.Now()) // rate 0 branch
	})
}

func TestFindFreePort(t *testing.T) {
	t.Parallel()
	p, err := findFreePort()
	require.NoError(t, err)
	require.Greater(t, p, 0)
}

func TestCheckAnvilAvailable(t *testing.T) {
	t.Parallel()
	// Result depends on whether anvil is installed; just verify it returns a typed result.
	if err := checkAnvilAvailable(); err != nil {
		require.Contains(t, err.Error(), "anvil not found")
	}
}

func TestSaveFailedExit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := exitJob{
		index:       2,
		isNative:    false,
		l2TokenAddr: common.HexToAddress("0xtoken"),
		bridge: &agglayertypes.BridgeExit{
			TokenInfo:          &agglayertypes.TokenInfo{OriginNetwork: 1, OriginTokenAddress: common.HexToAddress("0xorigin")},
			DestinationNetwork: 0,
			DestinationAddress: common.HexToAddress("0xdest"),
			Amount:             big.NewInt(500),
		},
	}
	saveFailedExit(dir, job, errors.New("replay blew up"))

	raw, err := os.ReadFile(filepath.Join(dir, "step-g-failed-exit.json"))
	require.NoError(t, err)
	var fe FailedBridgeExit
	require.NoError(t, json.Unmarshal(raw, &fe))
	require.Equal(t, 2, fe.Index)
	require.Equal(t, "replay blew up", fe.Error)
	require.Equal(t, uint32(1), fe.OriginNetwork)
	require.Equal(t, "500", fe.Amount)
}
