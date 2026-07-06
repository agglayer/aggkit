# Aggkit-owned Kurtosis e2e config templates

This directory is the **source of truth for the aggkit config used by the
kurtosis-cdk based Bats e2e** (the `agglayer/e2e` reusable workflows driven from
`.github/workflows/test-e2e.yml`).

## Files

| File | Overlays kurtosis-cdk's | Used by |
| --- | --- | --- |
| `aggkit-config.template.toml` | `static_files/chain/shared/aggkit/config.toml` | OP / op-succinct / sovereign chains |
| `aggkit-cdk-config.template.toml` | `static_files/chain/shared/aggkit/cdk-config.toml` | cdk-erigon chains |

## How it works

1. `test-e2e.yml` uploads this directory as the `aggkit-e2e-config` artifact and
   passes `aggkit-config-artifact: aggkit-e2e-config` to the reusable workflows.
2. The `agglayer/e2e` reusable workflows, when that input is set, download the
   artifact and copy these files over kurtosis-cdk's `static_files` copies
   **after checking out kurtosis-cdk and before `kurtosis run`**.
3. `kurtosis run` renders the template exactly as before (they are still Go
   `text/template` files consumed by kurtosis-cdk's `_build_config_data`).

The result: **aggkit's own e2e always uses the config from the aggkit commit
under test**, so config changes ship in the same PR as the code — no kurtosis-cdk
PR and no pin bump for a config-only change.

## ⚠️ Invariant: seed from the pinned `KURTOSIS_CDK_COMMIT`

These files are rendered by **the exact kurtosis-cdk commit pinned as
`KURTOSIS_CDK_COMMIT` in `.github/workflows/test-e2e.yml`** — not `main`. Its
`_build_config_data` decides which render variables and `{{.sequencer_type}}`
values exist, and the field set differs across kurtosis-cdk versions (e.g. the
`op-geth` → `op-reth` sequencer-type rename, or fields like `ClaimL2Sync`).

Seeding from the wrong version renders empty/missing values and aggkit dies at
startup (`hex string has length 0, want 40 for Address`). So:

- **Seed each file from `git show <KURTOSIS_CDK_COMMIT>:static_files/chain/shared/aggkit/<file>`**
  and apply only the aggkit-owned edits (e.g. `[REST]` → `[PublicREST]`).
- **When you bump `KURTOSIS_CDK_COMMIT`, re-seed these files from the new commit**
  and re-apply the edits.

## Ownership boundary — read before editing

These are **templates rendered by kurtosis-cdk**, not final configs. They may
only reference render variables that kurtosis-cdk's `_build_config_data`
provides (`{{.aggkit_node_rest_api_port}}`, `{{.l1_rpc_url}}`, keystore paths,
addresses, `{{.sequencer_type}}` conditionals, …).

- ✅ **Static changes** (rename a section, add a section with literal values,
  change an interval, add/remove an aggkit config field) — edit here freely, in
  the same PR as the code. No coordination needed.
- ⚠️ **A new *dynamic* value** (a new address/port/URL that must come from the
  environment) requires a new render variable in kurtosis-cdk first. That is the
  moment to open a kurtosis-cdk PR — which is also when you propagate (see below).

## Propagating to kurtosis-cdk (on release / periodically)

kurtosis-cdk keeps its own copy of these templates in
`static_files/chain/shared/aggkit/` for all the **other** repos that run e2e
against a released aggkit image. On an aggkit release (or similar cadence), open
a kurtosis-cdk PR that copies these files into kurtosis-cdk's `static_files` and
bumps the aggkit image version. Nothing in aggkit's own e2e depends on that PR
landing.
