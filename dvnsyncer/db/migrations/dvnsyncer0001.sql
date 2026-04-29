-- +migrate Down
DROP TABLE IF EXISTS dvn_job_assigned;
DROP TABLE IF EXISTS lz_packet;

-- +migrate Up
CREATE TABLE IF NOT EXISTS lz_packet (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id        INTEGER NOT NULL,
    block_num       INTEGER NOT NULL,
    tx_hash         TEXT NOT NULL,
    log_index       INTEGER NOT NULL,
    src_eid         INTEGER NOT NULL,
    sender          TEXT NOT NULL,
    dst_eid         INTEGER NOT NULL,
    receiver        TEXT NOT NULL,
    nonce           INTEGER NOT NULL,
    guid            TEXT NOT NULL,
    message         BLOB,
    payload_hash    TEXT NOT NULL,
    global_index    TEXT,
    oft_send_to     TEXT,
    oft_amount_sd   INTEGER,
    UNIQUE(chain_id, tx_hash, log_index)
);

CREATE TABLE IF NOT EXISTS dvn_job_assigned (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    chain_id        INTEGER NOT NULL,
    block_num       INTEGER NOT NULL,
    tx_hash         TEXT NOT NULL,
    log_index       INTEGER NOT NULL,
    payload_hash    TEXT NOT NULL,
    dst_eid         INTEGER NOT NULL,
    sender          TEXT NOT NULL,
    fee             TEXT NOT NULL,
    confirmations   INTEGER NOT NULL,
    UNIQUE(chain_id, tx_hash, log_index)
);
