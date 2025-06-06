-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN cert_type;
ALTER TABLE certificate_info DROP COLUMN cert_source;
ALTER TABLE certificate_info DROP COLUMN extra_data;
ALTER TABLE certificate_info_history DROP COLUMN cert_type;
ALTER TABLE certificate_info_history DROP COLUMN cert_source;
ALTER TABLE certificate_info_history DROP COLUMN extra_data;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN cert_type VARCHAR DEFAULT "";
ALTER TABLE certificate_info ADD COLUMN cert_source VARCHAR DEFAULT "";
ALTER TABLE certificate_info ADD COLUMN extra_data VARCHAR DEFAULT "";
ALTER TABLE certificate_info_history ADD COLUMN cert_type VARCHAR DEFAULT "";
ALTER TABLE certificate_info_history ADD COLUMN cert_source VARCHAR DEFAULT "";
ALTER TABLE certificate_info_history ADD COLUMN extra_data VARCHAR DEFAULT "";