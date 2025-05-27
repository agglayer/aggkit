package claimsponsor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/agglayer/aggkit/claimsponsor/migrations"
	"github.com/agglayer/aggkit/db"
	"github.com/agglayer/aggkit/log"
	"github.com/agglayer/aggkit/sync"
	tree "github.com/agglayer/aggkit/tree/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

type ClaimStatus string

const (
	PendingClaimStatus ClaimStatus = "pending"
	WIPClaimStatus     ClaimStatus = "work in progress"
	SuccessClaimStatus ClaimStatus = "success"
	FailedClaimStatus  ClaimStatus = "failed"
)

// AlreadyClaimed(): 0x646cf558
const alreadyClaimedRevertCode = "0x646cf558"

var (
	ErrInvalidClaim     = errors.New("invalid claim")
	ErrClaimDoesntExist = errors.New("the claim requested to be updated does not exist")
)

// Claim representation of a claim event
type Claim struct {
	LeafType            uint8          `meddler:"leaf_type"                         json:"leaf_type"`
	ProofLocalExitRoot  tree.Proof     `meddler:"proof_local_exit_root,merkleproof" json:"proof_local_exit_root"`
	ProofRollupExitRoot tree.Proof     `meddler:"proof_rollup_exit_root,merkleproof" json:"proof_rollup_exit_root"`
	GlobalIndex         *big.Int       `meddler:"global_index,bigint"               json:"global_index,string"`
	MainnetExitRoot     common.Hash    `meddler:"mainnet_exit_root,hash"            json:"mainnet_exit_root"`
	RollupExitRoot      common.Hash    `meddler:"rollup_exit_root,hash"             json:"rollup_exit_root"`
	OriginNetwork       uint32         `meddler:"origin_network"                    json:"origin_network"`
	OriginTokenAddress  common.Address `meddler:"origin_token_address,address"      json:"origin_token_address"`
	DestinationNetwork  uint32         `meddler:"destination_network"               json:"destination_network"`
	DestinationAddress  common.Address `meddler:"destination_address,address"       json:"destination_address"`
	Amount              *big.Int       `meddler:"amount,bigint"                     json:"amount,string"`
	Metadata            []byte         `meddler:"metadata"                          json:"metadata"`
	Status              ClaimStatus    `meddler:"status"                            json:"status"`
	TxID                string         `meddler:"tx_id"                             json:"tx_id"`
}

func (c *Claim) Key() []byte {
	return c.GlobalIndex.Bytes()
}

type ClaimSender interface {
	checkClaim(ctx context.Context, claim *Claim, data []byte) error
	sendClaim(ctx context.Context, claim *Claim) (string, error)
	claimStatus(ctx context.Context, id string) (ClaimStatus, error)
}

type ClaimSponsor struct {
	logger                *log.Logger
	db                    *sql.DB
	sender                ClaimSender
	rh                    *sync.RetryHandler
	waitTxToBeMinedPeriod time.Duration
	waitOnEmptyQueue      time.Duration
}

func newClaimSponsor(
	logger *log.Logger,
	dbPath string,
	sender ClaimSender,
	retryAfterErrorPeriod time.Duration,
	maxRetryAttemptsAfterError int,
	waitTxToBeMinedPeriod time.Duration,
	waitOnEmptyQueue time.Duration,
) (*ClaimSponsor, error) {
	err := migrations.RunMigrations(dbPath)
	if err != nil {
		return nil, err
	}
	db, err := db.NewSQLiteDB(dbPath)
	if err != nil {
		return nil, err
	}
	rh := &sync.RetryHandler{
		MaxRetryAttemptsAfterError: maxRetryAttemptsAfterError,
		RetryAfterErrorPeriod:      retryAfterErrorPeriod,
	}

	return &ClaimSponsor{
		logger:                logger,
		db:                    db,
		sender:                sender,
		rh:                    rh,
		waitTxToBeMinedPeriod: waitTxToBeMinedPeriod,
		waitOnEmptyQueue:      waitOnEmptyQueue,
	}, nil
}

func (c *ClaimSponsor) Start(ctx context.Context) {
	c.logger.Info("starting claim sponsor service")
	attempts := 0

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("claim sponsor service shutting down")
			return

		default:
			err := c.claim(ctx)
			if err != nil {
				attempts++
				c.logger.Errorf("error in claim sponsor main loop (attempt %d): %v", attempts, err)
				c.rh.Handle("claimsponsor main loop", attempts)
			} else {
				attempts = 0
			}
		}
	}
}

func (c *ClaimSponsor) claim(ctx context.Context) error {
	claim, err := c.getWIPClaim()
	if err != nil && !errors.Is(err, db.ErrNotFound) {
		return fmt.Errorf("error getting WIP claim: %w", err)
	}

	if errors.Is(err, db.ErrNotFound) || claim == nil {
		// there is no WIP claim, go for the next pending claim
		claim, err = c.getFirstPendingClaim()
		if err != nil {
			if errors.Is(err, db.ErrNotFound) {
				c.logger.Debug("no pending claims found in db")
				time.Sleep(c.waitOnEmptyQueue)
				return nil
			}
			return fmt.Errorf("getFirstPendingClaim failed: %w", err)
		}

		claim.TxID, err = c.sender.sendClaim(ctx, claim)
		if err != nil {
			if strings.Contains(err.Error(), alreadyClaimedRevertCode) {
				c.logger.Infof("claim with global index %s already claimed; deleting from queue", claim.GlobalIndex)
				if err := c.deleteClaim(claim.GlobalIndex); err != nil {
					return fmt.Errorf("cleanup delete after AlreadyClaimed: %w", err)
				}
				return nil
			} else if errors.Is(err, ErrGasEstimateTooHigh) {
				c.logger.Infof("Trx GlobalIndex: %s evicting from db, %s", claim.GlobalIndex, err.Error())
				if err := c.deleteClaim(claim.GlobalIndex); err != nil {
					return fmt.Errorf("cleanup delete after gas too high: %w", err)
				}
				return nil
			}
			return fmt.Errorf("error sending claim: %w", err)
		}

		if err := c.updateClaimStatus(claim.GlobalIndex, WIPClaimStatus); err != nil {
			return fmt.Errorf("error setting claim %s → WIP: %w", claim.GlobalIndex, err)
		}
		if err := c.updateClaimTxID(claim.GlobalIndex, claim.TxID); err != nil {
			return fmt.Errorf("error updating claim txID: %w", err)
		}
	}

	c.logger.Infof("waiting for transaction %s (global index %s) to be processed", claim.TxID, claim.GlobalIndex.String())
	status, err := c.waitForTxResult(ctx, claim.TxID)
	if err != nil {
		return fmt.Errorf("error calling waitForTxResult for tx %s: %w", claim.TxID, err)
	}
	c.logger.Infof(
		"transaction %s (global index %s) processed with status: %s",
		claim.TxID,
		claim.GlobalIndex.String(),
		status,
	)
	return c.updateClaimStatus(claim.GlobalIndex, status)
}

func (c *ClaimSponsor) getWIPClaim() (*Claim, error) {
	claim := &Claim{}
	err := meddler.QueryRow(
		c.db, claim,
		`SELECT * FROM claim WHERE status = $1 ORDER BY rowid ASC LIMIT 1;`,
		WIPClaimStatus,
	)
	return claim, db.ReturnErrNotFound(err)
}

func (c *ClaimSponsor) getFirstPendingClaim() (*Claim, error) {
	claim := &Claim{}
	err := meddler.QueryRow(
		c.db, claim,
		`SELECT * FROM claim WHERE status = $1 ORDER BY rowid ASC LIMIT 1;`,
		PendingClaimStatus,
	)
	return claim, db.ReturnErrNotFound(err)
}

func (c *ClaimSponsor) updateClaimTxID(globalIndex *big.Int, txID string) error {
	res, err := c.db.Exec(
		`UPDATE claim SET tx_id = $1 WHERE global_index = $2`,
		txID, globalIndex.String(),
	)
	if err != nil {
		return fmt.Errorf("error updating claim status: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rowsAff == 0 {
		return ErrClaimDoesntExist
	}
	return nil
}

func (c *ClaimSponsor) updateClaimStatus(globalIndex *big.Int, status ClaimStatus) error {
	res, err := c.db.Exec(
		`UPDATE claim SET status = $1 WHERE global_index = $2`,
		status, globalIndex.String(),
	)
	if err != nil {
		return fmt.Errorf("error updating claim status: %w", err)
	}
	rowsAff, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("error getting rows affected: %w", err)
	}
	if rowsAff == 0 {
		return ErrClaimDoesntExist
	}
	return nil
}

func (c *ClaimSponsor) waitForTxResult(ctx context.Context, txID string) (ClaimStatus, error) {
	ticker := time.NewTicker(c.waitTxToBeMinedPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", errors.New("context cancelled")
		case <-ticker.C:
			status, err := c.sender.claimStatus(ctx, txID)
			if err != nil {
				return "", err
			}

			if status == FailedClaimStatus || status == SuccessClaimStatus {
				return status, nil
			}
		}
	}
}

func (c *ClaimSponsor) AddClaimToQueue(claim *Claim) error {
	claim.Status = PendingClaimStatus
	return meddler.Insert(c.db, "claim", claim)
}

func (c *ClaimSponsor) GetClaim(globalIndex *big.Int) (*Claim, error) {
	claim := &Claim{}
	err := meddler.QueryRow(
		c.db, claim, `SELECT * FROM claim WHERE global_index = $1`, globalIndex.String(),
	)
	if err != nil {
		return nil, db.ReturnErrNotFound(err)
	}
	return claim, nil
}

func (c *ClaimSponsor) deleteClaim(globalIndex *big.Int) error {
	res, err := c.db.Exec(
		`DELETE FROM claim WHERE global_index = $1`,
		globalIndex.String(),
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrClaimDoesntExist
	}
	return nil
}
