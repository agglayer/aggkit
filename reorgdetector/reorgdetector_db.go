package reorgdetector

import (
	context "context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/agglayer/aggkit"
	"github.com/agglayer/aggkit/db"
	common "github.com/ethereum/go-ethereum/common"
	"github.com/russross/meddler"
)

// getTrackedBlocks returns a list of tracked blocks for each subscriber from db
func (rd *ReorgDetector) getTrackedBlocks() (map[string]*headersList, error) {
	trackedBlocks := make(map[string]*headersList, 0)
	var headersWithID []*HeaderWithSubscriberID
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
	currentHeaders := []Header{}
	for _, row := range headersWithID {
		// If the subscriber ID changes, save the current group
		if row.SubscriberID != currentID {
			trackedBlocks[currentID] = newHeadersList(currentHeaders...)
			currentID = row.SubscriberID
			currentHeaders = nil
		}

		currentHeaders = append(currentHeaders, Header{
			Num:  row.Num,
			Hash: row.Hash,
		})
	}

	// Add the final group of headers after the loop
	trackedBlocks[currentID] = newHeadersList(currentHeaders...)

	return trackedBlocks, nil
}

// saveTrackedBlock saves the tracked block for a subscriber in db and in memory
func (rd *ReorgDetector) saveTrackedBlock(id string, b Header) error {
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
	return meddler.Insert(rd.db, "tracked_block", &HeaderWithSubscriberID{
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

// GetLastReorgEvent returns the last ReorgEvent stored in reorg_event table, of any subscriber. The events
// detected in the same pass have the same detected_at, so the last one inserted among them is returned: the
// answer is stable for a given DB, but which subscriber it belongs to depends on the order in which the
// subscribers were checked. Use GetLastReorgEventBySubscribers to get the event of specific subscribers
func (rd *ReorgDetector) GetLastReorgEvent(ctx context.Context) (ReorgEvent, error) {
	query := `SELECT * FROM reorg_event ORDER BY detected_at DESC, rowid DESC LIMIT 1;`
	var rEvent ReorgEvent
	if err := meddler.QueryRow(rd.db, &rEvent, query); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReorgEvent{}, nil
		}
		return ReorgEvent{}, err
	}

	return rEvent, nil
}

// GetLastReorgEventBySubscribers returns the reorg of the last detection pass in which any of the given
// subscribers has an event, or an empty ReorgEvent if they have none. The events of a pass share detected_at.
// If more than one of the subscribers has an event in that pass, they are merged into one: FromBlock is the
// lowest and ToBlock the highest of them, and the rest of the fields (SubscriberID, TrackedHash, CurrentHash,
// Version) are the ones of the last event inserted in that pass (highest rowid)
func (rd *ReorgDetector) GetLastReorgEventBySubscribers(ctx context.Context,
	subscriberIDs ...string) (ReorgEvent, error) {
	if len(subscriberIDs) == 0 {
		return ReorgEvent{}, nil
	}
	placeholders := make([]string, len(subscriberIDs))
	args := make([]any, len(subscriberIDs))
	for i, id := range subscriberIDs {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	in := strings.Join(placeholders, ", ")
	// The placeholders are numbered, so the same arguments serve both IN clauses
	query := fmt.Sprintf(`SELECT * FROM reorg_event
		WHERE subscriber_id IN (%s)
			AND detected_at = (SELECT MAX(detected_at) FROM reorg_event WHERE subscriber_id IN (%s))
		ORDER BY rowid DESC;`, in, in)

	var events []*ReorgEvent
	if err := meddler.QueryAll(rd.db, &events, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) || errors.Is(err, db.ErrNotFound) {
			return ReorgEvent{}, nil
		}
		return ReorgEvent{}, err
	}
	if len(events) == 0 {
		return ReorgEvent{}, nil
	}

	merged := *events[0]
	for _, event := range events[1:] {
		merged.FromBlock = min(merged.FromBlock, event.FromBlock)
		merged.ToBlock = max(merged.ToBlock, event.ToBlock)
	}

	return merged, nil
}
