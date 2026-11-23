package api

// CurrentAPIRevision is the tracker's wire API contract version, returned by GET /health as
// APIRevision. Bump it by one whenever a change could break an existing client parsing the
// tracker's responses — a field added/removed/renamed, an enum's value set changed, a new step
// inserted into a bridge's expected path, and the like — so a client can tell which contract
// shape an instance speaks without diffing full response bodies against its own expectations.
// Purely informational: the tracker itself never rejects or alters behavior based on it
//
// Revision history:
//   - 2: GET /activity/from/{from_address}'s ActivityItem.claimed field — a tri-state
//     "false"/"true"/"error" mirroring the destination bridge contract's isClaimed() call —
//     was renamed to claim_status and revalued to the same "pending"/"readyToClaim"/
//     "claimed"/"error" vocabulary as TrackingData.claim_status; ?filterBridges= gained a new
//     "readyToClaim" value to match
//   - 3: a bridge step's status gained a new "skipped" value (types.StepStatusSkipped) and its
//     error, when present, a new "skipped" error_type (types.StepErrorSkipped) — the tracker
//     falls back to it for a step whose historical fact it can never verify once the
//     destination network's own claim status proves the bridge finished anyway (see
//     agglayer/aggkit#1836)
//   - 4: a "skipped" step's error field no longer always carries the "skipped" error_type from
//     revision 3 (now removed): the step whose real error actually triggered the
//     claimed-bridge fallback keeps its own genuine error_type (transient/permanent) and
//     description instead, so it stays distinguishable from a real, still-unresolved error;
//     any other step skipped alongside it, never itself attempted, now omits error entirely
//     instead of carrying a placeholder
//   - 5: GET /tracker/v1/health gained a new optional pending_networks array
//     (HealthResponse.PendingNetworks), listing networks the bridge service finder discovered
//     after startup but did not activate because [BridgeServiceFinder] AutoRegisterNewNetworks
//     is false; omitted when empty, so an existing client ignoring unknown fields is unaffected.
//     The same revision added start_date (HealthResponse.StartDate), the instant this instance
//     started, which is the reference point every pending_networks entry's first_seen is
//     relative to
//   - 6: GET /tracker/v1/activity/from/{from_address} is now paginated: it gained optional
//     page_number (default 1) and page_size (default 20, max 200) query parameters — same names
//     and 1-based numbering as the bridge service's paginated endpoints — and its response a
//     new count field (the total number of bridges matching filterBridges across every page).
//     Bridges are now returned most recent first (creation_timestamp descending). A client that
//     sends neither parameter used to receive every bridge and now receives only the first 20,
//     so it must page through the result using count
//   - 7: reorg support (agglayer/pm#445, agglayer/aggkit#1971). claim_status, on both the
//     tracker's TrackingData and GET /activity/from/{from_address}'s ActivityItem, gained a new
//     "claimed_finalized" value, the only final state — "claimed" can now go back to
//     "readyToClaim" or "pending" after a reorg, so a client that matched claim_status against a
//     closed set of values must accept it; filterBridges=claimed returns both "claimed" and
//     "claimed_finalized". New always-serialized fields: ActivityItem.finalized (its bridge and,
//     if claimed, its claim are final), BridgeStatus.block_finalized (the bridge's origin block
//     is final) and ActivityResponse.revision, an opaque string — a "v2:"-prefixed hash of the
//     address's activity (per bridge: global_index, the bridge's tx_hash and block_num, the
//     claim_status with claimed_finalized counted as claimed, the claim's tx_hash) — a client
//     polls with a cheap request (page_size=1) to detect changes beyond page 1. It changes on
//     an addition, removal, claim_status change or reorg correction, not when an entry only
//     becomes final, and being a hash it is the same after a restart and on every instance
//     holding the same data; clients compare it for equality only
//   - 8: GET /activity/from/{from_address} supports HTTP conditional requests
//     (agglayer/aggkit#1971). Its 200 response carries the revision as the ETag header (the
//     revision between double quotes, a strong entity tag) and Cache-Control: no-store; a request
//     carrying that value in If-None-Match (or "*") is answered 304 Not Modified, with the ETag
//     and no body, while the revision is unchanged, whatever filterBridges/page_number/page_size
//     say. Additive: the body is unchanged and a client that sends no If-None-Match never
//     receives a 304. A 304 says nothing about what the revision does not cover (finalized,
//     last_updated_timestamp, tracking, warnings)
const CurrentAPIRevision = 8
