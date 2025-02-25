-- +migrate Down
DROP TABLE IF EXISTS reorg_event;

-- +migrate Up
CREATE TABLE reorg_event (
    detected_at  BIGINT NOT NULL,
    from_block  BIGINT NOT NULL,
    to_block    BIGINT NOT NULL,
    subscriber_id  VARCHAR,
    current_hash VARCHAR,
    tracked_hash VARCHAR,
    version string,
    extra_data  VARCHAR
)