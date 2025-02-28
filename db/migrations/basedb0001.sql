-- +migrate Down
DROP TABLE IF EXISTS bound_data;
-- +migrate Up
CREATE TABLE IF NOT EXISTS bound_data (
   owner VARCHAR(254) NOT NULL,
    key VARCHAR(254) NOT NULL,
    value VARCHAR, 
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (key, owner)
);