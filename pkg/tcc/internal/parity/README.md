# parity — frozen legacy code, kept as conformance oracles

`parity/` holds verbatim copies of the old tau code paths that the new tcc
pipeline replaces. Nothing here runs in production; each package exists only so
tcc's tests can diff the new output against the old and prove they still agree.

- `config-compiler/` — the legacy config compiler (the first inhabitant).

## What's frozen

- `config-compiler/` — the legacy compiler (the conformance oracle).
- `schema/`, `specs/`, `yaseer/`, `utils/mapstructure/`, `utils/maps/` — verbatim copies of
  config-compiler's entire input-processing stack, so tcc-gen's regeneration of live `pkg/schema`
  cannot move the reference the oracle compiles against. `config-compiler` imports these, not the
  live packages.

## Load-bearing invariants (do not "complete" the freeze)

- **`core/common/repositorytype` MUST stay live — never copy it here.** It is the only non-primitive
  type that crosses the compared compiler output (`indexer/website.go`, `indexer/library.go` write
  `repositorytype.WebsiteRepository`/`LibraryRepository` into `.Indexes()`). The new compiler writes
  the same live type. `repositorytype.Type` is an `int`; a frozen copy would make `cmp.Equal` fail on
  frozen-`Type(4)` vs live-`Type(4)`. Same reasoning applies to any other type that appears verbatim
  (not stringified) in the compared `.Object()`/`.Indexes()` maps.
- **`fixtures/config` (under `pkg/tcc/taubyte/v1`) is frozen input.** It is generated from LIVE schema
  by `v1/fixtures/gen_*.go`; do not regenerate it after tcc-gen rewrites `pkg/schema`, or the oracle's
  input changes silently on the old side only.

As tcc-gen absorbs more of the old pipeline, more reference code lands here under the same rule:
frozen, `internal/` to `pkg/tcc`, imported only by tests.

Everything under `parity/` is temporary. A package leaves once its new counterpart has shipped across
a few releases with no drift.
