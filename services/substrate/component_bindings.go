//go:build !wasmtime_component

package substrate

// attachComponentBindings is a no-op in the default build. Build with
// -tags wasmtime_component (and `wasmtime` on PATH at runtime) to enable the
// StarlingMonkey component engine and its KV/storage/secrets bindings; see
// component_bindings_wasmtime.go and docs/js-runtime-roadmap.md.
func (srv *Service) attachComponentBindings() error { return nil }
