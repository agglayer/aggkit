-- +migrate Down
DROP TABLE IF EXISTS resync_counter;

-- +migrate Up
CREATE TABLE resync_counter (
    key VARCHAR PRIMARY KEY,
    value INTEGER NOT NULL
);

