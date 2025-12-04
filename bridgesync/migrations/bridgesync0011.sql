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
    select  tx_hash from bridge group by tx_hash having count(*) > 1 
);
