# ARCH: aggsender/validator/proto

## Overview

Pure container. No `.proto` files, no generated code, no Go package at this level — only versioned subdirectories. Today that is a single `v1/` directory holding the proto source and the mechanically generated Go bindings; see `aggsender/validator/proto/v1/ARCH.md` for the internals of that version. This file exists to pin the versioning pattern itself, which upholds SPEC #1–#7.

## Patterns

- **1.** When the wire contract needs a breaking change (renamed field, reused tag, removed RPC, altered method signature), a new `v<MAJOR>/` sibling MUST be created and the change landed there; the prior version subdirectory stays frozen. This upholds SPEC #3 and #4.
- **2.** A new version directory SHOULD be seeded by copying the prior version's `.proto` and then applying the breaking edit, so unrelated messages keep identical shapes across versions and consumers can migrate incrementally.
- **3.** The `go_package` option in each version's proto MUST match `github.com/agglayer/aggkit/aggsender/validator/proto/v<MAJOR>` so generated bindings land at the import path SPEC #7 requires; mismatches cause consumers to import the wrong version silently.

## Notable decisions

- **4.** Versions are expressed as sibling directories rather than via proto-level `reserved`/`deprecated` annotations on a single evolving schema. Sibling directories give each version a distinct Go import path and a distinct proto package, so a process that must speak both versions during a migration can link both sets of bindings without symbol conflicts — which a single-directory evolution cannot provide.
