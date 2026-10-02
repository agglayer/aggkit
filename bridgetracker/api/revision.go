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
const CurrentAPIRevision = 6
