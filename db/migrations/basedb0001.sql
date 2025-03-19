-- +migrate Down
DROP TABLE IF EXISTS key_value;
-- +migrate Up
CREATE TABLE IF NOT EXISTS key_value (
    owner VARCHAR(254) NOT NULL,
    key VARCHAR(254) NOT NULL,
    value VARCHAR, 
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (key, owner)
);