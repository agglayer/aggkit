package reorgdetector

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
	for i := 0; i < len(headersWithID); i++ {
		if i == len(headersWithID)-1 {
			currentHeaders = append(currentHeaders, header{
				Num:  headersWithID[i].Num,
				Hash: headersWithID[i].Hash,
			})
			trackedBlocks[currentID] = newHeadersList(currentHeaders...)
		} else if headersWithID[i].SubscriberID != currentID {
			trackedBlocks[currentID] = newHeadersList(currentHeaders...)
			currentHeaders = []header{{
				Num:  headersWithID[i].Num,
				Hash: headersWithID[i].Hash,
			}}
			currentID = headersWithID[i].SubscriberID
		} else {
			currentHeaders = append(currentHeaders, header{
				Num:  headersWithID[i].Num,
				Hash: headersWithID[i].Hash,
			})
		}
	}

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
	DetectedAt   time.Time
	FromBlock    uint64
	ToBlock      uint64
	SubscriberID string
	TrackedHash  common.Hash
	CurrentHash  common.Hash
	ExtraData    interface{}
}

type eventReorgRow struct {
	DetectedAt   int64  `meddler:"detected_at"`
	FromBlock    uint64 `meddler:"from_block"`
	ToBlock      uint64 `meddler:"to_block"`
	SubscriberID string `meddler:"subscriber"`
	TrackedHash  string `meddler:"tracked_hash"`
	CurrentHash  string `meddler:"current_hash"`
	Version      string `meddler:"version"`
	ExtraData    string `meddler:"extra_data"`
}

func (rd *ReorgDetector) insertReorgEvent(event ReorgEvent) error {
	extra, err := json.Marshal(event.ExtraData)
	if err != nil {
		return fmt.Errorf("failed to marshal extra data: %w", err)
	}
	row := eventReorgRow{
		DetectedAt:   event.DetectedAt.Unix(),
		FromBlock:    event.FromBlock,
		ToBlock:      event.ToBlock,
		SubscriberID: event.SubscriberID,
		TrackedHash:  event.TrackedHash.String(),
		CurrentHash:  event.CurrentHash.String(),
		Version:      aggkit.GetVersion().Brief(),
		ExtraData:    string(extra),
	}
	return meddler.Insert(rd.db, "reorg_event", &row)
}
