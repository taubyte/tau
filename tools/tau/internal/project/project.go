// Package project is a v2 project directory as the CLI edits it, through the
// schema's editable session (pkg/tcc/session via pkg/tcc/taubyte/v2/schema).
//
// EVERYTHING ABOUT THE VOCABULARY COMES FROM THE SESSION: which kinds exist, a
// resource's address and file, its id, which fields it still owes. Nothing here
// names a kind or a field; the one piece of content that does is the starter
// project, which is a document an author would write, compiled by its test.
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/session"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Project is an open project directory.
type Project struct {
	dir     string
	s       *schema.Session
	touched map[string][]string
}

// Open stages the configuration documents under dir — its yaml, and nothing in
// a dot-directory (.git, .tau) — into a private session.
func Open(dir string) (*Project, error) {
	mem := afero.NewMemMapFs()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".yaml" {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return afero.WriteFile(mem, "/"+filepath.ToSlash(rel), data, 0o644)
	})
	if err != nil {
		return nil, err
	}
	s, err := schema.AdoptSession(mem)
	if err != nil {
		return nil, err
	}
	return &Project{dir: dir, s: s, touched: map[string][]string{}}, nil
}

// IsProject reports whether dir holds a project root document.
func (p *Project) IsProject() bool { return p.s.Exists(p.s.Root()) }

// Kinds are the resource kinds the vocabulary declares, sorted by name.
func Kinds() []session.Kind {
	p, err := schema.AdoptSession(afero.NewMemMapFs())
	if err != nil {
		return nil
	}
	kinds := p.Kinds()
	sort.Slice(kinds, func(i, j int) bool { return kinds[i].Name < kinds[j].Name })
	return kinds
}

// identity is what the vocabulary calls a resource's id and name fields.
func identity() (id, name string) { return engine.IdentityOf(schema.CompileRoot()) }

// Name is the project's name, read from its root document.
func (p *Project) Name() string {
	_, nameField := identity()
	v, err := p.s.Get(p.s.Root(), []string{nameField})
	if err != nil {
		return ""
	}
	name, _ := v.(string)
	return name
}

// ID is the project's id, read from its root document: the CID tcc's
// generator minted, which is the id the project is linked and deployed under.
func (p *Project) ID() string {
	idField, _ := identity()
	v, err := p.s.Get(p.s.Root(), []string{idField})
	if err != nil {
		return ""
	}
	id, _ := v.(string)
	return id
}

// Issue is a field a resource still owes, or holds wrongly.
type Issue = session.FieldIssue

// New adds a resource of kind named name, inside scope ("" for the project
// root; a/b for nested scopes), with the field values given — each a path of
// segments joined by "." and a yaml value. Its id is minted by the vocabulary's
// generator. It returns the resource's address and what it still owes.
func (p *Project) New(kind, name, scope string, values map[string]string) ([]string, []Issue, error) {
	res, err := p.s.Address(kind, name, scope)
	if err != nil {
		return nil, nil, err
	}
	if p.s.Exists(res) {
		return nil, nil, fmt.Errorf("%s already exists", strings.Join(res, "/"))
	}
	idField, _ := identity()
	id, err := p.s.Generate(res, []string{idField})
	if err != nil {
		return nil, nil, err
	}
	if err := p.s.Set(res, []string{idField}, id); err != nil {
		return nil, nil, err
	}
	p.touch(res)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := p.SetField(res, k, values[k]); err != nil {
			return nil, nil, err
		}
	}
	return res, p.s.ValidateResource(res), nil
}

// SetField sets one field of res from its text form: the field is dotted
// segments, the value is read as yaml so lists, numbers and booleans arrive
// typed. The value is checked against the field's declared rule first.
func (p *Project) SetField(res []string, field, text string) error {
	path := strings.Split(field, ".")
	value, err := parseValue(text)
	if err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if err := p.s.ValidateField(res, path, value); err != nil {
		return fmt.Errorf("%s: %w", field, err)
	}
	if err := p.s.Set(res, path, value); err != nil {
		return err
	}
	p.touch(res)
	return nil
}

// CheckField reports whether text is a value field of res accepts, without
// setting it — what a prompt asks between keystrokes.
func (p *Project) CheckField(res []string, field, text string) error {
	value, err := parseValue(text)
	if err != nil {
		return err
	}
	return p.s.ValidateField(res, strings.Split(field, "."), value)
}

// Issues is what res still owes.
func (p *Project) Issues(res []string) []Issue { return p.s.ValidateResource(res) }

func parseValue(text string) (any, error) {
	var v any
	if err := yaml.Unmarshal([]byte(text), &v); err != nil {
		return nil, err
	}
	if v == nil {
		return text, nil
	}
	return v, nil
}

func (p *Project) touch(res []string) { p.touched[strings.Join(res, "/")] = res }

// Save writes every document this session changed back into the directory,
// and nothing else — a document the CLI did not touch is not rewritten.
// It returns the paths written, relative to the directory.
func (p *Project) Save() ([]string, error) {
	var written []string
	keys := make([]string, 0, len(p.touched))
	for k := range p.touched {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		rel, data, err := p.s.Serialize(p.touched[k])
		if err != nil {
			return written, err
		}
		dst := filepath.Join(p.dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return written, err
		}
		written = append(written, rel)
	}
	p.touched = map[string][]string{}
	return written, nil
}

// Report is what validating a project found.
type Report struct {
	// Error is the compile failure, when the project does not compile.
	Error string `json:"error,omitempty"`
	// Findings are the cross-resource audit's: Broken, Check, Note.
	Findings []session.Note `json:"findings"`
	// Deferred are checks the compiler cannot answer itself (a DNS name, an
	// image) and leaves to whoever deploys.
	Deferred []interp.NextValidation `json:"deferred,omitempty"`
}

// Broken reports whether the project cannot deploy as written.
func (r Report) Broken() bool {
	if r.Error != "" {
		return true
	}
	for _, f := range r.Findings {
		if f.Severity == string(schema.Broken) {
			return true
		}
	}
	return false
}

// Validate compiles the project for environment ("" for the bare keys) and
// audits the result.
func (p *Project) Validate(ctx context.Context, environment string) Report {
	return validate(ctx, p.s, environment)
}

// Preflight is Validate as a build judges a project before a cloud does: the
// cut is previewed across the regions the project itself names (schema.Preview,
// which is the vocabulary's own look before any cloud is chosen), so whether a
// cloud serves those regions is left to the cloud, and everything else a cloud
// would refuse is refused here.
func (p *Project) Preflight(ctx context.Context, environment string) Report {
	obj, _, err := p.s.Compile(ctx, schema.CompileOptions{Environment: environment})
	if err != nil {
		return Report{Error: err.Error(), Findings: []session.Note{}}
	}
	regions := schema.WithNoRegions()
	if named := schema.RegionsNamed(obj.Flat()); len(named) > 0 {
		regions = schema.Preview(named...)
	}
	s, err := schema.NewSessionIn(p.s.FS(), "/", regions)
	if err != nil {
		return Report{Error: err.Error(), Findings: []session.Note{}}
	}
	return validate(ctx, s, environment)
}

func validate(ctx context.Context, s *schema.Session, environment string) Report {
	opts := schema.CompileOptions{Environment: environment}
	deferred, err := s.Validate(ctx, opts)
	if err != nil {
		return Report{Error: err.Error(), Findings: []session.Note{}}
	}
	viewed, err := s.Views(ctx, opts)
	r := Report{Findings: viewed.Notes, Deferred: deferred}
	if r.Findings == nil {
		r.Findings = []session.Note{}
	}
	if err != nil && !errors.Is(err, schema.ErrUnviewable) {
		r.Error = err.Error()
	}
	return r
}

// Views cuts the compiled project into the views its destinations hold.
func (p *Project) Views(ctx context.Context, environment string) (session.Viewed, error) {
	return p.s.Views(ctx, schema.CompileOptions{Environment: environment})
}

// Environments are the deployments the project declares.
func (p *Project) Environments() []string { return schema.EnvironmentNames(p.declared()) }

func (p *Project) declared() []schema.Environment {
	envs, err := schema.EnvironmentsOf(p.s.FS(), "/")
	if err != nil {
		return nil
	}
	return envs
}

// Deployments are the environments a branch feeds.
func (p *Project) Deployments(branch string) []string {
	return schema.DeploymentNames(p.declared(), branch)
}

// Compiled is the project compiled for one deployment from branch — the
// options a cloud's deploy compiles with, so the bytes are what it would
// publish.
func (p *Project) Compiled(ctx context.Context, branch, environment string) ([]byte, error) {
	obj, _, err := p.s.Compile(ctx, schema.CompileOptions{Branch: branch, Environment: environment})
	if err != nil {
		return nil, err
	}
	return json.Marshal(obj.Flat())
}
