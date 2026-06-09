-- +migrate Down
DROP TABLE IF EXISTS autoclaim_bridge_cursor;
DROP TABLE IF EXISTS autoclaim_transaction_attempt;
DROP TABLE IF EXISTS autoclaim_request;
-- +migrate Up
CREATE TABLE autoclaim_request (
    request_key                  TEXT PRIMARY KEY,
    origin_network               INTEGER NOT NULL,
    destination_network          INTEGER NOT NULL,
    deposit_count                INTEGER NOT NULL,
    status                       TEXT NOT NULL,
    policy_result                TEXT,
    bridge_tx_hash               TEXT NOT NULL,
    claim_tx_hash                TEXT,
    tx_manager_id                TEXT,
    block_num                    INTEGER NOT NULL,
    block_pos                    INTEGER NOT NULL,
    global_index                 TEXT,
    l1_info_tree_index           INTEGER,
    retry_count                  INTEGER NOT NULL DEFAULT 0,
    max_retries                  INTEGER NOT NULL DEFAULT 0,
    last_observed_send_at        TIMESTAMP,
    last_observed_result_at      TIMESTAMP,
    created_at                   TIMESTAMP NOT NULL,
    updated_at                   TIMESTAMP NOT NULL,
    last_error                   TEXT NOT NULL DEFAULT '',
    bridge_json                  BLOB NOT NULL,
    proof_json                   BLOB,
    policy_decision_json         BLOB,
    manual_decision_json         BLOB,
    UNIQUE(origin_network, destination_network, deposit_count)
);

CREATE INDEX idx_autoclaim_request_status ON autoclaim_request(status);
CREATE INDEX idx_autoclaim_request_destination_status ON autoclaim_request(destination_network, status);
CREATE INDEX idx_autoclaim_request_policy_result ON autoclaim_request(policy_result);
CREATE INDEX idx_autoclaim_request_bridge_tx_hash ON autoclaim_request(bridge_tx_hash);
CREATE INDEX idx_autoclaim_request_claim_tx_hash ON autoclaim_request(claim_tx_hash);
CREATE INDEX idx_autoclaim_request_block_num ON autoclaim_request(block_num);
CREATE INDEX idx_autoclaim_request_updated_at ON autoclaim_request(updated_at);

CREATE TABLE autoclaim_transaction_attempt (
    request_key                  TEXT NOT NULL REFERENCES autoclaim_request(request_key) ON DELETE CASCADE,
    attempt_number               INTEGER NOT NULL,
    claimer_id                   TEXT NOT NULL,
    tx_manager_id                TEXT NOT NULL,
    claim_tx_hash                TEXT NOT NULL,
    status                       TEXT NOT NULL,
    status_reason                TEXT NOT NULL DEFAULT '',
    retry_count                  INTEGER NOT NULL DEFAULT 0,
    max_retries                  INTEGER NOT NULL DEFAULT 0,
    sent_at                      TIMESTAMP,
    confirmed_at                 TIMESTAMP,
    last_observed_at             TIMESTAMP,
    created_at                   TIMESTAMP NOT NULL,
    updated_at                   TIMESTAMP NOT NULL,
    last_error                   TEXT NOT NULL DEFAULT '',
    transaction_data             BLOB,
    target_bridge_addr           TEXT NOT NULL,
    attempt_json                 BLOB NOT NULL,
    PRIMARY KEY (request_key, attempt_number)
);

CREATE INDEX idx_autoclaim_attempt_request ON autoclaim_transaction_attempt(request_key);
CREATE INDEX idx_autoclaim_attempt_tx_manager ON autoclaim_transaction_attempt(tx_manager_id);
CREATE INDEX idx_autoclaim_attempt_claim_tx_hash ON autoclaim_transaction_attempt(claim_tx_hash);

CREATE TABLE autoclaim_bridge_cursor (
    cursor_name                  TEXT PRIMARY KEY,
    from_block                   INTEGER NOT NULL,
    to_block                     INTEGER NOT NULL,
    block_num                    INTEGER NOT NULL,
    block_pos                    INTEGER NOT NULL,
    updated_at                   TIMESTAMP NOT NULL
);
