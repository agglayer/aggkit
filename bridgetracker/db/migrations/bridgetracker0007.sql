-- +migrate Down
DROP INDEX IF EXISTS idx_activity_bridge_page;
ALTER TABLE activity_bridge DROP COLUMN gi_sort;

-- +migrate Up
-- GET /activity/from/{from_address} filters, counts, orders and pages in SQL instead of loading,
-- decoding and sorting every row of the address on each request.
--
-- gi_sort: global_index as a fixed-width (78 digits), zero-padded decimal string
-- (domain.GlobalIndexSortKey), so ordering it as text orders global indexes numerically, exactly
-- -- they exceed 2^64, so an INTEGER cast would be lossy. Written with every row; existing rows are
-- backfilled here (a value that is not all digits, never served by a bridge service, sorts as 0
-- until its row is written again).
ALTER TABLE activity_bridge ADD COLUMN gi_sort VARCHAR NOT NULL DEFAULT '';
UPDATE activity_bridge SET gi_sort =
    CASE WHEN global_index <> '' AND global_index NOT GLOB '*[^0-9]*' AND length(global_index) <= 78
         THEN substr('000000000000000000000000000000000000000000000000000000000000000000000000000000'
                     || global_index, -78)
         ELSE '000000000000000000000000000000000000000000000000000000000000000000000000000000' END;

-- The page order of an address (domain.CompareActivityEntries: created_at, then global index, then
-- the bridge key, all descending), followed by the columns the request filters on, so the count
-- and the page are read from the index in order — filtered without touching the rows — and only
-- the rows of the page itself are read.
CREATE INDEX idx_activity_bridge_page ON activity_bridge
    (from_address, created_at DESC, gi_sort DESC, origin_network_id DESC, deposit_count DESC,
     schema_version, tracker_claim_status);
