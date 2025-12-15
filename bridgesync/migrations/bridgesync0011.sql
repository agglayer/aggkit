-- +migrate Down

-- +migrate Up
UPDATE bridge
SET from_address = NULL
WHERE tx_hash IN (
    SELECT b.tx_hash
    FROM bridge b
    JOIN claim c ON b.tx_hash = c.tx_hash
);
UPDATE bridge
SET from_address = NULL
WHERE tx_hash IN (
    SELECT tx_hash FROM bridge GROUP BY tx_hash HAVING COUNT(*) > 1 
);
