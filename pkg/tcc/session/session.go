// Package session is a Go-usable editable configuration session over a yaseer
// document tree: read/write/delete fields and resources by path, compile or
// validate the whole config, and fork copy-on-write to validate speculative
// edits before merging them back. It is the same abstraction the browser wasm
// exposes, usable directly from Go (e.g. tau-cli).
//
// The core (edit, fork, merge, save) is DSL-agnostic; compilation is injected via
// CompilerFor, so the Taubyte binding lives in pkg/tcc/taubyte/v1/schema, not here.
package session

import (
	"context"
	"errors"
	"os"
	"path"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// CompilerFor builds a compiler over an afero filesystem for the given compile
// parameters. The schema package supplies the Taubyte binding; the session never
// imports schema, keeping the dependency one-way.
type CompilerFor func(fs afero.Fs, branch, cloud string) (*interp.Compiler, error)

// FieldValidator runs a DSL's declared single-value field validators (enum, string
// shape, cid, fqdn, ...) for partial validation — no compile. Injected by the
// binding, since the session core is DSL-agnostic.
type FieldValidator interface {
	// ValidateField runs one field's validator; nil if the field has none.
	ValidateField(group string, field []string, value any) error
	// Fields returns the authored paths of a resource group's validated fields.
	Fields(group string) [][]string
}

// Completer supplies a DSL's field completion sources: the fixed candidates (enum
// members, shape literals) and, for a reference field, the resource group whose
// in-scope instances are candidates. Injected by the binding.
type Completer interface {
	// Field returns a field's fixed candidates and, if it references a resource
	// group, that group + the prefix to prepend to each referenced name.
	Field(group string, field []string) (values []string, refGroup, refPrefix string)
}

// Bindings wires a Session to a specific DSL: how to compile it (required), how to
// partial-validate its fields, and how to complete field values (both optional).
type Bindings struct {
	CompilerFor    CompilerFor
	FieldValidator FieldValidator
	Completer      Completer
}

// CompileOptions are the per-compile parameters (empty Branch uses the compiler's
// default).
type CompileOptions struct {
	Branch string
	Cloud  string
}

// Session is an editable configuration, resident on a private in-memory
// filesystem. Not safe for concurrent use.
type Session struct {
	fs     afero.Fs
	seer   *yaseer.Seer
	bind   Bindings
	parent *Session // non-nil for a fork (see Fork/Merge)
}

// New stages the config under dir in src into a private in-memory copy and opens
// an editable session over it. bind wires the DSL (see the schema package's
// NewSession).
func New(src afero.Fs, dir string, bind Bindings) (*Session, error) {
	mem := afero.NewMemMapFs()
	if err := copyTree(src, dir, mem, "/"); err != nil {
		return nil, err
	}
	return Adopt(mem, bind)
}

// Adopt opens a session directly over fs (no copy) — for callers that already own
// a private filesystem (e.g. a freshly decompiled config). The session then owns
// fs; don't mutate it behind the session's back.
func Adopt(fs afero.Fs, bind Bindings) (*Session, error) {
	sr, err := yaseer.New(yaseer.VirtualFS(fs, "/"))
	if err != nil {
		return nil, err
	}
	return &Session{fs: fs, seer: sr, bind: bind}, nil
}

// FS exposes the session's working filesystem (read-only intent; for compilers /
// inspection).
func (s *Session) FS() afero.Fs { return s.fs }

func (s *Session) query(res, field []string) *yaseer.Query {
	q := s.seer.Get(res[0])
	for _, seg := range res[1:] {
		q = q.Get(seg)
	}
	q = q.Document()
	for _, seg := range field {
		q = q.Get(seg)
	}
	return q
}

// Get reads a field of a resource; a nil/absent value returns (nil, error).
func (s *Session) Get(res, field []string) (any, error) {
	var v any
	if err := s.query(res, field).Value(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Set writes a field of a resource (raw write — no validation; see Validate).
func (s *Session) Set(res, field []string, value any) error {
	return s.query(res, field).Set(value).Commit()
}

// Delete removes a whole resource (field == nil/empty) or a single field of it.
func (s *Session) Delete(res, field []string) error {
	if len(field) > 0 {
		return s.query(res, field).Delete().Commit()
	}
	q := s.seer.Get(res[0])
	for _, seg := range res[1:] {
		q = q.Get(seg)
	}
	return q.Delete().Commit()
}

// List returns the names under a folder path (resource names, app names, ...).
func (s *Session) List(p []string) ([]string, error) {
	q := s.seer.Get(p[0])
	for _, seg := range p[1:] {
		q = q.Get(seg)
	}
	return q.List()
}

// Compile assembles the whole config; returns the object, deferred checks, and
// any error.
func (s *Session) Compile(ctx context.Context, opts CompileOptions) (interp.Object, []interp.NextValidation, error) {
	c, err := s.compiler(opts)
	if err != nil {
		return nil, nil, err
	}
	return c.Compile(ctx)
}

// Validate re-runs the compiler for diagnostics only: it returns the deferred
// checks and any error, discarding the artifact. Values can't be validated in
// isolation, so this is the honest whole-config check.
func (s *Session) Validate(ctx context.Context, opts CompileOptions) ([]interp.NextValidation, error) {
	c, err := s.compiler(opts)
	if err != nil {
		return nil, err
	}
	return c.Validate(ctx)
}

func (s *Session) compiler(opts CompileOptions) (*interp.Compiler, error) {
	if err := s.seer.Sync(); err != nil {
		return nil, err
	}
	return s.bind.CompilerFor(s.fs, opts.Branch, opts.Cloud)
}

// ValidateField runs the DSL's single-value validator for one field of a resource
// against value (enum, string shape, cid, fqdn, ...) WITHOUT compiling — the cheap
// live-edit path. It does not check cross-element constraints (refs, cross-app
// scope); those need a whole-config Validate. Returns nil when partial validation
// isn't wired or the field has no validator.
func (s *Session) ValidateField(res, field []string, value any) error {
	if s.bind.FieldValidator == nil {
		return nil
	}
	return s.bind.FieldValidator.ValidateField(resGroup(res), field, value)
}

// ValidateResource runs every single-value validator of one resource against its
// current values, returning all failures (empty slice = locally valid). Scoped to
// the one file and compile-free; cross-element refs still need a whole-config
// Validate.
func (s *Session) ValidateResource(res []string) []error {
	if s.bind.FieldValidator == nil {
		return nil
	}
	group := resGroup(res)
	var errs []error
	for _, f := range s.bind.FieldValidator.Fields(group) {
		v, err := s.Get(res, f)
		if err != nil {
			continue // field absent -> nothing to validate
		}
		if e := s.bind.FieldValidator.ValidateField(group, f, v); e != nil {
			errs = append(errs, e)
		}
	}
	return errs
}

// resGroup is the resource-kind name in a resource path: res[len-2] — the folder
// above the instance name, whether or not the path is application-scoped.
func resGroup(res []string) string {
	if len(res) < 2 {
		return ""
	}
	return res[len(res)-2]
}

// Complete returns completion candidates for a field's value, filtered by the
// partial string the user has typed (case-insensitive prefix; "" = all). Fixed
// candidates come from the DSL (enum members, shape literals); reference fields
// also offer the target group's instances IN SCOPE (the resource's own app plus
// root/global), each prefixed. Returns nil if completion isn't wired.
func (s *Session) Complete(res, field []string, partial string) []string {
	if s.bind.Completer == nil {
		return nil
	}
	values, refGroup, refPrefix := s.bind.Completer.Field(resGroup(res), field)
	cands := append([]string(nil), values...)
	if refGroup != "" {
		for _, name := range s.scopedNames(res, refGroup) {
			cands = append(cands, refPrefix+name)
		}
	}
	return filterPrefix(cands, partial)
}

// scopedNames lists the instances of refGroup visible from res: its own
// application scope (if application-scoped) then root/global, deduped.
func (s *Session) scopedNames(res []string, refGroup string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path []string) {
		names, err := s.List(path)
		if err != nil {
			return
		}
		for _, n := range names {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	if len(res) >= 4 { // [container, app, group, name] -> the app's own scope
		add([]string{res[0], res[1], refGroup})
	}
	add([]string{refGroup}) // root/global
	return out
}

func filterPrefix(cands []string, partial string) []string {
	if partial == "" {
		return cands
	}
	p := strings.ToLower(partial)
	out := make([]string, 0, len(cands))
	for _, c := range cands {
		if strings.HasPrefix(strings.ToLower(c), p) {
			out = append(out, c)
		}
	}
	return out
}

// Save flushes the session and writes its config out under dir in dst.
func (s *Session) Save(dst afero.Fs, dir string) error {
	if err := s.seer.Sync(); err != nil {
		return err
	}
	return copyTree(s.fs, "/", dst, dir)
}

// Fork opens a copy-on-write child over this session: edits land in an overlay,
// leaving the parent untouched until Merge. Validate the child, then Merge to
// adopt its changes (or discard it). The parent must not be edited until then.
func (s *Session) Fork() (*Session, error) {
	if err := s.seer.Sync(); err != nil {
		return nil, err
	}
	// The fork edits a CoW over the parent (its own validate working-fs), and
	// records every commit in an in-memory op-log (the WAL) — that log, not the
	// files, is what Merge replays onto the parent.
	cow := NewCoW(s.fs)
	sr, err := yaseer.New(yaseer.VirtualFS(cow, "/"), yaseer.WithInMemWAL())
	if err != nil {
		return nil, err
	}
	return &Session{fs: cow, seer: sr, bind: s.bind, parent: s}, nil
}

// Merge replays the fork's in-memory op-log onto the parent seer — no file
// copying, the parent stays live and consistent — then flushes the parent to its
// filesystem. After Merge the fork is spent.
func (s *Session) Merge() error {
	if s.parent == nil {
		return errors.New("session: not a fork")
	}
	if err := s.seer.Sync(); err != nil { // flush any pending fork edits into the WAL
		return err
	}
	if err := s.parent.seer.ReplayWal(s.seer.WAL()); err != nil {
		return err
	}
	return s.parent.seer.Sync()
}

// Close releases the session. The in-memory filesystem is then garbage-collected.
func (s *Session) Close() { s.seer = nil }

// copyTree copies the subtree at srcDir in src into dst rooted at dstDir.
func copyTree(src afero.Fs, srcDir string, dst afero.Fs, dstDir string) error {
	if srcDir == "" {
		srcDir = "/"
	}
	return afero.Walk(src, srcDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, srcDir)
		target := path.Join(dstDir, rel)
		if info.IsDir() {
			return dst.MkdirAll(target, 0o755)
		}
		data, err := afero.ReadFile(src, p)
		if err != nil {
			return err
		}
		if err := dst.MkdirAll(path.Dir(target), 0o755); err != nil {
			return err
		}
		return afero.WriteFile(dst, target, data, 0o644)
	})
}
