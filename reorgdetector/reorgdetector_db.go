package reorgdetector

import (
	context "context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/db"
	common "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

// getTrackedBlocks returns a list of tracked blocks for each subscriber from db
func (rd *ReorgDetector) getTrackedBlocks() (map[string]*headersList, error) {
	trackedBlocks := make(map[string]*headersList, 0)
	var headersWithID []*headerWithSubscriberID
	err := meddler.QueryAll(rd.db, &headersWithID, "SELECT * FROM tracked_block ORDER BY subscriber_id;")
	if err != nil {
		if errors.Is(err, db.ErrNotFound) {
			return trackedBlocks, nil
		}
		return nil, fmt.Errorf("error queryng tracked_block: %w", err)
	}
	if len(headersWithID) == 0 {
		return trackedBlocks, nil
	}
	currentID := headersWithID[0].SubscriberID
	currentHeaders := []header{}
	for _, row := range headersWithID {
		// If the subscriber ID changes, save the current group
		if row.SubscriberID != currentID {
			trackedBlocks[currentID] = newHeadersList(currentHeaders...)
			currentID = row.SubscriberID
			currentHeaders = nil
		}

		currentHeaders = append(currentHeaders, header{
			Num:  row.Num,
			Hash: row.Hash,
		})
	}

	// Add the final group of headers after the loop
	trackedBlocks[currentID] = newHeadersList(currentHeaders...)

	return trackedBlocks, nil
}

// saveTrackedBlock saves the tracked block for a subscriber in db and in memory
func (rd *ReorgDetector) saveTrackedBlock(id string, b header) error {
	rd.trackedBlocksLock.Lock()
	hdrs, ok := rd.trackedBlocks[id]
	if !ok || hdrs.isEmpty() {
		hdrs = newHeadersList(b)
		rd.trackedBlocks[id] = hdrs
	} else {
		hdrs.add(b)
	}

	rd.log.Debugf("Tracking block %d for subscriber %s", b.Num, id)

	rd.trackedBlocksLock.Unlock()
	return meddler.Insert(rd.db, "tracked_block", &headerWithSubscriberID{
		SubscriberID: id,
		Num:          b.Num,
		Hash:         b.Hash,
	})
}

// updateTrackedBlocksDB updates the tracked blocks for a subscriber in db
func (rd *ReorgDetector) removeTrackedBlockRange(id string, fromBlock, toBlock uint64) error {
	_, err := rd.db.Exec(
		"DELETE FROM tracked_block WHERE num >= $1 AND num <= $2 AND subscriber_id = $3;",
		fromBlock, toBlock, id,
	)
	return err
}

type ReorgEvent struct {
	DetectedAt   int64       `meddler:"detected_at"`
	FromBlock    uint64      `meddler:"from_block"`
	ToBlock      uint64      `meddler:"to_block"`
	SubscriberID string      `meddler:"subscriber_id"`
	TrackedHash  common.Hash `meddler:"tracked_hash,hash"`
	CurrentHash  common.Hash `meddler:"current_hash,hash"`
	Version      string      `meddler:"version"`
}

func (rd *ReorgDetector) insertReorgEvent(event ReorgEvent) error {
	if event.Version == "" {
		event.Version = aggkit.GetVersion().Brief()
	}
	return meddler.Insert(rd.db, "reorg_event", &event)
}

// GetLastReorgEvent returns the the last ReorgEvent stored in reorg_event table
func (rd *ReorgDetector) GetLastReorgEvent(ctx context.Context) (ReorgEvent, error) {
	query := `SELECT * FROM reorg_event ORDER BY detected_at DESC LIMIT 1;`
	var rEvent ReorgEvent
	if err := meddler.QueryRow(rd.db, &rEvent, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReorgEvent{}, nil
		}
		return ReorgEvent{}, err
	}

	return rEvent, nil
}
