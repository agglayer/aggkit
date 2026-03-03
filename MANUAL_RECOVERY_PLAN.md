# Manual Recovery Plan: Aggsender DB Missing Certificates

## Problem Statement

When the aggsender DB is empty or has been wiped (e.g., after a restart with fresh storage),
the backward/forward LET tool cannot fetch bridge exits for settled certificates. The
`findDivergencePoint` function calls `aggsender_getCertificateBridgeExits(height)` via
JSON-RPC. When the aggsender DB has no data for that height, the RPC returns a "certificate
not found" error, causing the tool to set `AggsenderAPIFailed = true` and abort recovery.

The certificates DO still exist on the agglayer node (in its debug RocksDB store, accessible
via the `admin_getCertificate` JSON-RPC). We need a way to extract that data and feed it
to the tool.

## Design Constraints

1. **No direct DB access** — the extraction path uses only `admin_getCertificate` via the
   agglayer admin JSON-RPC (port 4446). No RocksDB manipulation.
2. **Tool provides the cert IDs to fetch** — when the tool can't complete diagnosis, its
   output includes the `CertificateId` for each missing height (where resolvable via the
   public agglayer gRPC). The operator uses these IDs to call `admin_getCertificate`.
3. **Tool does not call the admin API** — the tool only knows about the aggsender RPC and
   the JSON override file. The extraction step is a manual (or scripted) operator action.
4. **Test uses only tool output** — the E2E test does not pre-save certificate data before
   the aggsender DB is wiped. It reads cert IDs solely from the `DiagnosisResult` returned
   by the tool, calls the agglayer admin API with those IDs, and feeds the result back.

## Architecture Overview

### What the tool needs per settled height
- `[]*agglayertypes.BridgeExit` — the bridge exits array from the certificate at that height.
- Each `BridgeExit` has: `LeafType`, `TokenInfo{OriginNetwork, OriginTokenAddress}`,
  `DestinationNetwork`, `DestinationAddress`, `Amount`, `Metadata`.

### How cert IDs are resolved by the tool
The tool uses the public agglayer gRPC to resolve `CertificateId` values for missing heights:

| Height | Resolves via | Method |
|--------|-------------|--------|
| Latest settled height | `GetLatestSettledCertificateHeader(network_id)` → `CertificateHeader.CertificateID` | Already available in `AgglayerClientInterface` |
| Any height < latest | **Not resolvable via public API** | Reported as `UNKNOWN`; agglayer admin can find it |

In the most common failure scenario (a single recent cert diverged, which is also the latest
settled cert), the tool can provide the cert ID without any additional configuration. For
multi-height scenarios where earlier heights are missing, the operator must obtain those
cert IDs from the agglayer admin.

### Operator extraction flow (after tool reports missing cert IDs)
```
For each cert ID reported by the tool:

  POST http://<agglayer-admin>:4446
  {"jsonrpc":"2.0","method":"admin_getCertificate","params":["<cert_id_hex>"],"id":1}

  Response: [Certificate, CertificateHeader|null]
  Needed: Certificate.bridge_exits

Build the JSON override file (one entry per height) and re-run the tool with:
  --cert-exits-file <path>
```

The `admin_getCertificate` method requires `debug-mode = true` in the agglayer config.
In the E2E environment this is already set (`debug-mode = true` in
`test/e2e/envs/op-pp/config/agglayer/config.toml`).

---

## Implementation Chunks

### Chunk 1: Extend DiagnosisResult with Missing Cert Report

**Goal**: Add a structured `MissingCerts` field to `DiagnosisResult` so callers (the E2E
test, an operator script, or a future automation layer) can read exactly which cert IDs
to fetch, without parsing printed text.

**Files to modify**:
- `tools/backward_forward_let/types.go`

**Implementation details**:

1. Define `MissingCertInfo`:
   ```go
   // MissingCertInfo describes a certificate height for which bridge exits
   // could not be obtained from any available source.
   type MissingCertInfo struct {
       // Height is the certificate height that is missing.
       Height uint64

       // CertID is the agglayer CertificateId for this height, if it could be
       // resolved via the public gRPC. Zero-value when not resolvable.
       CertID common.Hash

       // CertIDResolved is true when CertID was successfully resolved.
       // When false, the operator must contact the agglayer admin.
       CertIDResolved bool
   }
   ```

2. Add to `DiagnosisResult`:
   ```go
   // MissingCerts lists the certificate heights for which no bridge exit data
   // was available. Populated when AggsenderAPIFailed is true.
   // The operator should fetch each cert from the agglayer admin API using
   // the provided CertID, then supply a JSON override file.
   MissingCerts []MissingCertInfo
   ```

3. The existing `FailedCertHeight` and `FailedCertID` fields become redundant now that
   `MissingCerts` carries the same information more completely. Mark them `Deprecated`
   via comment (do not remove yet; they may be used elsewhere).

---

### Chunk 2: Change findDivergencePoint to Collect All Missing Heights

**Goal**: Instead of aborting on the first aggsender failure, walk ALL settled heights,
collect every height where bridge exits cannot be obtained, and return a complete missing
report so the operator can supply all needed data in a single pass.

**Files to modify**:
- `tools/backward_forward_let/diagnosis.go`

**Implementation details**:

1. Replace the `aggsenderAPIError` struct with a richer `missingCertsError`:
   ```go
   // missingCertsError is returned when one or more heights have no bridge exit data.
   type missingCertsError struct {
       missing []MissingCertInfo
   }
   ```

2. Rewrite `findDivergencePoint` loop behavior when aggsender fails at height `h`:
   - Attempt to resolve the cert ID for `h` using `GetLatestSettledCertificateHeader`:
     - If `h == settledHeight`: the cert_id is in `result.L1SettledCertificateID` (already
       fetched in Step 1 of `Diagnose`). Use it directly.
     - If `h < settledHeight`: cert_id cannot be resolved; set `CertIDResolved = false`.
   - Record `MissingCertInfo{Height: h, CertID: certID, CertIDResolved: resolved}`.
   - **Do NOT abort**. Continue the loop to the next (older) height.
   - At the end of the loop, if `len(missing) > 0`, return the `missingCertsError`.

3. Note on correctness: skipping bridge exits for a height means the tool cannot determine
   whether that cert matches L2. The walk is therefore incomplete. The full diagnosis
   (divergence point, case classification) cannot proceed until all heights are covered.
   This is acceptable: the tool is surfacing what it needs, not attempting a partial result.

4. After the loop, in `Diagnose`, when `missingCertsError` is returned:
   ```go
   if missingErr != nil {
       result.AggsenderAPIFailed = true
       result.MissingCerts = missingErr.missing
       // Back-compat: populate deprecated fields from the first entry
       if len(missingErr.missing) > 0 {
           result.FailedCertHeight = missingErr.missing[0].Height
           result.FailedCertID     = missingErr.missing[0].CertID
       }
       return result, nil
   }
   ```

**Testing** (unit):
- Aggsender returns data for all heights → no change in behavior.
- Aggsender fails at height 2 of 3 → `MissingCerts` has entry for height 2 only;
  heights 3 and 1 (which succeed or continue past 2) are NOT reported.
- Aggsender fails at all heights → all heights in `MissingCerts`.
- Latest settled height cert ID is correctly extracted from `result.L1SettledCertificateID`.
- Earlier heights report `CertIDResolved = false`.

---

### Chunk 3: Define JSON Override File Format and Loader

**Goal**: Define a JSON file format for pre-extracted certificate bridge exits, and implement
a Go function to load and validate it. This is the only fallback source the tool uses.

**Files to create**:
- `tools/backward_forward_let/override.go` (new file)
- `tools/backward_forward_let/override_test.go` (new file)

**JSON file format** (`certificate_exits_override.json`):
```json
{
  "network_id": 1,
  "description": "Bridge exits extracted from agglayer admin_getCertificate",
  "heights": {
    "0": [
      {
        "leaf_type": 0,
        "token_info": {
          "origin_network": 0,
          "origin_token_address": "0x0000000000000000000000000000000000000000"
        },
        "destination_network": 0,
        "destination_address": "0xAbCd...1234",
        "amount": "0",
        "metadata": null
      }
    ],
    "1": []
  }
}
```

Heights are string-keyed (JSON does not support integer keys).
`amount` is a decimal string (matching `*big.Int` JSON marshaling in Go).
`metadata` is `null` for empty (nil `[]byte` in Go).

**Implementation**:

1. Define `BridgeExitsOverride`:
   ```go
   type BridgeExitsOverride struct {
       NetworkID   uint32                                 `json:"network_id"`
       Description string                                 `json:"description"`
       // rawHeights is keyed by string height from JSON.
       rawHeights  map[string][]*agglayertypes.BridgeExit `json:"heights"`
       // parsed is keyed by uint64 height for fast lookup.
       parsed      map[uint64][]*agglayertypes.BridgeExit
   }

   func (o *BridgeExitsOverride) GetExits(height uint64) ([]*agglayertypes.BridgeExit, bool) {
       exits, ok := o.parsed[height]
       return exits, ok
   }
   ```

2. Implement `LoadBridgeExitsOverride(filePath string) (*BridgeExitsOverride, error)`:
   - Read and unmarshal the JSON file.
   - Validate `network_id != 0` and `heights` is not nil.
   - Parse all string height keys to `uint64`; return error on non-numeric keys.
   - Store in `parsed` map.

3. **Critical**: verify that `agglayertypes.BridgeExit` JSON tags match the agglayer Rust
   serde output format. The `admin_getCertificate` response is Rust-serialized JSON. Check
   field names in `agglayer/types/types.go`. If JSON tag names differ from Rust serde names
   (e.g., `LeafType` vs `leaf_type`), add a dedicated unmarshaling type in the loader rather
   than modifying the agglayer types.

4. The file can be produced by the operator using a simple shell script (see Chunk 6).

**Testing** (unit, `override_test.go`):
- Happy path: valid JSON, all fields parse correctly.
- Edge case: height "0" → `uint64(0)` maps correctly.
- Edge case: empty bridge exits list at a height → `GetExits` returns `([], true)`.
- Error: non-numeric height key.
- Error: missing `network_id`.
- Error: malformed JSON.

---

### Chunk 4: Wire Override File into Env and Fallback

**Goal**: Add config support for the JSON override file path and use it as a fallback inside
`findDivergencePoint` before returning a missing error.

**Files to modify**:
- `tools/backward_forward_let/config.go`
- `tools/backward_forward_let/run.go`
- `tools/backward_forward_let/diagnosis.go`

**Config change** (`BackwardForwardLETConfig`):
```go
// CertificateExitsFile is an optional path to a JSON override file containing
// pre-extracted bridge exits keyed by certificate height. When set, used as a
// fallback if the aggsender RPC cannot supply bridge exits for a height.
// Obtain the file by calling admin_getCertificate on the agglayer for each
// cert ID reported in the tool's missing-cert output.
CertificateExitsFile string `mapstructure:"CertificateExitsFile"`
```

**Env change** (`run.go`):
```go
// BridgeExitsOverride is loaded from CertificateExitsFile if configured.
// nil when no override file is specified.
BridgeExitsOverride *BridgeExitsOverride
```

In `SetupEnv`: if `cfg.BackwardForwardLET.CertificateExitsFile != ""`, call
`LoadBridgeExitsOverride(path)` and store in `env.BridgeExitsOverride`. Fail fast
(return error) if the file exists but fails to parse.

**Fallback in `findDivergencePoint`** (`diagnosis.go`):

Replace the direct aggsender call with a two-source getter:
```go
func getBridgeExitsForHeight(
    env *Env,
    height uint64,
) ([]*agglayertypes.BridgeExit, error) {
    // Primary: aggsender RPC.
    exits, err := env.AggsenderRPC.GetCertificateBridgeExits(&height)
    if err == nil {
        return exits, nil
    }
    // Secondary: JSON override file.
    if env.BridgeExitsOverride != nil {
        if overrideExits, ok := env.BridgeExitsOverride.GetExits(height); ok {
            return overrideExits, nil
        }
    }
    return nil, fmt.Errorf("no bridge exit data for height %d: aggsender: %w", height, err)
}
```

This is the COMPLETE fallback chain. The tool has no third source.

**Testing** (unit):
- Aggsender succeeds → override not consulted.
- Aggsender fails, override has entry → override returned.
- Aggsender fails, override present but has no entry for height → error returned.
- Aggsender fails, no override configured → error returned.

---

### Chunk 5: Update PrintDiagnosis with Actionable Missing-Cert Output

**Goal**: When `AggsenderAPIFailed = true`, print clear, copy-pasteable instructions
for the operator. Include cert IDs so the operator can call `admin_getCertificate` directly.

**Files to modify**:
- `tools/backward_forward_let/diagnosis.go` (the `PrintDiagnosis` function)

**Output format** (replaces the current vague "contact your AggLayer admin" message):
```
WARNING: Aggsender RPC returned no bridge exit data for the following certificate heights.
Recovery cannot proceed until this data is provided.

Missing certificates (2 heights):
  Height 3  CertID: 0xabc123...def456  [ID auto-resolved]
  Height 2  CertID: UNKNOWN            [contact agglayer admin for cert ID]

To extract bridge exits for each KNOWN cert ID:
  POST http://<agglayer-admin-url>/
  Content-Type: application/json

  {"jsonrpc":"2.0","method":"admin_getCertificate","params":["<CertID>"],"id":1}

  The response is [Certificate, CertificateHeader|null].
  Extract the "bridge_exits" field from the Certificate object.

Build a JSON override file in this format:
  {
    "network_id": <L2NetworkID>,
    "heights": {
      "3": [ ...bridge_exits from admin_getCertificate response... ],
      "2": [ ...bridge_exits... ]
    }
  }

Re-run the tool with:
  backward-forward-let --cfg <config> --cert-exits-file <path-to-override.json>
```

**Implementation details**:
1. The format above is printed via `fmt.Fprintf(w, ...)` calls; no new dependencies.
2. The agglayer admin URL is NOT hardcoded or stored in the tool's config. The operator
   fills in their own URL in the printed template (the `<agglayer-admin-url>` placeholder).
3. For `CertIDResolved = false` entries, print `CertID: UNKNOWN` and add an extra note
   directing the operator to check aggsender submission logs or ask the agglayer admin
   to look up `certificate_per_network_cf` for `(network_id, height)`.

---

### Chunk 6: CLI Flag for Override File

**Goal**: Allow `--cert-exits-file` as a CLI flag so the operator does not have to edit the
TOML config file to supply the override.

**Files to modify**:
- The file where the `backward-forward-let` CLI app and its flags are defined
  (locate via `grep -r "urfave/cli" tools/backward_forward_let/`).

**Implementation details**:
1. Add `--cert-exits-file` (alias `-f`) string flag.
2. In the `Run` function, after `LoadConfig`, check if the flag is non-empty and override
   `cfg.BackwardForwardLET.CertificateExitsFile` with the flag value before calling `SetupEnv`.
3. The flag is optional. If neither the config nor the flag provides the path, the tool
   behaves as before (missing-cert report only, no override used).

---

### Chunk 7: Operator Extraction Procedure (Documentation)

**Goal**: Document the exact steps for extracting bridge exits from the agglayer and building
the JSON override file. No code changes in this chunk.

#### Prerequisites
- The agglayer node must have `debug-mode = true` in its config.
  In the op-pp E2E environment this is already set.
- The agglayer admin API must be reachable (default port 4446; exposed as `admin_api.external`
  in `summary.json`).
- `jq` installed for JSON manipulation (optional but convenient).

#### Step 1 — Run the tool to get the missing cert IDs
```bash
backward-forward-let --cfg aggkit-config.toml
# Output includes:
# Missing certificates (N heights):
#   Height H  CertID: 0xABC...  [ID auto-resolved]
```

#### Step 2 — Fetch each certificate from the agglayer admin API
For each cert ID printed by the tool:
```bash
AGGLAYER_ADMIN="http://localhost:4446"
CERT_ID="0xABC..."

curl -s -X POST "$AGGLAYER_ADMIN" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"admin_getCertificate\",\"params\":[\"$CERT_ID\"],\"id\":1}" \
  | jq '.'
```

The response is:
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": [
    {
      "network_id": 1,
      "height": 0,
      "bridge_exits": [ ... ],
      ...
    },
    { ... }
  ]
}
```

Extract `result[0].bridge_exits`.

#### Step 3 — Build the JSON override file
```bash
# Example for a single cert at height 0
BRIDGE_EXITS=$(curl -s -X POST "$AGGLAYER_ADMIN" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"admin_getCertificate\",\"params\":[\"$CERT_ID\"],\"id\":1}" \
  | jq '.result[0].bridge_exits')

cat > certificate_exits_override.json <<EOF
{
  "network_id": 1,
  "description": "Extracted from agglayer admin_getCertificate on $(date -u)",
  "heights": {
    "0": $BRIDGE_EXITS
  }
}
EOF
```

#### Step 4 — Re-run the tool with the override file
```bash
backward-forward-let --cfg aggkit-config.toml --cert-exits-file certificate_exits_override.json
```

#### For heights with UNKNOWN cert IDs
When the tool reports `CertID: UNKNOWN` for a height, the agglayer admin must look up the
cert ID by searching for `(network_id, height)` in the agglayer's `certificate_per_network_cf`
state DB column family. The agglayer admin then calls `admin_getCertificate` and provides the
result to the operator to include in the override file.

---

### Chunk 8: Modify TestBackwardForwardLET_AggsenderAPIFallback for Full Recovery

**Goal**: Transform the existing `TestBackwardForwardLET_AggsenderAPIFallback` test into a
test that:
1. Uses only tool output to discover what cert IDs are needed (no pre-saved data).
2. Calls the agglayer admin API using those cert IDs.
3. Feeds the extracted data back to the tool via the JSON override file.
4. Completes full Case2 recovery.

**Files to modify**:
- `test/e2e/backwardforwardlet_test.go`

**Test scenario** — Case2 with empty aggsender DB:

```
Phase 1: Setup (same structure as TestBackwardForwardLET_Case2)
  1.  Enable debug cert endpoint.
  2.  Build & send 1 malicious cert (1 fake bridge exit).
  3.  Wait for the cert to settle on agglayer.
  4.  Disable debug cert endpoint. Restore original aggkit config.
  5.  Create 2 real L2 bridges (createL2BridgeNoClaim × 2).
  6.  Wait for bridge service to re-sync (waitForBridgeServiceSynced).
      [At this point the system is in a Case2 diverged state.]

Phase 2: Wipe aggsender DB
  7.  Restart aggkit via RestartAggkitWithConfig, patching [AggSender] StoragePath to a
      fresh path: "/tmp/aggsender-empty-<unix-nanos>".
      This gives aggkit an empty SQLite DB while leaving all other state intact.
  8.  Wait for bridge service to re-sync again (restart may reset bridge service too).

Phase 3: First diagnosis — discover missing cert IDs
  9.  Build toolEnv using prepareBFLToolConfig (real aggsender URL, no CertificateExitsFile).
  10. Call bfl.Diagnose(ctx, toolEnv).
  11. Assert diagnosis.AggsenderAPIFailed == true.
  12. Assert len(diagnosis.MissingCerts) >= 1.
  13. Assert diagnosis.MissingCerts[0].CertIDResolved == true.
      (The malicious cert IS the latest settled cert, so cert_id is auto-resolved.)

Phase 4: Extract bridge exits from agglayer using cert IDs from tool output
  14. Read agglayer admin URL from summary.json ("agglayer.services.admin_api.external").
  15. For each entry in diagnosis.MissingCerts:
        a. Assert CertIDResolved == true (if not, the test must fail with a clear message).
        b. Call admin_getCertificate(entry.CertID) on agglayer:
             response = jsonrpcCall(adminURL, "admin_getCertificate", entry.CertID)
             cert = response.result[0]   // Certificate struct
        c. Collect cert.BridgeExits keyed by entry.Height.

Phase 5: Build JSON override file
  16. Marshal the collected bridge exits into the override file format:
        {"network_id": <L2NetworkID>, "heights": {"<height>": [<exits>], ...}}
  17. Write to t.TempDir() + "/override.json".

Phase 6: Second diagnosis — full diagnosis with override file
  18. Build toolEnv2 with CertificateExitsFile = path from step 17.
  19. Call bfl.Diagnose(ctx, toolEnv2).
  20. Assert:
        diagnosis2.AggsenderAPIFailed == false
        diagnosis2.Case == bfl.Case2
        len(diagnosis2.DivergentLeaves) == 1
        len(diagnosis2.ExtraL2Bridges) == 2

Phase 7: Recovery
  21. Call bfl.ExecuteRecovery(recoveryCtx, toolEnv2, diagnosis2).
  22. Assert post-state:
        L2 DepositCount == diagnosis2.DivergencePoint +
                           len(diagnosis2.DivergentLeaves) +
                           len(diagnosis2.ExtraL2Bridges)
        L2 IsEmergencyState == false

Phase 8: Cleanup
  23. Restore original aggkit StoragePath via RestartAggkitWithConfig.
```

**Key implementation details**:

1. **Admin API JSON-RPC call from test code** — add a test-local helper:
   ```go
   // callAgglayerAdminGetCertificate calls admin_getCertificate on the agglayer
   // admin JSON-RPC and returns the Certificate. Uses rpc.JSONRPCCall (same
   // package used by aggsender/rpcclient).
   func callAgglayerAdminGetCertificate(
       t *testing.T,
       adminURL string,
       certID common.Hash,
   ) *agglayertypes.Certificate {
       t.Helper()
       response, err := rpc.JSONRPCCall(adminURL, "admin_getCertificate", certID)
       require.NoError(t, err)
       require.Nil(t, response.Error)
       // response.Result is [Certificate, CertificateHeader|null]
       var pair [2]json.RawMessage
       require.NoError(t, json.Unmarshal(response.Result, &pair))
       var cert agglayertypes.Certificate
       require.NoError(t, json.Unmarshal(pair[0], &cert))
       return &cert
   }
   ```
   This helper is entirely data-driven from the cert IDs in `diagnosis.MissingCerts`.
   It does NOT know the cert content ahead of time.

2. **Extending `summaryForBFLToolConfig`** — add `AdminAPI URLPair` to the Agglayer services
   struct so the test can read `summary.json` for the admin URL:
   ```go
   Agglayer struct {
       Services struct {
           GrpcRPC  URLPair `json:"grpc_rpc"`
           AdminAPI URLPair `json:"admin_api"`  // add this
       } `json:"services"`
   } `json:"agglayer"`
   ```

3. **Wiping the aggsender DB** — use `RestartAggkitWithConfig` to patch only the
   `[AggSender]` section's `StoragePath`, keeping all other config intact:
   ```go
   err = testEnv.RestartAggkitWithConfig(ctx, func(cfgPath string) error {
       content, err := os.ReadFile(cfgPath)
       if err != nil { return err }
       freshPath := fmt.Sprintf("/tmp/aggsender-empty-%d", time.Now().UnixNano())
       patched := strings.Replace(
           string(content), "[AggSender]",
           "[AggSender]\nStoragePath = \"" + freshPath + "\"", 1,
       )
       return os.WriteFile(cfgPath, []byte(patched), 0o600)
   })
   ```

4. **Building the override file** — marshal `MissingCertOverride`:
   ```go
   type overrideFile struct {
       NetworkID   uint32                                 `json:"network_id"`
       Description string                                 `json:"description"`
       Heights     map[string][]*agglayertypes.BridgeExit `json:"heights"`
   }
   of := overrideFile{
       NetworkID:   uint32(testEnv.L2.NetworkID),
       Description: "extracted by E2E test from agglayer admin API",
       Heights:     make(map[string][]*agglayertypes.BridgeExit),
   }
   for _, mc := range diagnosis.MissingCerts {
       cert := callAgglayerAdminGetCertificate(t, adminURL, mc.CertID)
       of.Heights[strconv.FormatUint(mc.Height, 10)] = cert.BridgeExits
   }
   overrideBytes, err := json.Marshal(of)
   require.NoError(t, err)
   overridePath := filepath.Join(t.TempDir(), "override.json")
   require.NoError(t, os.WriteFile(overridePath, overrideBytes, 0o600))
   ```

5. **`prepareBFLToolConfig` extension** — add an optional `certExitsFile string` parameter
   (or use a separate `prepareBFLToolConfigWithOverride` variant) to set
   `[BackwardForwardLET] CertificateExitsFile`.

**Test assertions checklist**:
- [ ] Phase 3: `AggsenderAPIFailed == true` after wipe
- [ ] Phase 3: `MissingCerts` is non-empty with `CertIDResolved == true`
- [ ] Phase 4: `admin_getCertificate` returns a non-nil Certificate for each cert ID
- [ ] Phase 6: Second diagnosis completes without `AggsenderAPIFailed`
- [ ] Phase 6: Case2 classification with correct divergent leaves and extra bridges
- [ ] Phase 7: Recovery succeeds, correct deposit count, no emergency state

---

## Execution Order

```
Chunk 1 (DiagnosisResult: MissingCerts field)
    ↓
Chunk 2 (findDivergencePoint: walk all heights, collect missing)
    ↓
Chunk 3 (JSON override file format + loader)
    ↓
Chunk 4 (wire override into Env + getBridgeExitsForHeight fallback)
    ↓
Chunk 5 (PrintDiagnosis: actionable output with cert IDs)
    ↓
Chunk 6 (CLI flag --cert-exits-file)
    ↓
Chunk 7 (documentation: extraction procedure) ← no dependencies, write anytime
    ↓
Chunk 8 (E2E test: full recovery with tool-driven extraction)
```

All chunks are required. Chunk 7 (docs) can be written in parallel with any other chunk.

---

## Key Risks and Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| `admin_getCertificate` response JSON format doesn't match Go `agglayertypes.Certificate` JSON tags | Bridge exits unmarshal incorrectly or fail | Chunk 3 explicitly requires verifying JSON tags against Rust serde output. Add a targeted unit test in `override_test.go` with a raw JSON fixture captured from the agglayer. |
| `debug-mode = false` in production agglayer | `admin_getCertificate` returns empty (cert not in debug store) | Document clearly in Chunk 7. Operator must confirm `debug-mode = true` before attempting extraction. |
| Height < latest settled has `CertIDResolved = false` | Operator can't use the tool's cert ID to call admin API | Document in Chunk 7 that agglayer admin must look up `(network_id, height)` in the state DB and provide the cert ID manually. |
| `BridgeExit.Amount` serialization: `*big.Int` ↔ JSON decimal string | Override file parses amount as 0 or errors | Verify `agglayertypes.BridgeExit`'s `json` tag for `Amount`. Use the same marshaling in the override file. The Rust serde likely outputs a decimal string too; confirm this. |
| Aggsender re-populates DB before Phase 3 of the test | Test catches DB with data, `AggsenderAPIFailed` is false | After restart with fresh StoragePath, the aggsender queries agglayer and learns height N is settled. It will NOT re-submit heights 0..N (only build N+1). Heights 0..N remain absent from the new DB for the duration of the test. |
| Aggkit double-restart (phases 4 and 8) delays test | Long test runtime | Both restarts are already within the 35-minute timeout. No change needed. |

## Summary

The solution has two sources for bridge exit data (in priority order):
1. **Aggsender RPC** (primary, existing) — works when aggsender DB is intact.
2. **JSON override file** (new) — operator provides data extracted from the agglayer admin API.

The tool does NOT call the admin API. Instead, when data is missing, it prints a structured
report showing which cert IDs to fetch. The operator calls `admin_getCertificate` externally,
builds the override file, and re-runs the tool.

The E2E test exercises this exact loop — using only the tool's `DiagnosisResult.MissingCerts`
to drive the extraction step — and validates end-to-end Case2 recovery with an empty aggsender DB.
