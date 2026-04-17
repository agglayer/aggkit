# ARCH: bridgesync/types

## Overview

A single file (`types.go`) defines the `LeafType` uint8 enum with its named values, a `String()` method backed by a fixed-index string array, and a custom `UnmarshalJSON` that first matches the two known string labels and otherwise falls back to parsing the raw bytes as a decimal number via `fmt.Sscanf`. The `EmptyLER` sentinel is computed once at package init via `common.HexToHash`. Upholds SPEC #1–#10.

## Notable decisions

- **1.** `UnmarshalJSON` accepts a numeric fallback (not just the two named string labels) so that persisted or peer-produced payloads encoding the leaf type as an integer decode successfully without requiring a schema migration. This upholds SPEC #8 and is why SPEC #9 only errors on non-numeric unknown strings rather than on any unknown value.
- **2.** `String()` uses a fixed two-element array indexed by the enum value rather than a switch. Adding a third named kind therefore requires extending both the enum constants and the array in lockstep — a diff touching one without the other will panic at runtime for the new value.
