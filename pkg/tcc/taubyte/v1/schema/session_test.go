package schema

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/afero"
	"gotest.tools/v3/assert"
)

// A session over the real fixtures validates; a fork can make a breaking edit,
// fail validation in isolation, and be discarded without touching the parent —
// or make a good edit, validate, and merge.
func TestSession(t *testing.T) {
	fixtures := filepath.Join("..", "fixtures", "config")
	ctx := context.Background()

	s, err := NewSession(afero.NewOsFs(), fixtures)
	assert.NilError(t, err)

	// baseline: the whole config validates
	vals, err := s.Validate(ctx, CompileOptions{})
	assert.NilError(t, err)
	assert.Assert(t, len(vals) > 0, "expected deferred checks")

	t.Run("fork with a bad edit fails validation, parent untouched", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		assert.NilError(t, fork.Set([]string{"functions", "test_function1_glob"}, []string{"source"}, "not_a_ref"))

		_, err = fork.Validate(ctx, CompileOptions{})
		assert.ErrorContains(t, err, `must be "." or start with "libraries/"`)

		// parent still validates — the bad edit never touched it
		_, err = s.Validate(ctx, CompileOptions{})
		assert.NilError(t, err)
	})

	t.Run("partial validation is compile-free and scoped", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		// field-level: enum on trigger.type
		assert.NilError(t, s.ValidateField(fn, []string{"trigger", "type"}, "https"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "type"}, "nope"), "invalid value")

		// resource-level: the fixture function is locally valid...
		assert.Equal(t, len(s.ValidateResource(fn)), 0)
		// ...set a bad enum, and ValidateResource surfaces it (no compile).
		assert.NilError(t, s.Set(fn, []string{"trigger", "type"}, "nope"))
		issues := s.ValidateResource(fn)
		assert.Equal(t, len(issues), 1)
		assert.DeepEqual(t, issues[0].Field, []string{"trigger", "type"})
		assert.Assert(t, strings.Contains(issues[0].Message, "invalid value"))
		// undo
		assert.NilError(t, s.Set(fn, []string{"trigger", "type"}, "http"))
	})

	t.Run("partial validation: unknown fields error, scalar formats are checked", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		// (a) an unknown path is reported as unknown, not silently OK
		assert.ErrorContains(t, s.ValidateField(fn, []string{"nonexistent"}, "x"), "unknown field")
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "typo"}, "x"), "unknown field")
		// a known field with no constraint is still valid
		assert.NilError(t, s.ValidateField(fn, []string{"description"}, "anything"))
		// (b) duration/bytes format is checked per-field, not only at compile
		assert.NilError(t, s.ValidateField(fn, []string{"execution", "timeout"}, "20s"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"execution", "timeout"}, "20x"), "invalid duration")
		assert.NilError(t, s.ValidateField(fn, []string{"execution", "memory"}, "32GB"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"execution", "memory"}, "banana"), "invalid size")

		// a legacy Compat alias the accessors accept is recognized too — not
		// "unknown" — and its reference is still checked. "domains" is the compat
		// alias of the canonical "trigger/domains".
		assert.NilError(t, s.ValidateField(fn, []string{"domains"}, []any{"test_domain1"}))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"domains"}, []any{"ghost"}), `no domains named "ghost"`)
		// a list element is addressable by index — valid for a free-form list, but a
		// genuinely unknown path still errors
		assert.NilError(t, s.ValidateField(fn, []string{"tags", "0"}, "anything"))
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "foo"}, "x"), "unknown field")
	})

	t.Run("validateField enforces each field's declared type, not only format validators", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		// trigger.local is a boolean whose ONLY constraint is its type — previously
		// anything passed. A non-bool is now rejected.
		for _, bad := range []any{"true6", 123, "nope"} {
			assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "local"}, bad), "expects boolean")
		}
		assert.NilError(t, s.ValidateField(fn, []string{"trigger", "local"}, true))
		// tags is an array of strings — a bare string is not a list.
		assert.ErrorContains(t, s.ValidateField(fn, []string{"tags"}, "notalist"), "expects array")
		assert.NilError(t, s.ValidateField(fn, []string{"tags"}, []any{"a", "b"}))
		// a string field rejects a non-string before its format validator runs.
		assert.ErrorContains(t, s.ValidateField(fn, []string{"execution", "timeout"}, true), "expects string")

		// the resource-level check catches a wrong-typed value too (via a fork, so
		// the shared session stays untouched — no merge).
		fork, err := s.Fork()
		assert.NilError(t, err)
		assert.NilError(t, fork.Set(fn, []string{"trigger", "local"}, "true6")) // raw write
		var found bool
		for _, i := range fork.ValidateResource(fn) {
			found = found || strings.Contains(i.Message, "expects boolean")
		}
		assert.Assert(t, found, "ValidateResource should flag the mistyped boolean")
	})

	t.Run("list elements are addressable by index for validate and complete", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		elem := []string{"trigger", "domains", "0"} // one element of the domains list
		// per-element reference validation
		assert.NilError(t, s.ValidateField(fn, elem, "test_domain1"))
		assert.ErrorContains(t, s.ValidateField(fn, elem, "ghost"), `no domains named "ghost"`)
		// per-element completion
		c, err := s.Complete(fn, elem, "test_")
		assert.NilError(t, err)
		assert.DeepEqual(t, c, []string{"test_domain1"})
	})

	t.Run("partial validation catches bad references in scope, compile-free", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"} // a root function
		// a domain that doesn't exist -> flagged (was silent before)
		assert.ErrorContains(t, s.ValidateField(fn, []string{"trigger", "domains"}, []any{"ghost"}), `no domains named "ghost"`)
		// an existing global domain -> ok
		assert.NilError(t, s.ValidateField(fn, []string{"trigger", "domains"}, []any{"test_domain1"}))
		// a library only defined in test_app1 is out of scope for a root function
		assert.ErrorContains(t, s.ValidateField(fn, []string{"source"}, "libraries/test_library2"), `no libraries named "test_library2"`)
		// "." is a literal, not a reference -> ok
		assert.NilError(t, s.ValidateField(fn, []string{"source"}, "."))

		// resource-level surfaces it too
		assert.NilError(t, s.Set(fn, []string{"trigger", "domains"}, []any{"ghost"}))
		issues := s.ValidateResource(fn)
		assert.Assert(t, len(issues) == 1)
		assert.DeepEqual(t, issues[0].Field, []string{"trigger", "domains"})
		assert.Assert(t, strings.Contains(issues[0].Message, `no domains named "ghost"`))
		assert.NilError(t, s.Set(fn, []string{"trigger", "domains"}, []any{"test_domain1"})) // undo
	})

	complete := func(t *testing.T, res, field []string, partial string) []string {
		t.Helper()
		c, err := s.Complete(res, field, partial)
		assert.NilError(t, err)
		return c
	}

	t.Run("completion: enum members and scoped references, filtered by the partial", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"} // a root function

		// enum field — partial filters the members
		all := complete(t, fn, []string{"trigger", "type"}, "")
		assert.Assert(t, slices.Contains(all, "pubsub") && slices.Contains(all, "http"))
		assert.DeepEqual(t, complete(t, fn, []string{"trigger", "type"}, "p"), []string{"pubsub", "p2p"})

		// reference field — the shape literal "." plus in-scope libraries, prefixed.
		// Root scope sees the global library test_library1 (not app1's test_library2).
		src := complete(t, fn, []string{"source"}, "")
		assert.Assert(t, slices.Contains(src, "."), "source offers the inline literal")
		assert.Assert(t, slices.Contains(src, "libraries/test_library1"), "source offers the global library")
		assert.Assert(t, !slices.Contains(src, "libraries/test_library2"), "a root function must not see app1's library")

		// the user's partial narrows it
		assert.DeepEqual(t, complete(t, fn, []string{"source"}, "libraries/test_l"), []string{"libraries/test_library1"})
		assert.DeepEqual(t, complete(t, fn, []string{"source"}, "."), []string{"."})

		// compat alias resolves for completion too, and an unknown path errors
		assert.DeepEqual(t, complete(t, fn, []string{"domains"}, ""), []string{"test_domain1"})
		_, err := s.Complete(fn, []string{"nonexistent"}, "")
		assert.ErrorContains(t, err, "unknown field")
	})

	t.Run("completion: an app function also sees its own app's libraries", func(t *testing.T) {
		appFn := []string{"applications", "test_app1", "functions", "test_function2"}
		src := complete(t, appFn, []string{"source"}, "libraries/")
		assert.Assert(t, slices.Contains(src, "libraries/test_library2"), "app scope sees app1's library")
		assert.Assert(t, slices.Contains(src, "libraries/test_library1"), "and the global one")
	})

	// The crux: a container instance is addressed by its DSL-group form, and that
	// address resolves to the document INSIDE its directory — never to a sibling
	// applications/<name>.yaml, which is what split an edited app into two files.
	t.Run("a container instance is one address, mapped to its own document", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		app := []string{"applications", "test_app1"}

		assert.NilError(t, fork.Set(app, []string{"description"}, "edited"))
		path, data, err := fork.Serialize(app)
		assert.NilError(t, err)
		assert.Equal(t, path, "applications/test_app1/config.yaml")
		assert.Assert(t, strings.Contains(string(data), "edited"))
		_, err = fork.FS().Stat("/applications/test_app1.yaml")
		assert.Assert(t, err != nil, "must not create a sibling applications/test_app1.yaml")

		// the same address validates as the container group, not as "test_app1"
		assert.Equal(t, len(fork.ValidateResource(app)), 0)
		assert.ErrorContains(t, fork.ValidateField(app, []string{"name"}, "not a var name!"), "invalid variable name")

		// a brand-new application lands in its own directory too
		fresh := []string{"applications", "brand_new"}
		assert.NilError(t, fork.Set(fresh, []string{"id"}, "QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o"))
		path, _, err = fork.Serialize(fresh)
		assert.NilError(t, err)
		assert.Equal(t, path, "applications/brand_new/config.yaml")

		// the legacy config-suffixed address (tau-cli's) still resolves there
		path, _, err = fork.Serialize([]string{"applications", "test_app1", "config"})
		assert.NilError(t, err)
		assert.Equal(t, path, "applications/test_app1/config.yaml")
	})

	t.Run("a structurally bogus address errors instead of corrupting the tree", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		// used to silently create /applications.yaml next to the real directory
		assert.ErrorContains(t, fork.Set([]string{"applications"}, []string{"a", "id"}, "x"), "not a resource address")
		assert.ErrorContains(t, fork.Set([]string{"functions", "x", "config"}, []string{"id"}, "x"), "not a resource address")
		assert.ErrorContains(t, fork.Set([]string{"functions", "a", "b", "c", "d"}, []string{"id"}, "x"), "not a resource address")
		_, err = fork.FS().Stat("/applications.yaml")
		assert.Assert(t, err != nil, "no sibling document was created")
	})

	t.Run("Serialize returns one resource's file and exact YAML, comments kept", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		fn := []string{"functions", "test_function1_glob"}
		assert.NilError(t, fork.Set(fn, []string{"description"}, "serialized"))
		path, data, err := fork.Serialize(fn)
		assert.NilError(t, err)
		assert.Equal(t, path, "functions/test_function1_glob.yaml")
		assert.Assert(t, strings.Contains(string(data), "serialized"))
		_, _, err = fork.Serialize([]string{"functions", "no_such_function"})
		assert.Assert(t, err != nil, "a missing resource has no document")
	})

	t.Run("SetResource applies the minimal diff and leaves untouched YAML alone", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		fn := []string{"functions", "diffed"}
		assert.NilError(t, fork.SetResource(fn, map[string]any{
			"id":          "QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o",
			"description": "first",
			"trigger":     map[string]any{"type": "https", "method": "GET"},
			"tags":        []any{"a", "b"},
		}))
		// nested change + removed key + explicit null; description is untouched
		assert.NilError(t, fork.SetResource(fn, map[string]any{
			"id":          "QmT78zSuBmuS4z925WZfrqQ1qHaJ56DQaTfyMUF7F8ff5o",
			"description": "first",
			"trigger":     map[string]any{"type": "http"},
			"tags":        nil,
		}))
		doc, err := fork.Get(fn, nil)
		assert.NilError(t, err)
		m := doc.(map[string]any)
		assert.Equal(t, m["description"], "first")
		assert.Equal(t, m["trigger"].(map[string]any)["type"], "http")
		_, stillThere := m["trigger"].(map[string]any)["method"]
		assert.Assert(t, !stillThere, "a key missing from the doc is deleted")
		v, ok := m["tags"]
		assert.Assert(t, ok && v == nil, "an explicit nil writes null rather than deleting")
	})

	t.Run("ResourceAt maps a repo path back to its canonical address", func(t *testing.T) {
		for path, want := range map[string][]string{
			"config.yaml":                      {"config"},
			"functions/x.yaml":                 {"functions", "x"},
			"applications/a/config.yaml":       {"applications", "a"},
			"applications/a/functions/x.yaml":  {"applications", "a", "functions", "x"},
			"/applications/a/functions/x.yaml": {"applications", "a", "functions", "x"},
		} {
			got, ok := s.ResourceAt(path)
			assert.Assert(t, ok, path)
			assert.DeepEqual(t, got, want)
		}
		for _, path := range []string{
			"README.md", "notes.yaml", ".git/config.yaml", "functions/x/config.yaml",
			"docs/guide.yaml", "applications/a/functions/x/y.yaml", "",
		} {
			_, ok := s.ResourceAt(path)
			assert.Assert(t, !ok, path)
		}
	})

	// A consumer that knows a kind only as a string builds no address itself.
	t.Run("kinds, addressing and existence by kind name", func(t *testing.T) {
		kinds := s.Kinds()
		byGroup := map[string]bool{}
		for _, k := range kinds {
			byGroup[k.Group] = k.Container
		}
		assert.Equal(t, byGroup["functions"], false)
		assert.Equal(t, byGroup["applications"], true, "the container says so, so no caller name-checks it")

		addr := func(kind, name, app string) []string {
			t.Helper()
			r, err := s.Address(kind, name, app)
			assert.NilError(t, err)
			return r
		}
		// the group key and the declared singular resolve alike
		assert.DeepEqual(t, addr("functions", "f", ""), []string{"functions", "f"})
		assert.DeepEqual(t, addr("function", "f", ""), []string{"functions", "f"})
		assert.DeepEqual(t, addr("function", "f", "app"), []string{"applications", "app", "functions", "f"})
		assert.DeepEqual(t, addr("application", "app", ""), []string{"applications", "app"})

		_, err := s.Address("nope", "f", "")
		assert.ErrorContains(t, err, "unknown kind")
		_, err = s.Address("application", "app", "other")
		assert.ErrorContains(t, err, "container kind")

		names, err := s.Names("function", "")
		assert.NilError(t, err)
		assert.Assert(t, slices.Contains(names, "test_function1_glob"))
		scoped, err := s.Names("function", "test_app1")
		assert.NilError(t, err)
		assert.Assert(t, slices.Contains(scoped, "test_function2"))
		assert.Assert(t, !slices.Contains(scoped, "test_function1_glob"))

		assert.Equal(t, s.Exists([]string{"functions", "test_function1_glob"}), true)
		assert.Equal(t, s.Exists([]string{"functions", "ghost"}), false)
		assert.Equal(t, s.Exists([]string{"applications", "test_app1"}), true)
		assert.Equal(t, s.Exists(s.Root()), true)
	})

	t.Run("Generate mints a resource id per the DSL, not per consumer", func(t *testing.T) {
		fn := []string{"functions", "test_function1_glob"}
		got, err := s.Generate(fn, []string{"id"})
		assert.NilError(t, err)
		assert.NilError(t, s.ValidateField(fn, []string{"id"}, got))
		again, err := s.Generate(fn, []string{"id"})
		assert.NilError(t, err)
		assert.Assert(t, got != again, "each call mints a fresh id")
		_, err = s.Generate(fn, []string{"description"})
		assert.ErrorContains(t, err, "not a generated field")
	})

	t.Run("fork with a good edit validates and merges", func(t *testing.T) {
		fork, err := s.Fork()
		assert.NilError(t, err)
		assert.NilError(t, fork.Set([]string{"functions", "test_function1_glob"}, []string{"description"}, "edited via fork"))

		_, err = fork.Validate(ctx, CompileOptions{})
		assert.NilError(t, err)
		assert.NilError(t, fork.Merge())

		got, err := s.Get([]string{"functions", "test_function1_glob"}, []string{"description"})
		assert.NilError(t, err)
		assert.Equal(t, got, "edited via fork")
	})
}
