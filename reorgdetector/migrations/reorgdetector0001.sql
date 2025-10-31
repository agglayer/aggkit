-- +migrate Down
DROP TABLE IF EXISTS tracked_block;

-- +migrate Up
CREATE TABLE tracked_block (
	subscriber_id VARCHAR NOT NULL,
	num           BIGINT NOT NULL,
	hash          VARCHAR NOT NULL
);
