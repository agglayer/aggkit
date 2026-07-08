-- +migrate Down
-- Reverses autoclaim0002: drops the LER cursor table and rebuilds autoclaim_request back to the
-- autoclaim0001 schema (removing source_network/ler/verify_block_num and restoring the
-- origin-network uniqueness constraint). The request_key values are left in their source-based format
-- because the original origin-based keys are not recoverable. The child transaction-attempt table is
-- detached and reattached around the parent rebuild so foreign keys never dangle (dropping a table that
-- is the target of a foreign key is rejected while foreign keys are enforced, even under
-- defer_foreign_keys).
DROP TABLE IF EXISTS autoclaim_ler_cursor;

CREATE TABLE autoclaim_transaction_attempt_tmp (
    request_key                  TEXT NOT NULL,
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
INSERT INTO autoclaim_transaction_attempt_tmp SELECT * FROM autoclaim_transaction_attempt;
DROP TABLE autoclaim_transaction_attempt;

CREATE TABLE autoclaim_request_old (
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
INSERT INTO autoclaim_request_old (
    request_key, origin_network, destination_network, deposit_count, status, policy_result,
    bridge_tx_hash, claim_tx_hash, tx_manager_id, block_num, block_pos, global_index,
    l1_info_tree_index, retry_count, max_retries, last_observed_send_at, last_observed_result_at,
    created_at, updated_at, last_error, bridge_json, proof_json, policy_decision_json, manual_decision_json
)
SELECT
    request_key, origin_network, destination_network, deposit_count, status, policy_result,
    bridge_tx_hash, claim_tx_hash, tx_manager_id, block_num, block_pos, global_index,
    l1_info_tree_index, retry_count, max_retries, last_observed_send_at, last_observed_result_at,
    created_at, updated_at, last_error, bridge_json, proof_json, policy_decision_json, manual_decision_json
FROM autoclaim_request;
DROP TABLE autoclaim_request;
ALTER TABLE autoclaim_request_old RENAME TO autoclaim_request;

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
INSERT INTO autoclaim_transaction_attempt SELECT * FROM autoclaim_transaction_attempt_tmp;
DROP TABLE autoclaim_transaction_attempt_tmp;

CREATE INDEX idx_autoclaim_attempt_request ON autoclaim_transaction_attempt(request_key);
CREATE INDEX idx_autoclaim_attempt_tx_manager ON autoclaim_transaction_attempt(tx_manager_id);
CREATE INDEX idx_autoclaim_attempt_claim_tx_hash ON autoclaim_transaction_attempt(claim_tx_hash);

-- +migrate Up
-- autoclaim0001 ships in no release/tag (verified in the plan, §4bis #6), so the request_key format is
-- re-keyed from origin:destination:deposit_count to source:destination:deposit_count and recomputed for
-- existing rows. Every existing row is an L1-origin (source network 0) request.
--
-- Foreign keys are enforced (see db.NewSQLiteDB) and PRAGMA foreign_keys cannot be toggled inside the
-- migration transaction. Dropping a table that is the target of a foreign key is rejected even under
-- defer_foreign_keys, so the child transaction-attempt table is detached (rebuilt without its foreign
-- key, re-keyed via a join to the old request table) before the request table is rebuilt, then
-- reattached with its foreign key once the request table carries the new keys.

-- Phase 1: detach and re-key the child transaction-attempt table (temporary table has no foreign key).
CREATE TABLE autoclaim_transaction_attempt_new (
    request_key                  TEXT NOT NULL,
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
INSERT INTO autoclaim_transaction_attempt_new (
    request_key, attempt_number, claimer_id, tx_manager_id, claim_tx_hash, status, status_reason,
    retry_count, max_retries, sent_at, confirmed_at, last_observed_at, created_at, updated_at,
    last_error, transaction_data, target_bridge_addr, attempt_json
)
SELECT
    '0:' || p.destination_network || ':' || p.deposit_count,
    a.attempt_number, a.claimer_id, a.tx_manager_id, a.claim_tx_hash, a.status, a.status_reason,
    a.retry_count, a.max_retries, a.sent_at, a.confirmed_at, a.last_observed_at, a.created_at,
    a.updated_at, a.last_error, a.transaction_data, a.target_bridge_addr, a.attempt_json
FROM autoclaim_transaction_attempt a
JOIN autoclaim_request p ON p.request_key = a.request_key;
DROP TABLE autoclaim_transaction_attempt;

-- Phase 2: rebuild the request table with source_network, the LER columns, and the new uniqueness.
CREATE TABLE autoclaim_request_new (
    request_key                  TEXT PRIMARY KEY,
    source_network               INTEGER NOT NULL DEFAULT 0,
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
    ler                          TEXT,
    verify_block_num             INTEGER NOT NULL DEFAULT 0,
    UNIQUE(source_network, destination_network, deposit_count)
);
INSERT INTO autoclaim_request_new (
    request_key, source_network, origin_network, destination_network, deposit_count, status,
    policy_result, bridge_tx_hash, claim_tx_hash, tx_manager_id, block_num, block_pos, global_index,
    l1_info_tree_index, retry_count, max_retries, last_observed_send_at, last_observed_result_at,
    created_at, updated_at, last_error, bridge_json, proof_json, policy_decision_json,
    manual_decision_json, ler, verify_block_num
)
SELECT
    '0:' || destination_network || ':' || deposit_count,
    0,
    origin_network, destination_network, deposit_count, status, policy_result, bridge_tx_hash,
    claim_tx_hash, tx_manager_id, block_num, block_pos, global_index, l1_info_tree_index, retry_count,
    max_retries, last_observed_send_at, last_observed_result_at, created_at, updated_at, last_error,
    bridge_json, proof_json, policy_decision_json, manual_decision_json,
    NULL, 0
FROM autoclaim_request;
DROP TABLE autoclaim_request;
ALTER TABLE autoclaim_request_new RENAME TO autoclaim_request;

CREATE INDEX idx_autoclaim_request_status ON autoclaim_request(status);
CREATE INDEX idx_autoclaim_request_destination_status ON autoclaim_request(destination_network, status);
CREATE INDEX idx_autoclaim_request_policy_result ON autoclaim_request(policy_result);
CREATE INDEX idx_autoclaim_request_bridge_tx_hash ON autoclaim_request(bridge_tx_hash);
CREATE INDEX idx_autoclaim_request_claim_tx_hash ON autoclaim_request(claim_tx_hash);
CREATE INDEX idx_autoclaim_request_block_num ON autoclaim_request(block_num);
CREATE INDEX idx_autoclaim_request_updated_at ON autoclaim_request(updated_at);
CREATE INDEX idx_autoclaim_request_source_network ON autoclaim_request(source_network);

-- Phase 3: reattach the child transaction-attempt table with its foreign key now that the request
-- table carries the recomputed keys.
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
INSERT INTO autoclaim_transaction_attempt SELECT * FROM autoclaim_transaction_attempt_new;
DROP TABLE autoclaim_transaction_attempt_new;

CREATE INDEX idx_autoclaim_attempt_request ON autoclaim_transaction_attempt(request_key);
CREATE INDEX idx_autoclaim_attempt_tx_manager ON autoclaim_transaction_attempt(tx_manager_id);
CREATE INDEX idx_autoclaim_attempt_claim_tx_hash ON autoclaim_transaction_attempt(claim_tx_hash);

CREATE TABLE autoclaim_ler_cursor (
    source_network               INTEGER PRIMARY KEY,
    last_ler                     TEXT NOT NULL,
    last_verify_block_num        INTEGER NOT NULL,
    updated_at                   TIMESTAMP NOT NULL
);
