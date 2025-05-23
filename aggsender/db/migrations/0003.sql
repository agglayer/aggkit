-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN cert_type;
ALTER TABLE certificate_info_history DROP COLUMN cert_type;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN cert_type VARCHAR DEFAULT "";
ALTER TABLE certificate_info_history ADD COLUMN cert_type VARCHAR DEFAULT "";