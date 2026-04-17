# SPEC: bridgesync/types

## Summary

Shared value types used by the bridge-sync subsystem to classify and represent bridge leaves. Primarily provides an enumeration of leaf kinds (asset transfer vs. cross-chain message) and the canonical hash value representing an empty local exit root (LER). These definitions are the wire- and storage-level vocabulary that upstream/downstream components of bridge sync agree on.

## Requirements

- **1.** The leaf-type enumeration MUST define exactly two kinds: an asset-transfer kind and a message kind.
- **2.** The asset-transfer kind MUST have the numeric value `0`.
- **3.** The message kind MUST have the numeric value `1`.
- **4.** The leaf-type enumeration MUST be representable as an unsigned 8-bit integer preserving its numeric value.
- **5.** The leaf-type enumeration MUST render to the string `"Transfer"` for the asset-transfer kind and `"Message"` for the message kind when formatted as a string.
- **6.** JSON decoding of the leaf-type enumeration MUST accept the quoted string `"Transfer"` as the asset-transfer kind.
- **7.** JSON decoding of the leaf-type enumeration MUST accept the quoted string `"Message"` as the message kind.
- **8.** JSON decoding of the leaf-type enumeration MUST accept a bare JSON number and interpret it as the underlying numeric value of the enumeration, without validating that the number maps to a named kind.
- **9.** JSON decoding MUST return an error when the input is a quoted string that is neither `"Transfer"`, `"Message"`, nor parseable as a number.
- **10.** An empty-LER sentinel hash value MUST be exported with the fixed 32-byte value `0x27ae5ba08d7291c96c8cbddcc148bf48a6d68c7974b94356f53754ef6171d757`.

## External interface

Exported identifiers that other packages depend on:

- `LeafType` — an unsigned 8-bit enumeration type with methods `Uint8() uint8`, `String() string`, and `UnmarshalJSON([]byte) error`.
- `LeafTypeAsset` — the asset-transfer value of `LeafType` (numeric `0`).
- `LeafTypeMessage` — the message value of `LeafType` (numeric `1`).
- `EmptyLER` — a 32-byte hash constant representing the empty local exit root.

## Error modes

- **11.** When `String()` is invoked on a leaf-type value outside the defined named range, behavior is undefined by this contract; callers MUST NOT rely on `String()` for leaf-type values other than the asset-transfer and message kinds.

## Out of scope

- Defining the on-chain or cross-domain semantics of what an asset transfer or message actually does — this directory only supplies the classifying type.
- Persistence, serialization wire format beyond JSON decoding of the enumeration, and hashing logic for leaves.
- Any other bridge-related record types (claims, deposits, events) — those live in the parent `bridgesync` package and its siblings.
