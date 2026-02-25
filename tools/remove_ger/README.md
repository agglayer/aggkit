# remove-ger

Diagnose and recover from invalid Global Exit Root (GER) injection on L2.

## Overview

**What it does:** The `remove-ger` CLI validates a GER on L1 and L2, finds claims on L2 that used that GER, and classifies each claim. After an optional interactive confirmation, it runs the recovery steps (freeze bridge, remove GER, unset/set claims or force-emit corrected events as needed, restore bridge).

**When to use it:** Use after you have detected an invalid GER—for example via aggsender or l2gersync error logs. For how to detect invalid GERs and manual recovery context, see the [Remove GER runbook](../../docs/remove_ger_runbook.md). This tool automates the procedures described there.

## Building

From the repository root:

- **Single binary:**  
  `go build -o remove_ger ./tools/remove_ger/cmd`
- **All tools (Makefile):**  
  `make build-tools` — builds the remove_ger tool (and other tools) into `$(GOBIN)/remove_ger`.

## Config file

The tool uses the **same** config file(s) as the main aggkit binary: standard `aggkit-config.toml` format, loaded via the `--cfg` / `-c` flag. The **only** change versus the main config is that you must add a `[RemoveGER]` section.

### Additional section: `[RemoveGER]`

| Field | Type | Description |
| ----- | ---- | ------------ |
| **BridgeServiceURL** | string | Bridge service REST API base URL (**required**). Used for querying claims and bridges. The tool runs a health check at startup and will fail if the service is unreachable. |
| **SovereignAdminKey** | section | Signing key with sovereign admin privileges (activate/deactivate emergency state, remove GER, unset/set claims, force-emit claim events). Supports local keystore, AWS KMS, and GCP KMS. See sub-fields below. |

**SovereignAdminKey** sub-fields (depends on `Method`):

| Field | Type | Description |
| ----- | ---- | ------------ |
| **Method** | string | Signing method: `"local"` (keystore file), `"AWS"` (AWS KMS), `"GCP"` (GCP KMS). |
| **Path** | string | Path to the keystore file (`"local"` only). |
| **Password** | string | Password to decrypt the keystore (`"local"` only). |

## Example config addition

Append the following to your existing `aggkit-config.toml` (adjust paths and URL to your environment):

```toml
[RemoveGER]
BridgeServiceURL = "http://localhost:8080"
SovereignAdminKey = { Method = "local", Path = "/path/to/sovereign_admin_keystore.json", Password = "your-keystore-password" }
```

## Usage examples

**Interactive (diagnose, then confirm before recovery):**

```bash
./remove_ger --cfg aggkit-config.toml --ger 0x0123...64_hex_chars
```

**Non-interactive (for automation):**

```bash
./remove_ger --cfg aggkit-config.toml --ger 0x0123...64_hex_chars --yes
```

You can pass multiple config files; later files override earlier ones (e.g. `--cfg base.toml --cfg overrides.toml`).

## CLI flags

| Flag | Short | Required | Description |
| ---- | ----- | -------- | ----------- |
| `--cfg` | `-c` | Yes | Configuration file(s), same format as aggkit-config.toml. |
| `--ger` | — | Yes | Invalid GER hash to diagnose and remove (hex, 0x-prefixed, 32 bytes / 64 hex chars). |
| `--yes` | — | No | Skip interactive confirmation and run recovery immediately. |
| `--force` | — | No | Continue even if the GER exists on L1 (still diagnose and remove). |

## Scenarios

The tool classifies the situation and runs the matching recovery flow:

- **No claims** — The GER exists on L2 but no claims use it.  
  **Steps:** Freeze bridge → remove GER → restore bridge.

- **Category A (under-collateralization)** — Claim(s) reference a bridge that does not exist on L1 or has different content.  
  **Steps:** Freeze bridge → remove GER → unset those claims → restore bridge.

- **Category B.1 (GER mismatch, same index)** — Claim(s) have correct bridge data but wrong GER.  
  **Steps:** Freeze bridge → remove GER → force-emit corrected claim event(s) → restore bridge.

- **Category B.2 (GER and index mismatch)** — Claim(s) have wrong GER and wrong index; the correct bridge exists on L1 at a different deposit count.  
  **Steps:** Freeze bridge → remove GER → unset wrong claims → set correct claims (correct global indexes) → force-emit corrected claim events → restore bridge.

## Troubleshooting

- **Wrong private key / keystore:** Ensure `SovereignAdminKey` points to a key that has sovereign admin roles on the L2 contracts (emergency bridge pause/unpause, GER removal, unset/set claims, force emit). If recovery transactions fail with auth errors, verify the key’s roles on the L2 bridge and GER manager contracts.

- **Bridge service not reachable:** If `BridgeServiceURL` is set, the tool runs a health check at startup. Connection or HTTP errors will cause an immediate exit. Check the URL, network access, and that the bridge service is running.

- **GER actually exists on L1:** By default the tool exits when the GER is found on L1 (treated as valid). Use `--force` only when you intentionally want to diagnose and remove a GER that exists on L1 (e.g. operational override).
