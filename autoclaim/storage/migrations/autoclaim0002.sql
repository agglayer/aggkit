-- +migrate Down
DROP TABLE IF EXISTS autoclaim_bridge_cursor;
-- +migrate Up
CREATE TABLE autoclaim_bridge_cursor (
    cursor_name                 TEXT PRIMARY KEY,
    from_block                  INTEGER NOT NULL,
    to_block                    INTEGER NOT NULL,
    block_num                   INTEGER NOT NULL,
    block_pos                   INTEGER NOT NULL,
    updated_at                  TIMESTAMP NOT NULL
);
