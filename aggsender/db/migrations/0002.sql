-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN aggchain_proof;
ALTER TABLE certificate_info_history DROP COLUMN aggchain_proof;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN aggchain_proof VARCHAR;
ALTER TABLE certificate_info_history ADD COLUMN aggchain_proof VARCHAR;