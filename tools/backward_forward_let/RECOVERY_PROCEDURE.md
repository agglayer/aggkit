# Backward/Forward LET — Fallback Recovery Procedure

This document is the canonical fallback procedure for `backward-forward-let` when the
aggsender database is empty, has been wiped, or otherwise cannot provide certificate
bridge exits.

In this situation the tool can still diagnose the settled AggLayer state, but it cannot
complete the divergence walk from aggsender data alone. The operator must extract the
missing certificate bridge exits from the AggLayer admin API, build an override file,
and rerun the tool with `--cert-exits-file`.

---

## Prerequisites

- The agglayer node must have `debug-mode = true` in its configuration.
  In the op-pp E2E environment this is already set (`debug-mode = true` in
  `test/e2e/envs/op-pp/config/agglayer/config.toml`).
- The agglayer admin JSON-RPC API must be reachable (default port 4446).
  The URL is exposed as `agglayer.services.admin_api.external` in `summary.json`.
- `curl` and `jq` must be installed on the operator's machine (`jq` is optional but
  makes the JSON manipulation much more convenient).

---

## Step 1 — Run the tool to discover missing cert IDs

```bash
backward-forward-let --cfg aggkit-config.toml
```

When the aggsender DB is empty the tool prints an actionable report:

```
WARNING: Aggsender RPC returned no bridge exit data for the following certificate heights.
Recovery cannot proceed until this data is provided.

Missing certificates (2 heights):
  Height 3  CertID: 0xabc123...def456  [ID auto-resolved]
  Height 2  CertID: UNKNOWN            [contact agglayer admin for cert ID]
```

- **`[ID auto-resolved]`** — the tool resolved the cert ID from the agglayer gRPC. You can
  call `admin_getCertificate` directly in Step 2.
- **`UNKNOWN`** — the cert ID could not be resolved automatically (only the latest settled
  height is resolvable via the public gRPC). The agglayer admin must look up
  `(network_id, height)` in the `certificate_per_network_cf` column family of the agglayer
  state DB and supply the cert ID manually before you can proceed.

Important operator note:

- After an aggsender DB wipe, the missing range may span the full settled history
  (`0..latest settled height`), not just the newest malicious certificate. That is normal
  for this fallback path.
- A large missing range is not a signal that the operator should do hundreds or thousands
  of manual one-by-one admin lookups.
- For large ranges, use automation: either script the `admin_getCertificate` calls for all
  known cert IDs, or ask the agglayer admin for a batch export of cert IDs and bridge exits.

---

## Step 2 — Fetch each certificate from the agglayer admin API

For small ranges, you can call `admin_getCertificate` manually per cert ID. For large
ranges, script this step or ask the agglayer admin for a batch export instead.

Per-certificate example:

```bash
AGGLAYER_ADMIN="http://localhost:4446"
CERT_ID="0xabc123...def456"

curl -s -X POST "$AGGLAYER_ADMIN" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"admin_getCertificate\",\"params\":[\"$CERT_ID\"],\"id\":1}" \
  | jq '.'
```

The response is a JSON-RPC result where `result` is a two-element array
`[Certificate, CertificateHeader|null]`:

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": [
    {
      "network_id": 1,
      "height": 3,
      "bridge_exits": [ ... ],
      ...
    },
    { ... }
  ]
}
```

You need `result[0].bridge_exits` from each response.

---

## Step 3 — Build the JSON override file

### Field name note

The override file uses the **Go `json` tag names** from `agglayertypes.BridgeExit`:

| Go field             | JSON key               |
|----------------------|------------------------|
| `LeafType`           | `leaf_type`            |
| `TokenInfo`          | `token_info`           |
| `DestinationNetwork` | `dest_network`         |
| `DestinationAddress` | `dest_address`         |
| `Amount`             | `amount` (decimal string) |
| `Metadata`           | `metadata` (base64 or null) |

The agglayer Rust serde may use different field names (e.g., `destination_network`
instead of `dest_network`). **Do not paste the raw `jq` output directly** unless you
have verified the field names match. The safest approach is to let Go do the translation
by using a small helper script (see below).

### Option A — Shell script (single cert, no Go tooling)

Verify that the field names in the admin API response match the table above before using
this option. If they do, you can pipe the `bridge_exits` array straight into the file:

```bash
AGGLAYER_ADMIN="http://localhost:4446"
CERT_ID="0xabc123...def456"
HEIGHT=3
NETWORK_ID=1

BRIDGE_EXITS=$(curl -s -X POST "$AGGLAYER_ADMIN" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"admin_getCertificate\",\"params\":[\"$CERT_ID\"],\"id\":1}" \
  | jq '.result[0].bridge_exits')

cat > certificate_exits_override.json <<EOF
{
  "network_id": $NETWORK_ID,
  "description": "Extracted from agglayer admin_getCertificate on $(date -u)",
  "heights": {
    "$HEIGHT": $BRIDGE_EXITS
  }
}
EOF
```

For multiple heights, collect each `bridge_exits` array and combine them:

```bash
HEIGHT_3=$(curl -s ... | jq '.result[0].bridge_exits')
HEIGHT_2=$(curl -s ... | jq '.result[0].bridge_exits')

cat > certificate_exits_override.json <<EOF
{
  "network_id": 1,
  "description": "Extracted from agglayer admin_getCertificate on $(date -u)",
  "heights": {
    "3": $HEIGHT_3,
    "2": $HEIGHT_2
  }
}
EOF
```

### Option B — Go round-trip (recommended when field names are uncertain)

Unmarshal the admin API response into `agglayertypes.Certificate` and re-marshal
`BridgeExits` using Go's json tags. This is what the E2E test (`TestBackwardForwardLET_AggsenderAPIFallback`)
does automatically. For a standalone operator script the pattern is:

```go
// Pseudo-code — adapt to your own tooling
response := jsonrpcCall(adminURL, "admin_getCertificate", certID)
var pair [2]json.RawMessage
json.Unmarshal(response.Result, &pair)
var cert agglayertypes.Certificate
json.Unmarshal(pair[0], &cert)

// cert.BridgeExits now uses Go field values regardless of Rust serde names.
data, _ := json.Marshal(overrideFileJSON{
    NetworkID: networkID,
    Heights:   map[string][]*agglayertypes.BridgeExit{
        strconv.FormatUint(height, 10): cert.BridgeExits,
    },
})
os.WriteFile("certificate_exits_override.json", data, 0o600)
```

---

## Step 4 — Re-run the tool with the override file

```bash
backward-forward-let --cfg aggkit-config.toml \
  --cert-exits-file certificate_exits_override.json
```

The `-f` short alias also works:

```bash
backward-forward-let --cfg aggkit-config.toml -f certificate_exits_override.json
```

The tool will:
1. Load bridge exits from the override file for any height where the aggsender RPC fails.
2. Complete the divergence walk using the combined data.
3. Print the full diagnosis (case classification, divergent leaves, extra L2 bridges).
4. Prompt for confirmation, then execute the recovery plan.

---

## Heights with UNKNOWN cert IDs

When the tool reports `CertID: UNKNOWN` for a height, the agglayer admin must:

1. Open the agglayer state DB (RocksDB).
2. Look up the key `(network_id, height)` in the `certificate_per_network_cf` column family
   to retrieve the `CertificateId`.
3. Call `admin_getCertificate` with that ID and provide `result[0].bridge_exits` to the
   operator.
4. The operator includes the exits for that height in the override file alongside any
   auto-resolved heights and re-runs the tool.

Only the latest settled height is auto-resolvable via the public agglayer gRPC. All
earlier heights require this lookup when the aggsender DB is absent.

For large missing ranges, do not treat this as a one-by-one manual task. The expected
workflow is to:

1. obtain the missing cert IDs in bulk from the agglayer admin or from pre-collected
   aggsender submission logs
2. fetch `bridge_exits` in bulk with a script or admin-side export
3. build a single override file and rerun the tool once

---

## Override file format reference

```json
{
  "network_id": 1,
  "description": "optional human-readable note",
  "heights": {
    "3": [
      {
        "leaf_type": 0,
        "token_info": {
          "origin_network": 0,
          "origin_token_address": "0x0000000000000000000000000000000000000000"
        },
        "dest_network": 1,
        "dest_address": "0xAbCd...1234",
        "amount": "1000000000000000000",
        "metadata": null
      }
    ],
    "2": []
  }
}
```

Key constraints enforced by the loader:

- `network_id` must be non-zero.
- `heights` must be present (even if empty).
- Height keys must be non-negative decimal integers (e.g., `"0"`, `"3"`).
- `amount` is a decimal string (matching Go `*big.Int` JSON marshaling).
- `metadata` is `null` for empty/nil; base64-encoded for non-empty byte slices.
- Field names must use Go json tags (`dest_network`, `dest_address`), not Rust serde names.
