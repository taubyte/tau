package interp

import (
	"fmt"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
)

// EnvKeyedPromoteSpec describes a compile-only projection of a nested map: pick the
// Source map's entry keyed by env[EnvVar], hoist its Fields to the enclosing scope as
// flat scalars, then drop the whole Source map. It is the generic, schema-agnostic
// form of the runtime key selection no static annotation can express — the compile
// environment supplies the key. (Used by `clouds`: Source "clouds", EnvVar "cloud".)
type EnvKeyedPromoteSpec struct {
	Source           string   // map group key, e.g. "clouds"
	EnvVar           string   // env var holding the entry key, e.g. "cloud"
	Fields           []string // fields hoisted from the selected entry, e.g. account, plan
	RequireAllOrNone bool     // selected entry's Fields must be all-set or all-empty
}

// PromoteEnvKeyed annotates a group iterator with an EnvKeyedPromoteSpec; the
// CompileDriver runs it in place of the per-instance walk. The entry is selected by
// Child(env[EnvVar]) — a single path segment — so keys containing dots (FQDNs) stay
// intact; nothing splits a dotted string.
func PromoteEnvKeyed(source, envVar string, fields []string, requireAllOrNone bool) engine.NodeOption {
	return engine.GroupAnnotate("promoteEnvKeyed", EnvKeyedPromoteSpec{
		Source: source, EnvVar: envVar, Fields: fields, RequireAllOrNone: requireAllOrNone,
	})
}

// runPromoteEnvKeyed reproduces the old FlattenClouds semantics, generalized to N
// fields:
//   - Source map absent           -> no-op (nothing to drop).
//   - env[EnvVar] empty           -> drop Source, promote nothing.
//   - selected entry absent       -> drop Source, promote nothing.
//   - entry with all Fields set   -> set each field on scope, drop Source.
//   - entry with some-but-not-all -> error if RequireAllOrNone, else promote the set ones.
//   - entry with no Fields set    -> drop Source, promote nothing.
func runPromoteEnvKeyed(spec EnvKeyedPromoteSpec, env Env, scope object.Object[object.Refrence]) error {
	sourceObj, err := scope.Child(spec.Source).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s map failed with %w", spec.Source, err)
	}
	defer scope.Delete(spec.Source)

	key := env[spec.EnvVar]
	if key == "" {
		return nil
	}

	entryObj, err := sourceObj.Child(key).Object()
	if err == object.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reading %s[%q] failed with %w", spec.Source, key, err)
	}

	vals := make([]string, len(spec.Fields))
	anySet, allSet := false, true
	for i, f := range spec.Fields {
		v, _ := entryObj.GetString(f)
		vals[i] = v
		if v == "" {
			allSet = false
		} else {
			anySet = true
		}
	}
	if spec.RequireAllOrNone && anySet && !allSet {
		return fmt.Errorf("%s[%q] is incomplete; fields %v must all be set or all omitted", spec.Source, key, spec.Fields)
	}
	if !anySet {
		return nil
	}
	for i, f := range spec.Fields {
		if vals[i] == "" {
			continue
		}
		scope.Set(f, vals[i])
	}
	return nil
}
