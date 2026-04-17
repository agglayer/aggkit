# ARCH: aggsender/config

## Overview

Two files: `config.go` declares the three config structs (`Config`, `TriggerASAPConfig`, `TriggerEpochBasedConfig`) with `mapstructure` tags, a `NewTriggerASAPConfigDefault` factory, `String` renderers, and `Validate` methods; `config_test.go` covers the validation gating and the `String` output.

`Config.Validate` is a flat sequence of delegated sub-validations — one per field that has its own `Validate` — each wrapped with `fmt.Errorf("...: %w", err)` on failure. The only conditional is that `AggkitProverClient.Validate` is invoked solely when `Mode == AggchainProofMode`; this upholds SPEC #3 and #4. The remaining sub-validations (Agglayer client, retry policy, retention policy, L1-info-tree finality, trigger mode) are unconditional and uphold SPEC #2, #5, #6, #7, #8. Error wrapping with `%w` upholds SPEC #9.

`TriggerASAPConfig.Validate` is two explicit numeric guards on `DelayBetweenCertificates` and `MinimumNewCertificateInterval`, upholding SPEC #10 and #11. `NewTriggerASAPConfigDefault` hard-codes the defaults in SPEC #12.

`Config.String` is hand-written concatenation of selected fields — not every field. The signer sub-config is rendered by its `Method` only (upholds SPEC #14). `CheckCertConfigBriefString` is a compact two-field helper used by log lines elsewhere.

## Patterns

- **1.** Every new configuration field added to `Config` MUST carry a `mapstructure:"<Name>"` tag matching the external key name, because decoding depends on the tag (not the Go field name).
- **2.** Any new sub-config whose own package exposes a `Validate()` method SHOULD be wired into `Config.Validate` with `fmt.Errorf("<human-readable sub-component name>: %w", err)`, preserving the wrap-on-fail pattern future SPEC evaluators rely on.
- **3.** Fields that carry sensitive material (private keys, secrets) MUST NOT be rendered verbatim by `Config.String`; render only a non-secret descriptor (e.g., signer method identifier).

## Notable decisions

- **4.** `TriggerASAPConfig.Validate` requires `MinimumNewCertificateInterval > 0` but allows `DelayBetweenCertificates == 0`. The asymmetry is deliberate: a zero minimum interval would let the trigger loop busy-spin, whereas a zero delay between certificates is a valid "as fast as possible" request.
- **5.** Enum-valued fields (`Mode`, `BlockFinalityForL1InfoTree`, `TriggerCertMode`) carry `jsonschema:"enum=..."` tags in addition to `mapstructure`. The jsonschema tags are load-bearing for the generated config schema consumed by operators and MUST be kept in sync with the values accepted by the corresponding `Validate` method in the type's own package.
- **6.** `Config.String` is intentionally a partial render (not a reflect-based dump of every field). This keeps log output bounded and avoids accidentally leaking newly-added sensitive fields; the cost is that new non-sensitive fields are not auto-logged and must be added explicitly if they matter for operator diagnostics.
