package l1infotreesync

import (
	"testing"

	"github.com/agglayer/aggkit/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var testHash = common.HexToHash("0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef")

func TestL1InfoTreeSyncRPC_Status(t *testing.T) {
	mockSyncer := NewL1InfoTreeSyncerMock(t)
	rpc := NewL1InfoTreeSyncRPC(log.WithFields("modules", "test"), mockSyncer)
	mockSyncer.EXPECT().GetCompletionPercentage().Return(nil).Once()
	result, err := rpc.Status()
	require.Nil(t, err, "expected no error from Status")

	statusInfo, ok := result.(StatusInfo)
	require.True(t, ok, "expected result to be of type StatusInfo")
	assert.Equal(t, "running", statusInfo.Status, "status should be 'running'")
	require.Nil(t, statusInfo.CompletionPercentage, "expected CompletionPercentage to be nil")

	percent := float64(20.0)
	mockSyncer.EXPECT().GetCompletionPercentage().Return(&percent).Once()
	result, err = rpc.Status()
	require.NoError(t, err)
	statusInfo, ok = result.(StatusInfo)
	require.True(t, ok, "expected result to be of type StatusInfo")
	assert.Equal(t, "running", statusInfo.Status, "status should be 'running'")
	require.NotNil(t, statusInfo.CompletionPercentage, "expected CompletionPercentage to not be nil")
	assert.Equal(t, percent, *statusInfo.CompletionPercentage, "expected CompletionPercentage to match the mock value")
}

func TestL1InfoTreeSyncRPC_GetInfoByGlobalExitRoot_NilParam_Success(t *testing.T) {
	mockSyncer := NewL1InfoTreeSyncerMock(t)
	mockSyncer.EXPECT().GetLatestL1InfoLeaf(mock.Anything).
		Return(&L1InfoTreeLeaf{Hash: testHash}, nil).
		Once()
	rpc := NewL1InfoTreeSyncRPC(log.WithFields("modules", "test"), mockSyncer)

	result, err := rpc.GetInfoByGlobalExitRoot(nil)
	require.Nil(t, err, "expected no error")
	leaf, ok := result.(*L1InfoTreeLeaf)
	require.True(t, ok, "expected result to be *L1InfoTreeLeaf")
	assert.Equal(t, testHash, leaf.Hash)
}

func TestL1InfoTreeSyncRPC_GetInfoByGlobalExitRoot_Param_Success(t *testing.T) {
	mockSyncer := NewL1InfoTreeSyncerMock(t)
	mockSyncer.EXPECT().GetInfoByGlobalExitRoot(mock.Anything).
		Return(&L1InfoTreeLeaf{Hash: testHash}, nil).
		Once()
	rpc := NewL1InfoTreeSyncRPC(log.WithFields("modules", "test"), mockSyncer)
	param := testHash.Hex()
	result, err := rpc.GetInfoByGlobalExitRoot(&param)
	require.Nil(t, err, "expected no error")
	leaf, ok := result.(*L1InfoTreeLeaf)
	require.True(t, ok, "expected result to be *L1InfoTreeLeaf")
	assert.Equal(t, testHash, leaf.Hash)
}

func TestL1InfoTreeSyncRPC_GetInfoByRoot_NilParam_Success(t *testing.T) {
	mockSyncer := NewL1InfoTreeSyncerMock(t)
	mockSyncer.EXPECT().GetLatestL1InfoLeaf(mock.Anything).
		Return(&L1InfoTreeLeaf{Hash: testHash}, nil).
		Once()
	rpc := NewL1InfoTreeSyncRPC(log.WithFields("modules", "test"), mockSyncer)

	result, err := rpc.GetInfoByRoot(nil)
	require.Nil(t, err, "expected no error")
	leaf, ok := result.(*L1InfoTreeLeaf)
	require.True(t, ok, "expected result to be *L1InfoTreeLeaf")
	assert.Equal(t, testHash, leaf.Hash)
}

func TestL1InfoTreeSyncRPC_GetInfoByRoot_Param_Success(t *testing.T) {
	mockSyncer := NewL1InfoTreeSyncerMock(t)
	mockSyncer.EXPECT().GetInfoByRoot(mock.Anything).
		Return(&L1InfoTreeLeaf{Hash: testHash}, nil).
		Once()
	rpc := NewL1InfoTreeSyncRPC(log.WithFields("modules", "test"), mockSyncer)
	param := testHash.Hex()
	result, err := rpc.GetInfoByRoot(&param)
	require.Nil(t, err, "expected no error")
	leaf, ok := result.(*L1InfoTreeLeaf)
	require.True(t, ok, "expected result to be *L1InfoTreeLeaf")
	assert.Equal(t, testHash, leaf.Hash)
}
