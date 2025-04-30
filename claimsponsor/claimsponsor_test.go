package claimsponsor

import (
	"fmt"
	"math/big"
	"path"
	"testing"
	"time"

	"github.com/agglayer/aggkit/log"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
	"github.com/stretchr/testify/require"
)

const (
	testRetryAfterErrorPeriod      = 2 * time.Second
	testMaxRetryAttemptsAfterError = 3
	testWaitTxToBeMinedPeriod      = 5 * time.Second
	testWaitOnEmptyQueue           = 10 * time.Second
)

func TestAddClaimToQueue(t *testing.T) {
	testDir := path.Join(t.TempDir(), "claimsponsor_TestAddClaimToQueue.sqlite")
	logger := log.GetDefaultLogger()
	c, err := newClaimSponsor(logger, testDir, nil, TestRetryAfterErrorPeriod, TestMaxRetryAttemptsAfterError, TestWaitTxToBeMinedPeriod, TestWaitOnEmptyQueue)
	require.NoError(t, err)

	claim := &Claim{
		LeafType:            1,
		ProofLocalExitRoot:  tree.Proof{},
		ProofRollupExitRoot: tree.Proof{},
		GlobalIndex:         big.NewInt(1),
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		OriginNetwork:       1,
		OriginTokenAddress:  common.Address{},
		DestinationNetwork:  2,
		DestinationAddress:  common.Address{},
		Amount:              big.NewInt(100),
		TxID:                "test-tx-id",
	}

	err = c.AddClaimToQueue(claim)
	require.NoError(t, err)

	claimDB := &Claim{}
	err = meddler.QueryRow(
		c.db, claimDB, `SELECT * FROM claim`,
	)
	require.NoError(t, err)
	require.Equal(t, claim, claimDB)
}

func TestGetClaim(t *testing.T) {
	testDir := path.Join(t.TempDir(), "claimsponsor_TestGetClaim.sqlite")
	logger := log.GetDefaultLogger()
	c, err := newClaimSponsor(logger, testDir, nil, TestRetryAfterErrorPeriod, TestMaxRetryAttemptsAfterError, TestWaitTxToBeMinedPeriod, TestWaitOnEmptyQueue)
	require.NoError(t, err)

	claim := Claim{
		LeafType:            1,
		ProofLocalExitRoot:  tree.Proof{},
		ProofRollupExitRoot: tree.Proof{},
		GlobalIndex:         big.NewInt(1),
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		OriginNetwork:       1,
		OriginTokenAddress:  common.Address{},
		DestinationNetwork:  2,
		DestinationAddress:  common.Address{},
		Amount:              big.NewInt(100),
		TxID:                "test-tx-id",
		Status:              PendingClaimStatus,
	}

	// Add claim to queue
	err = meddler.Insert(c.db, "claim", &claim)
	require.NoError(t, err)

	claimResp, err := c.GetClaim(claim.GlobalIndex)
	require.NoError(t, err)
	fmt.Printf("%+v", *claimResp)

	// try to find a claim that doesn't exist
	claimResp, err = c.GetClaim(big.NewInt(999))
	require.Error(t, err)
	require.Nil(t, claimResp)
}

func TestGetWIPClaim(t *testing.T) {
	testDir := path.Join(t.TempDir(), "claimsponsor_TestGetWIPClaim.sqlite")
	logger := log.GetDefaultLogger()
	c, err := newClaimSponsor(logger, testDir, nil, TestRetryAfterErrorPeriod, TestMaxRetryAttemptsAfterError, TestWaitTxToBeMinedPeriod, TestWaitOnEmptyQueue)
	require.NoError(t, err)

	claim := Claim{
		LeafType:            1,
		ProofLocalExitRoot:  tree.Proof{},
		ProofRollupExitRoot: tree.Proof{},
		GlobalIndex:         big.NewInt(1),
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		OriginNetwork:       1,
		OriginTokenAddress:  common.Address{},
		DestinationNetwork:  2,
		DestinationAddress:  common.Address{},
		Amount:              big.NewInt(100),
		TxID:                "test-tx-id",
		Status:              WIPClaimStatus,
	}

	err = meddler.Insert(c.db, "claim", &claim)
	require.NoError(t, err)

	claimResp, err := c.getWIPClaim()
	require.NoError(t, err)
	require.Equal(t, claim.GlobalIndex, claimResp.GlobalIndex)
	require.Equal(t, claim.Status, claimResp.Status)
	require.Equal(t, claim.TxID, claimResp.TxID)
}

func TestGetFirstPendingClaim(t *testing.T) {
	testDir := path.Join(t.TempDir(), "claimsponsor_TestGetFirstPendingClaim.sqlite")
	logger := log.GetDefaultLogger()
	c, err := newClaimSponsor(logger, testDir, nil, TestRetryAfterErrorPeriod, TestMaxRetryAttemptsAfterError, TestWaitTxToBeMinedPeriod, TestWaitOnEmptyQueue)
	require.NoError(t, err)

	claim := Claim{
	claim := &Claim{
		LeafType:            1,
		ProofLocalExitRoot:  tree.Proof{},
		ProofRollupExitRoot: tree.Proof{},
		GlobalIndex:         big.NewInt(1),
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		OriginNetwork:       1,
		OriginTokenAddress:  common.Address{},
		DestinationNetwork:  2,
		DestinationAddress:  common.Address{},
		Amount:              big.NewInt(100),
		TxID:                "test-tx-id",
		Status:              PendingClaimStatus,
	}

	err = meddler.Insert(c.db, "claim", claim)
	require.NoError(t, err)

	claimResp, err := c.getFirstPendingClaim()
	require.NoError(t, err)
	require.Equal(t, claim, claimResp)

func Test_updateClaimTxID(t *testing.T) {
	testDir := path.Join(t.TempDir(), "claimsponsor_Test_updateClaimTxID.sqlite")
	logger := log.GetDefaultLogger()
	c, err := newClaimSponsor(logger, testDir, nil, TestRetryAfterErrorPeriod, TestMaxRetryAttemptsAfterError, TestWaitTxToBeMinedPeriod, TestWaitOnEmptyQueue)
	require.NoError(t, err)

	claim := Claim{
		LeafType:            1,
		ProofLocalExitRoot:  tree.Proof{},
		ProofRollupExitRoot: tree.Proof{},
		GlobalIndex:         big.NewInt(1),
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		OriginNetwork:       1,
		OriginTokenAddress:  common.Address{},
		DestinationNetwork:  2,
		DestinationAddress:  common.Address{},
		Amount:              big.NewInt(100),
		TxID:                "test-tx-id",
		Status:              PendingClaimStatus,
	}

	err = meddler.Insert(c.db, "claim", &claim)
	require.NoError(t, err)

	newTxID := "new-tx-id"
	err = c.updateClaimTxID(claim.GlobalIndex, newTxID)
	require.NoError(t, err)

	claimResp, err := c.GetClaim(claim.GlobalIndex)
	require.NoError(t, err)
	require.Equal(t, newTxID, claimResp.TxID)
	require.Equal(t, PendingClaimStatus, claimResp.Status)
}

func Test_updateClaimStatus(t *testing.T) {
	testDir := path.Join(t.TempDir(), "claimsponsor_Test_updateClaimStatus.sqlite")
	logger := log.GetDefaultLogger()
	c, err := newClaimSponsor(logger, testDir, nil, TestRetryAfterErrorPeriod, TestMaxRetryAttemptsAfterError, TestWaitTxToBeMinedPeriod, TestWaitOnEmptyQueue)
	require.NoError(t, err)

	claim := Claim{
		LeafType:            1,
		ProofLocalExitRoot:  tree.Proof{},
		ProofRollupExitRoot: tree.Proof{},
		GlobalIndex:         big.NewInt(1),
		MainnetExitRoot:     common.Hash{},
		RollupExitRoot:      common.Hash{},
		OriginNetwork:       1,
		OriginTokenAddress:  common.Address{},
		DestinationNetwork:  2,
		DestinationAddress:  common.Address{},
		Amount:              big.NewInt(100),
		TxID:                "test-tx-id",
		Status:              PendingClaimStatus,
	}

	err = meddler.Insert(c.db, "claim", &claim)
	require.NoError(t, err)

	newStatus := SuccessClaimStatus
	err = c.updateClaimStatus(claim.GlobalIndex, newStatus)
	require.NoError(t, err)

	claimResp, err := c.GetClaim(claim.GlobalIndex)
	require.NoError(t, err)
	require.Equal(t, newStatus, claimResp.Status)
}
