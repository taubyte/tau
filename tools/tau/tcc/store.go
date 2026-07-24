package tcc

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/schema"
	"github.com/taubyte/tau/tools/tau/common"
	"github.com/taubyte/tau/tools/tau/config"
	"github.com/taubyte/tau/tools/tau/i18n"
)

// Store is an editable view of the selected project's config repo, scoped to the
// selected application. All reads and writes go through the tcc session, so the
// CLI never needs a per-resource accessor.
type Store struct {
	s   *schema.Session
	app string
}

// Open opens a session over the selected project's config repo, in place (edits
// land on the checked-out repo the user pushes).
func Open() (*Store, error) {
	selected, err := config.GetSelectedProject()
	if err != nil {
		i18n.Help().BeSureToSelectProject()
		return nil, err
	}
	cfg, err := config.Projects().Get(selected)
	if err != nil {
		i18n.Help().BeSureToCloneProject()
		return nil, err
	}
	st, err := OpenAt(cfg.ConfigLoc())
	if err != nil {
		return nil, err
	}
	st.app, _ = config.GetSelectedApplication()
	return st, nil
}

// OpenAt opens a session over a config repo at a known location — for callers
// that hold the path directly (a freshly cloned project).
func OpenAt(configDir string) (*Store, error) {
	s, err := schema.AdoptSession(afero.NewBasePathFs(afero.NewOsFs(), configDir))
	if err != nil {
		return nil, fmt.Errorf("opening project config failed with: %w", err)
	}
	return &Store{s: s}, nil
}

// ConfigDir is the config repo of a project cloned at location.
func ConfigDir(location string) string {
	return filepath.Join(location, common.ConfigRepoDir)
}

// Session exposes the underlying session for whole-config operations.
func (st *Store) Session() *schema.Session { return st.s }

// Application is the selected application, empty at project scope.
func (st *Store) Application() string { return st.app }

// res is the session path of one resource's document in the current scope. A
// container kind's instance is a directory, so its document is the config file
// inside it, and it is never application-scoped (containers don't nest).
func (st *Store) res(group, name string) []string {
	if isContainer(group) {
		return []string{group, name, rootDoc}
	}
	if st.app != "" {
		return []string{containerDir(), st.app, group, name}
	}
	return []string{group, name}
}

// dir is the session path of a resource group in the current scope.
func (st *Store) dir(group string) []string {
	if st.app != "" && !isContainer(group) {
		return []string{containerDir(), st.app, group}
	}
	return []string{group}
}

// containerDir is the directory the DSL authors application-scoped resources
// under — the container group's own dir — so the scope prefix follows the DSL
// rather than a hardcoded "applications".
func containerDir() string {
	groups, err := Groups()
	if err != nil {
		return ""
	}
	for _, g := range groups {
		if g.Container {
			return g.Dir
		}
	}
	return ""
}

// rootDoc is the config document a directory-shaped resource — an application,
// or the project itself — keeps its own fields in, next to the resources it
// contains. This is the DSL's layout (the same convention the JS client's
// resourceParts encodes); the session addresses it as a plain path segment.
const rootDoc = "config"

// flush persists the session's pending edits back to the config repo in place,
// via the session's own Save — the same primitive the wasm binding uses, no
// session-level additions needed.
func (st *Store) flush() error { return st.s.Save(st.s.FS(), "/") }

func isContainer(group string) bool {
	groups, err := Groups()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g.Dir == group {
			return g.Container
		}
	}
	return false
}

// List names the resources of a group in the current scope.
func (st *Store) List(group string) ([]string, error) {
	names, err := st.s.List(st.dir(group))
	if err != nil {
		return nil, nil // an absent group directory is simply empty
	}
	return names, nil
}

// Doc reads one resource's whole document.
func (st *Store) Doc(group, name string) (Doc, error) {
	v, err := st.s.Get(st.res(group, name), nil)
	if err != nil {
		return nil, err
	}
	d, _ := v.(map[string]any)
	if d == nil {
		return Doc{}, nil
	}
	return Doc(d), nil
}

// ProjectID is the config repo's project id, used to derive resource ids.
func (st *Store) ProjectID() (string, error) {
	v, err := st.s.Get([]string{rootDoc}, []string{"id"})
	if err != nil {
		return "", err
	}
	id, _ := v.(string)
	return id, nil
}

// SetProject writes fields of the project's own root document (id, name,
// description, cloud bindings, ...) — the same DSL, one level up from the
// resources.
func (st *Store) SetProject(fields map[string]any) error {
	for path, value := range fields {
		if err := st.s.Set([]string{rootDoc}, strings.Split(path, "/"), value); err != nil {
			return err
		}
	}
	return st.flush()
}

// Write applies doc to a resource as the minimal set of field writes and
// deletes, so untouched YAML (comments included) is preserved.
func (st *Store) Write(group, name string, doc Doc) error {
	res := st.res(group, name)
	prev, _ := st.Doc(group, name)
	for _, op := range diff(prev, doc, nil) {
		var err error
		if op.del {
			err = st.s.Delete(res, op.path)
		} else {
			err = st.s.Set(res, op.path, op.value)
		}
		if err != nil {
			return err
		}
	}
	return st.flush()
}

// Delete removes a resource. A container's instance is a directory, so its
// document goes through the session (keeping it consistent) and the directory —
// with whatever it still contained — goes with it.
func (st *Store) Delete(group, name string) error {
	if err := st.s.Delete(st.res(group, name), nil); err != nil {
		return err
	}
	if err := st.flush(); err != nil {
		return err
	}
	if isContainer(group) {
		return st.s.FS().RemoveAll(path.Join(group, name))
	}
	return nil
}

// ValidateField runs the DSL's compile-free check for one field value. A
// container's own document (an application's config) carries no single-value
// validators, and the session derives the group from the [group, name] tail of
// the path — which a container's [group, name, config] path doesn't have — so
// per-field validation is skipped for it here; the whole-config Validate still
// covers it.
func (st *Store) ValidateField(group, name string, field []string, value any) error {
	if isContainer(group) {
		return nil
	}
	return st.s.ValidateField(st.res(group, name), field, value)
}

// Complete lists the allowed values of a field — enum members and, for a
// reference, the in-scope resources it may point at.
func (st *Store) Complete(group, name string, field []string) []string {
	c, err := st.s.Complete(st.res(group, name), field, "")
	if err != nil {
		return nil
	}
	return c
}

type op struct {
	path  []string
	value any
	del   bool
}

// diff is the minimal set/delete ops turning prev into next. Maps recurse;
// arrays and scalars are leaves.
func diff(prev, next map[string]any, base []string) []op {
	var ops []op
	for k, nv := range next {
		path := append(append([]string{}, base...), k)
		if nm, ok := nv.(map[string]any); ok {
			pm, _ := prev[k].(map[string]any)
			ops = append(ops, diff(pm, nm, path)...)
			continue
		}
		if !equal(prev[k], nv) {
			ops = append(ops, op{path: path, value: nv})
		}
	}
	for k := range prev {
		if _, ok := next[k]; !ok {
			ops = append(ops, op{path: append(append([]string{}, base...), k), del: true})
		}
	}
	return ops
}

func equal(a, b any) bool {
	as, aok := asList(a)
	bs, bok := asList(b)
	if aok || bok {
		if len(as) != len(bs) {
			return false
		}
		for i := range as {
			if as[i] != bs[i] {
				return false
			}
		}
		return true
	}
	return a == b
}

func asList(v any) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			out = append(out, fmt.Sprint(e))
		}
		return out, true
	}
	return nil, false
}
