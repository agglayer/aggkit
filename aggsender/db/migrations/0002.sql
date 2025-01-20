-- +migrate Down
DROP TABLE IF EXISTS auth_proof;

-- +migrate Up
CREATE TABLE auth_proof (
    start_block    INTEGER NOT NULL,
    end_block      INTEGER NOT NULL,
    proof          VARCHAR,
    created_at     INTEGER NOT NULL,
    updated_at     INTEGER NOT NULL,
    PRIMARY KEY (start_block, end_block)
);