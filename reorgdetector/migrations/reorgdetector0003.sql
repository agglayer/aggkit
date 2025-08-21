-- +migrate Down
DROP INDEX IF EXISTS idx_tracked_block_subscriber_id;
DROP INDEX IF EXISTS idx_tracked_block_num;
DROP INDEX IF EXISTS idx_tracked_block_hash;
DROP INDEX IF EXISTS idx_tracked_block_subscriber_num;
DROP INDEX IF EXISTS idx_reorg_event_detected_at;
DROP INDEX IF EXISTS idx_reorg_event_subscriber_id;
DROP INDEX IF EXISTS idx_reorg_event_from_block;
DROP INDEX IF EXISTS idx_reorg_event_to_block;

-- +migrate Up
-- Tracked block table indexes
CREATE INDEX idx_tracked_block_subscriber_id ON tracked_block(subscriber_id);
CREATE INDEX idx_tracked_block_num ON tracked_block(num);
CREATE INDEX idx_tracked_block_hash ON tracked_block(hash);

-- Composite index for subscriber queries
CREATE INDEX idx_tracked_block_subscriber_num ON tracked_block(subscriber_id, num);

-- Reorg event table indexes
CREATE INDEX idx_reorg_event_detected_at ON reorg_event(detected_at DESC);
CREATE INDEX idx_reorg_event_subscriber_id ON reorg_event(subscriber_id);
CREATE INDEX idx_reorg_event_from_block ON reorg_event(from_block);
CREATE INDEX idx_reorg_event_to_block ON reorg_event(to_block);
