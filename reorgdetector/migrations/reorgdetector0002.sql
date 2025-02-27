-- +migrate Down
DROP TABLE IF EXISTS reorg_event;

-- +migrate Up
CREATE TABLE reorg_event (
    detected_at  INTEGER NOT NULL,
    from_block  BIGINT NOT NULL,
    to_block    BIGINT NOT NULL,
    subscriber_id  VARCHAR NOT NULL,
    current_hash VARCHAR,
    tracked_hash VARCHAR,
    version VARCHAR,
    extra_data  VARCHAR,
    PRIMARY KEY (detected_at, subscriber_id, from_block, to_block)
)