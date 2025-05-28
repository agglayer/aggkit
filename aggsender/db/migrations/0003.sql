-- +migrate Down
ALTER TABLE certificate_info DROP COLUMN cert_type;
ALTER TABLE certificate_info DROP COLUMN cert_source;
ALTER TABLE certificate_info_history DROP COLUMN cert_type;
ALTER TABLE certificate_info_history DROP COLUMN cert_source;
DROP TABLE IF EXISTS nonaccepted_certificates;

-- +migrate Up
ALTER TABLE certificate_info ADD COLUMN cert_type VARCHAR DEFAULT "";
ALTER TABLE certificate_info ADD COLUMN cert_source VARCHAR DEFAULT "";
ALTER TABLE certificate_info_history ADD COLUMN cert_type VARCHAR DEFAULT "";
ALTER TABLE certificate_info_history ADD COLUMN cert_source VARCHAR DEFAULT "";

CREATE TABLE nonaccepted_certificates (
    height                     INTEGER NOT NULL,
    created_at                 INTEGER NOT NULL,
    signed_certificate         TEXT,
    PRIMARY KEY (height, created_at)
);