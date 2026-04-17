# SPEC: aggsender/validator/proto

## Summary

Root of the versioned protocol-buffer schemas for the aggsender-to-validator gRPC contract. This directory owns no schema itself — every concrete message, service, and RPC lives under a versioned subdirectory (`v1/`, future `v2/`, ...). The directory's purpose is to frame how those versions relate: which version is current, what a "new version" means for wire compatibility, and the rule that each version is an independently-importable package rather than a patch on top of a previous one.

Consumers (aggsender clients, validator server implementations, mocks) pick a specific version subdirectory to depend on; this level exposes no types of its own.

## Requirements

- **1.** Every concrete schema file under this directory MUST live in a version-suffixed subdirectory matching the pattern `v<MAJOR>/`; proto files MUST NOT be placed directly at this level.
- **2.** Each version subdirectory MUST declare a distinct proto package of the form `aggkit.aggsender.validator.v<MAJOR>`, so generated bindings from different versions coexist without symbol collisions.
- **3.** A version subdirectory, once published, MUST NOT receive a wire-breaking edit to its schema: field tag numbers, field names, message names, and RPC method names fixed at publication are part of the contract for that version and MUST remain stable for the lifetime of the subdirectory.
- **4.** A wire-breaking change to the aggsender-validator contract MUST be introduced by adding a new version subdirectory rather than by mutating an existing one.
- **5.** Wire-compatible additions (new fields with unused tag numbers, new RPCs, new message types) within a version subdirectory MUST preserve the ability of existing clients and servers using that version to interoperate unchanged.
- **6.** The current version (the one aggsender and validator implementations in this repo target by default) is `v1/`; see `aggsender/validator/proto/v1/SPEC.md#1` for its service surface.
- **7.** Each version's generated Go package import path MUST be `github.com/agglayer/aggkit/aggsender/validator/proto/v<MAJOR>`, so a consumer pinning to a specific version has a stable, unambiguous Go import.

## Out of scope

- Message, service, and RPC semantics — those are owned by the version subdirectory under which they are declared (e.g., `aggsender/validator/proto/v1/SPEC.md`).
- Transport, authentication, deadlines, and retry policy — owned by the caller of whichever version is selected.
- Deprecation and sunset policy for old versions — not currently specified; when multiple versions exist this SPEC will need a claim governing overlap and removal.

## Children

- `v1/` — current version of the aggsender-validator gRPC schema; see `aggsender/validator/proto/v1/SPEC.md#1`.
