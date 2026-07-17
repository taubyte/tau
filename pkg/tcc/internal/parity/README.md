# parity — frozen legacy code, kept as conformance oracles

`parity/` holds verbatim copies of the old tau code paths that the new tcc
pipeline replaces. Nothing here runs in production; each package exists only so
tcc's tests can diff the new output against the old and prove they still agree.

- `config-compiler/` — the legacy config compiler (the first inhabitant).

As tcc-gen absorbs more of the old pipeline, more reference code lands here
(e.g. schema) under the same rule: frozen, `internal/` to `pkg/tcc`, imported
only by tests.

Everything under `parity/` is temporary. A package leaves once its new
counterpart has shipped across a few releases with no drift.
