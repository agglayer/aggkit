# Bridge service API revisions

The bridge service reports the version of its wire API contract as `api_revision` in the health endpoint
(`GET /` and `GET /health`), backed by `bridgeservice.CurrentAPIRevision` (`bridgeservice/revision.go`):

```bash
curl -s <bridge-service-url>/health | jq .api_revision
```

It lets a client tell which contract an instance speaks without diffing response bodies, and pick the right
behaviour per instance. It is purely informational: the service never rejects or alters its behaviour based on
it. The field is always present, whatever the `sync_status`.

This is the bridge service's own counter, independent of the bridge tracker's `api_revision`
(`GET /tracker/v1/health`, see `bridgetracker/api/revision.go`). The two numbers are unrelated.

For the full description of every endpoint see the swagger files in this directory
(`swagger.json` / `swagger.yaml`) and [docs/bridge_service.md](../../docs/bridge_service.md).

## When to bump the revision

Bump `CurrentAPIRevision` by one, in the same PR, whenever a change could break an existing client:

- a response field added, removed or renamed (a new field that clients can safely ignore is **not** a bump,
  unless it is the only way to tell a behaviour change);
- the set of values of an enum-like field changed;
- a query parameter whose absence changes the response shape, or whose default changed;
- an endpoint added, removed or renamed;
- any behaviour change an existing client could trip over (ordering, pagination, filtering semantics, HTTP
  status codes...).

Then add an entry for the new revision to this file **and** to the revision history in
`bridgeservice/revision.go`. `TestCurrentAPIRevisionHasHistoryEntry` fails if the constant, the history in
`revision.go` and this file get out of sync.

Revisions before `api_revision` existed are not back-filled: revision 1 is the contract of the release that
introduced the field.

## Revision history

Newest first. Each entry lists what changed with respect to the previous revision and what a client should
do about it.

### Revision 1

The contract of the release that introduced `api_revision`; there is nothing to compare it with.

Worth knowing for clients that have to deal with older instances (those that don't return `api_revision`
at all):

- Responses tied to a block carry an optional `block_finalized` (`true`, `false` or absent): `/bridges`,
  `/bridge-by-deposit-count`, `/bridges-by-content`, `/claims`, `/claims-by-ger`, `/root-by-ler`,
  `/settlements` and `/injected-l1-info-leaf`. It is absent when the syncer can't tell, so an absent value
  must be read as "unknown", not as `false`.
- `GET /` and `GET /health` return `api_revision`.
