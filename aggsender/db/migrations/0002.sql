-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN auth_proof;
ALTER TABLE certificate_info_history DROP COLUMN auth_proof;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN auth_proof VARCHAR;
ALTER TABLE certificate_info_history ADD COLUMN auth_proof VARCHAR;