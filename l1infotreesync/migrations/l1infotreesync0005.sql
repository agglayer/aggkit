-- +migrate Down
DROP TABLE IF EXISTS l1info_checkpoint;

-- +migrate Up
CREATE TABLE l1info_checkpoint (
    -- single_row_id prevents having more than 1 row in this table
    single_row_id INTEGER check(single_row_id=1) NOT NULL DEFAULT 1,
    -- block_num is the last block whose UpdateL1InfoTreeV2 sanity check passed, proving the
    -- local L1 info tree was consistent with L1 as of that event.
    block_num     INTEGER NOT NULL,
    PRIMARY KEY (single_row_id)
);
