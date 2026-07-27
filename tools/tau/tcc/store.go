package tcc

import (
	"fmt"
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

// res is the session address of one resource in the current scope. The scoping
// rule — which kinds nest under an application, and that a container's own
// instance never does — belongs to the session, not here.
func (st *Store) res(group, name string) []string {
	r, err := st.s.Address(group, name, st.app)
	if err != nil {
		// Unreachable by construction: every caller passes a Group.Dir, which
		// comes from the same DSL the session resolves against. Degrade to the
		// project-scope form rather than panic in a CLI.
		return []string{group, name}
	}
	return r
}

// flush persists the session's pending edits back to the config repo in place,
// via the session's own Save — the same primitive the wasm binding uses, no
// session-level additions needed.
func (st *Store) flush() error { return st.s.Save(st.s.FS(), "/") }

// List names the resources of a group in the current scope.
func (st *Store) List(group string) ([]string, error) {
	names, err := st.s.Names(group, st.app)
	if err != nil {
		return nil, nil // an unknown or absent group is simply empty
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
	v, err := st.s.Get(st.s.Root(), []string{"id"})
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
		if err := st.s.Set(st.s.Root(), strings.Split(path, "/"), value); err != nil {
			return err
		}
	}
	return st.flush()
}

// Write applies doc to a resource as the minimal set of field writes and
// deletes, so untouched YAML (comments included) is preserved. The diff itself
// lives in the session (Session.SetResource) — the web console needs the same
// thing, and neither should carry its own copy.
func (st *Store) Write(group, name string, doc Doc) error {
	if err := st.s.SetResource(st.res(group, name), doc); err != nil {
		return err
	}
	return st.flush()
}

// Delete removes a resource. A container's instance is a directory, and the
// session removes it whole — with whatever it still contained.
func (st *Store) Delete(group, name string) error {
	if err := st.s.Delete(st.res(group, name), nil); err != nil {
		return err
	}
	return st.flush()
}

// ValidateField runs the DSL's compile-free check for one field value. A
// container's own document is addressed like any other resource, so its fields
// are validated the same way.
func (st *Store) ValidateField(group, name string, field []string, value any) error {
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
