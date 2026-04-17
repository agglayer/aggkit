# Backward/Forward LET runbook

## Introduction

The Local Exit Tree (LET) on L2 must stay consistent with the Local Exit Root (LER)
settled on L1 through the AggLayer. When they diverge, future certificates can be
rejected until the L2 bridge is reconciled.

Use `backward-forward-let` for this workflow. The tool already:

- reads the settled AggLayer state,
- reads the current L2 bridge state,
- queries aggsender for certificate bridge exits,
- finds the divergence point,
- classifies the recovery case,
- prints the recovery plan,
- activates emergency mode when needed,
- executes `BackwardLET` and/or `ForwardLET`,
- verifies deposit count and LER after each step,
- deactivates emergency mode at the end.

This runbook documents the operator flow. It intentionally avoids manual diagnosis steps
that are already implemented in the tool.

## When to run this

Run the tool when the bridge appears out of sync with the last settled AggLayer state,
for example:

- a certificate is rejected or transitions to `InError`,
- aggsender repeatedly fails to build or send certificates,
- an L2 reorg or aggsender issue is suspected to have settled the wrong LET state.

The tool determines whether there is actual divergence. Do not manually compare L1 and
L2 state unless you are debugging the tool itself.

## Prerequisites

Prepare an aggkit config file that includes the normal chain and AggLayer settings plus
the `BackwardForwardLET` section used by the tool.

Required config inputs:

- `Common.L2RPC.URL`
- `BridgeL2Sync.BridgeAddr`
- `AgglayerClient`
- `BackwardForwardLET.BridgeServiceURL`
- `BackwardForwardLET.AggsenderRPCURL`
- `BackwardForwardLET.L2NetworkID`
- `BackwardForwardLET.GERRemoverKey`
- `BackwardForwardLET.EmergencyPauserKey`
- `BackwardForwardLET.EmergencyUnpauserKey`

Role expectations:

- `GERRemoverKey` must be allowed to call `backwardLET` and `forwardLET`.
- `EmergencyPauserKey` must be allowed to activate emergency state.
- `EmergencyUnpauserKey` must be allowed to deactivate emergency state.

The tool handles emergency-mode activation and deactivation itself. There is no separate
manual pause/unpause step in the normal flow.

For staged malicious-certificate drills used to create divergence intentionally:

- stop aggkit/aggsender before crafting or sending malicious certificates so normal
  certificate production does not race the drill,
- confirm there is no unrelated non-error pending certificate already occupying the next
  height before sending the malicious cert,
- if the drill includes genuine L2 bridge creation, wait for bridge-service indexing before
  expecting diagnosis or recovery to reason about those bridges,
- restart aggkit/aggsender only after all malicious certificates for that drill have been
  submitted.

Aggsender restart caveat:

- aggsender intentionally refuses to auto-reconcile if its local DB still points to an
  older or different certificate than the one already settled on AggLayer,
- if startup logs that the local certificate state is inconsistent with a further
  AggLayer certificate, the operator must wipe the aggsender DB and restart aggsender,
- there is no supported automatic recovery path for that mismatch.

## Standard procedure

Run the tool:

```bash
backward-forward-let --cfg aggkit-config.toml
```

For non-interactive execution:

```bash
backward-forward-let --cfg aggkit-config.toml --yes
```

What happens next:

1. The tool validates connectivity to the bridge service, L2 RPC, AggLayer, and aggsender.
2. It diagnoses the current state and prints one of:
   - `NoDivergence`
   - a recovery case with the divergence point and affected leaves
   - a missing-certificate report if aggsender cannot provide bridge exits
3. If recovery is needed, it prints the exact recovery plan.
4. It asks for confirmation unless `--yes` is set.
5. It executes the required on-chain steps and verifies the resulting deposit count and LER.

Operational notes from staging:

- A just-created genuine L2 bridge is not usable by the tool until bridge service has
  indexed it. If diagnosis says a deposit is not indexed yet, wait for bridge-service
  catch-up instead of improvising a manual recovery.
- In staged Case 3 drills, the state after only the first malicious certificate settles is
  still effectively Case 1. Final Case 3 classification only appears after the second
  malicious certificate also settles.
- After staged malicious-certificate drills, aggsender may fail its startup consistency
  checks because its local DB still points to a pre-drill certificate while AggLayer is
  already further ahead. In that case, wipe the aggsender DB and restart it before
  expecting honest certificate production to resume.

Recovery behavior by case:

- Case 1 and Case 3: `ForwardLET` only.
- Case 2 and Case 4: `BackwardLET`, then `ForwardLET` for divergent settled leaves, then a
  second `ForwardLET` when extra real L2 bridges must be replayed.

## Expected outcomes

- If the tool reports `NoDivergence`, no action is required.
- If the tool completes recovery successfully, the L2 bridge is reconciled to the settled
  AggLayer state and emergency mode is turned off before exit.
- If the tool reports missing certificate bridge exits, stop and use the fallback flow
  below.
- For staged Case 2 or Case 4 drills, if recovery replays genuine L2 bridges while
  aggsender is still stopped, the first post-recovery rerun may still show divergence.
  In that situation, restart aggsender, wait for the honest follow-up certificate(s) to
  settle, then rerun until the tool reports `NoDivergence`.

## Fallback when aggsender bridge exits are unavailable

If aggsender RPC cannot supply bridge exits for one or more settled certificate heights,
the tool prints an actionable report listing the missing heights and any certificate IDs
it could resolve automatically.

When aggsender is intentionally stopped for a fallback drill, this missing range may span
the full settled history from height `0` through the latest settled certificate. That is
expected; build an override file for the heights the tool needs and rerun with that data.

Re-run the tool with an override file once you have the missing bridge exits:

```bash
backward-forward-let --cfg aggkit-config.toml \
  --cert-exits-file certificate_exits_override.json
```

The override file is only a fallback for missing certificate exits. Diagnosis and
recovery still stay tool-driven.

The same override file can also be supplied to `backward-forward-let craft-cert` when a
later malicious certificate must be crafted while aggsender is still unavailable.

For the detailed fallback procedure, including AggLayer admin/debug endpoint
prerequisites and override-file handling examples, see
[`tools/backward_forward_let/RECOVERY_PROCEDURE.md`](../tools/backward_forward_let/RECOVERY_PROCEDURE.md).

### Step 1: fetch missing certificates from the AggLayer admin API

For each certificate ID reported by the tool, call `admin_getCertificate`:

```bash
AGGLAYER_ADMIN="http://localhost:4446"
CERT_ID="0xabc123...def456"

curl -s -X POST "$AGGLAYER_ADMIN" \
  -H "Content-Type: application/json" \
  -d "{\"jsonrpc\":\"2.0\",\"method\":\"admin_getCertificate\",\"params\":[\"$CERT_ID\"],\"id\":1}"
```

Use `result[0].bridge_exits` from the response.

If the tool reports `CertID: UNKNOWN`, the AggLayer admin must first resolve that
certificate ID from AggLayer state before you can fetch its `bridge_exits`.

### Step 2: build the override file

The override file must use Go JSON field names for `BridgeExit` objects:

```json
{
  "network_id": 1,
  "description": "Extracted from agglayer admin_getCertificate",
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
    ]
  }
}
```

Constraints:

- `network_id` must match the affected L2 network.
- `heights` keys are certificate heights as decimal strings.
- `amount` is a decimal string.
- `metadata` is `null` or base64-encoded bytes.
- Use `dest_network` and `dest_address`, not Rust serde field names.

### Step 3: rerun the tool

```bash
backward-forward-let --cfg aggkit-config.toml \
  --cert-exits-file certificate_exits_override.json
```

The tool will resume diagnosis using the override data, print the recovery plan, and
execute the same standard recovery flow.
