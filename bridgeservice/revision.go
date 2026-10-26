package bridgeservice

// CurrentAPIRevision is the bridge service's wire API contract version, returned by GET / and
// GET /health as HealthCheckResponse.APIRevision. Bump it by one whenever a change could break an
// existing client parsing the bridge service's responses — a field added/removed/renamed, an enum's
// value set changed, a query parameter whose absence changes the response shape, or any behavior
// change an existing client could trip over — so a client can tell which contract shape an instance
// speaks without diffing full response bodies against its own expectations. Purely informational:
// the bridge service itself never rejects or alters behavior based on it.
//
// It is independent of the bridge tracker's own revision (bridgetracker/api.CurrentAPIRevision): each
// service keeps its own counter, and this one only refers to the bridge service contract.
//
// Every revision is detailed in bridgeservice/docs/API_REVISIONS.md, which must get an entry together
// with the bump.
//
// Revision history:
//   - 1: first revision, the contract as of the release that introduced api_revision, which also
//     introduced the optional block_finalized field on the block-related responses. Earlier changes
//     are not back-filled
const CurrentAPIRevision = 1
