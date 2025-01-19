-- +migrate Down
DROP TABLE IF EXISTS auth_proof;

-- +migrate Up
CREATE TABLE auth_proof (
    indentifier    VARCHAR,
    proof          VARCHAR,
    PRIMARY KEY (indentifier)
);