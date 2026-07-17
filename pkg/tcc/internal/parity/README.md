# parity — the legacy config compiler, kept as a conformance oracle

This is the original `config-compiler` (verbatim, moved here from
`pkg/config-compiler`). It is **not** part of the tcc compiler and is not used
at runtime. It exists only so the tcc test suite can diff the new compiler's
output against the old one and prove they agree.

`pkg/tcc/taubyte/v1`'s `TestCompile` builds the same project through both
compilers and asserts the resulting objects are byte-for-byte equal — parity is
the golden reference on the right-hand side of that comparison.

## Why it's under `internal/`

`internal/` makes Go enforce what would otherwise be a convention: only code
under `pkg/tcc/` can import this, and in practice only tcc's tests do. Nothing
in production reaches it.

## It goes away

This is temporary. Once the new tcc compiler has shipped across a few releases
with no parity drift, this package and the tests that depend on it are deleted —
the conformance tests that don't compare against it (e.g. `dsl_conformance_test`)
remain the guard after that. Don't build anything new on top of it.
