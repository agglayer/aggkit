-- +migrate Down
ALTER TABLE token_mapping DROP COLUMN is_not_mintable;
ALTER TABLE token_mapping DROP COLUMN type NOT NULL;

-- +migrate Up
ALTER TABLE token_mapping ADD COLUMN is_not_mintable BOOLEAN NOT NULL;
ALTER TABLE token_mapping ADD COLUMN type SMALLINT NOT NULL;
