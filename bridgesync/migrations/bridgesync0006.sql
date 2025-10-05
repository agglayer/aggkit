-- +migrate Up
ALTER TABLE claim DROP COLUMN tx_sender;
