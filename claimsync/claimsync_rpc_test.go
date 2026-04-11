package claimsync

import (
	"errors"
	"math/big"
	"testing"

	jRPC "github.com/0xPolygon/cdk-rpc/rpc"
	"github.com/agglayer/aggkit/claimsync/mocks"
	claimsynctypes "github.com/agglayer/aggkit/claimsync/types"
	logger "github.com/agglayer/aggkit/log"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestRPC(t *testing.T) (*ClaimSyncRPC, *mocks.ClaimSyncer) {
	t.Helper()
	syncer := mocks.NewClaimSyncer(t)
	lg := logger.WithFields("module", "test")
	return NewClaimSyncRPC(lg, syncer), syncer
}

// --- Status ---

func TestClaimSyncRPC_Status_OK(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(42), true, nil)
	syncer.EXPECT().GetFirstProcessedBlock(mock.Anything).Return(uint64(10), true, nil)

	result, rpcErr := rpc.Status()
	require.Nil(t, rpcErr)
	require.NotNil(t, result)

	status, ok := result.(struct {
		FirstProcessedBlock *uint64 `json:"firstProcessedBlock,omitempty"`
		LastProcessedBlock  *uint64 `json:"lastProcessedBlock"`
	})
	require.True(t, ok)
	require.NotNil(t, status.FirstProcessedBlock)
	require.Equal(t, uint64(10), *status.FirstProcessedBlock)
	require.NotNil(t, status.LastProcessedBlock)
	require.Equal(t, uint64(42), *status.LastProcessedBlock)
}

func TestClaimSyncRPC_Status_Error(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().GetLastProcessedBlock(mock.Anything).Return(uint64(0), false, errors.New("db error"))

	result, rpcErr := rpc.Status()
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, jRPC.DefaultErrorCode, rpcErr.ErrorCode())
	require.Contains(t, rpcErr.Error(), "getting last processed block")
}

// --- GetClaims ---

func TestClaimSyncRPC_GetClaims_OK(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	expected := []claimsynctypes.Claim{
		{BlockNum: 1, GlobalIndex: big.NewInt(100)},
		{BlockNum: 2, GlobalIndex: big.NewInt(200)},
	}
	syncer.EXPECT().GetClaims(mock.Anything, uint64(1), uint64(10)).Return(expected, nil)

	result, rpcErr := rpc.GetClaims(1, 10)
	require.Nil(t, rpcErr)
	require.Equal(t, expected, result)
}

func TestClaimSyncRPC_GetClaims_Empty(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().GetClaims(mock.Anything, uint64(1), uint64(10)).Return([]claimsynctypes.Claim{}, nil)

	result, rpcErr := rpc.GetClaims(1, 10)
	require.Nil(t, rpcErr)
	require.Equal(t, []claimsynctypes.Claim{}, result)
}

func TestClaimSyncRPC_GetClaims_Error(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().GetClaims(mock.Anything, uint64(1), uint64(10)).Return(nil, errors.New("storage error"))

	result, rpcErr := rpc.GetClaims(1, 10)
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, jRPC.DefaultErrorCode, rpcErr.ErrorCode())
	require.Contains(t, rpcErr.Error(), "ClaimSyncRPC.GetClaims")
}

// --- GetClaimsByGlobalIndex ---

func TestClaimSyncRPC_GetClaimsByGlobalIndex_OK(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	expected := []claimsynctypes.Claim{{BlockNum: 5, GlobalIndex: big.NewInt(123)}}
	syncer.EXPECT().GetClaimsByGlobalIndex(mock.Anything, big.NewInt(123)).Return(expected, nil)

	result, rpcErr := rpc.GetClaimsByGlobalIndex("123")
	require.Nil(t, rpcErr)
	require.Equal(t, expected, result)
}

func TestClaimSyncRPC_GetClaimsByGlobalIndex_InvalidInput(t *testing.T) {
	rpc, _ := newTestRPC(t)

	result, rpcErr := rpc.GetClaimsByGlobalIndex("not-a-number")
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, jRPC.DefaultErrorCode, rpcErr.ErrorCode())
	require.Contains(t, rpcErr.Error(), "invalid global index")
}

func TestClaimSyncRPC_GetClaimsByGlobalIndex_NotFound(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().GetClaimsByGlobalIndex(mock.Anything, big.NewInt(999)).Return([]claimsynctypes.Claim{}, nil)

	result, rpcErr := rpc.GetClaimsByGlobalIndex("999")
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, jRPC.NotFoundErrorCode, rpcErr.ErrorCode())
	require.Contains(t, rpcErr.Error(), "no claims found")
}

func TestClaimSyncRPC_GetClaimsByGlobalIndex_Error(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().GetClaimsByGlobalIndex(mock.Anything, big.NewInt(1)).Return(nil, errors.New("db error"))

	result, rpcErr := rpc.GetClaimsByGlobalIndex("1")
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, jRPC.DefaultErrorCode, rpcErr.ErrorCode())
	require.Contains(t, rpcErr.Error(), "ClaimSyncRPC.GetClaimsByGlobalIndex")
}

// --- SetNextRequiredBlock ---

func TestClaimSyncRPC_SetNextRequiredBlock_OK(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().SetNextRequiredBlock(mock.Anything, uint64(500)).Return(nil)

	result, rpcErr := rpc.SetNextRequiredBlock(500)
	require.Nil(t, rpcErr)
	require.NotNil(t, result)

	msg, ok := result.(struct {
		Message string `json:"message"`
	})
	require.True(t, ok)
	require.Equal(t, "next required block set to 500", msg.Message)
}

func TestClaimSyncRPC_SetNextRequiredBlock_Error(t *testing.T) {
	rpc, syncer := newTestRPC(t)
	syncer.EXPECT().SetNextRequiredBlock(mock.Anything, uint64(500)).Return(errors.New("forbidden"))

	result, rpcErr := rpc.SetNextRequiredBlock(500)
	require.Nil(t, result)
	require.NotNil(t, rpcErr)
	require.Equal(t, jRPC.DefaultErrorCode, rpcErr.ErrorCode())
	require.Contains(t, rpcErr.Error(), "ClaimSyncRPC.SetNextRequiredBlock")
}
