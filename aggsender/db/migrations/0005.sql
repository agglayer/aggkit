-- +migrate Down
DROP INDEX IF EXISTS idx_certificate_info_status;
DROP INDEX IF EXISTS idx_certificate_info_certificate_id;
DROP INDEX IF EXISTS idx_certificate_info_from_block;
DROP INDEX IF EXISTS idx_certificate_info_to_block;
DROP INDEX IF EXISTS idx_certificate_info_created_at;
DROP INDEX IF EXISTS idx_certificate_info_updated_at;
DROP INDEX IF EXISTS idx_certificate_info_cert_type;
DROP INDEX IF EXISTS idx_certificate_info_cert_source;
DROP INDEX IF EXISTS idx_certificate_info_status_height;
DROP INDEX IF EXISTS idx_certificate_info_history_height;
DROP INDEX IF EXISTS idx_certificate_info_history_status;
DROP INDEX IF EXISTS idx_certificate_info_history_certificate_id;

-- +migrate Up
-- Certificate info table indexes
CREATE INDEX idx_certificate_info_status ON certificate_info(status);
CREATE INDEX idx_certificate_info_certificate_id ON certificate_info(certificate_id);
CREATE INDEX idx_certificate_info_from_block ON certificate_info(from_block);
CREATE INDEX idx_certificate_info_to_block ON certificate_info(to_block);
CREATE INDEX idx_certificate_info_created_at ON certificate_info(created_at);
CREATE INDEX idx_certificate_info_updated_at ON certificate_info(updated_at);
CREATE INDEX idx_certificate_info_cert_type ON certificate_info(cert_type);
CREATE INDEX idx_certificate_info_cert_source ON certificate_info(cert_source);

-- Composite index for status + height queries (high priority)
CREATE INDEX idx_certificate_info_status_height ON certificate_info(status, height DESC);

-- Certificate info history table indexes
CREATE INDEX idx_certificate_info_history_height ON certificate_info_history(height);
CREATE INDEX idx_certificate_info_history_status ON certificate_info_history(status);
CREATE INDEX idx_certificate_info_history_certificate_id ON certificate_info_history(certificate_id);
