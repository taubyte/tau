// Package session is an editable configuration session over a yaseer document
// tree: fields and resources by path, compile or validate, and copy-on-write
// forks merged back. The browser wasm exposes the same abstraction. The core is
// DSL-agnostic; compilation is injected via CompilerFor.
package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/afero"
	"github.com/taubyte/tau/pkg/tcc/interp"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// CompilerFor builds a compiler over an afero filesystem for the given compile
// parameters. The schema package supplies the Taubyte binding; the session never
// imports schema, keeping the dependency one-way.
type CompilerFor func(fs afero.Fs, opts CompileOptions) (*interp.Compiler, error)

// FieldValidator runs a DSL's declared single-value field validators (enum, string
// shape, cid, fqdn, ...) for partial validation — no compile. Injected by the
// binding, since the session core is DSL-agnostic.
type FieldValidator interface {
	// ValidateField runs one field's validator; nil if the field has none.
	ValidateField(group string, field []string, value any) error
	// Fields returns the authored paths of a resource group's validated fields.
	Fields(group string) [][]string
	// RequiredFields returns a resource group's required fields — asked for
	// separately because a missing field has no value for the value-driven
	// checks to run against.
	RequiredFields(group string) []RequiredField
}

// FieldRef is where a field may be authored.
type FieldRef struct {
	Path []string
	// AnyOf is every location the field may be authored at when the DSL gives more
	// than one (object/size or streaming/size); AnyOf[0] == Path.
	AnyOf [][]string
}

// RequiredField is a field whose absence is an error. When is nil when always
// required; otherwise it names the sibling to read, and WhenIn the values that
// require it.
type RequiredField struct {
	FieldRef
	When   *FieldRef
	WhenIn []string

	// WhenBranch: the discriminator's value is the key written under WhenBranchAt
	// (`build: {commands: {...}}`); When is nil then. WhenBranchAt may be empty, the
	// branch sitting at the resource's root.
	WhenBranch   bool
	WhenBranchAt []string

	// In is where the field's entries are, when it is a field of each entry of a group
	// or each element of a list; Path is then relative to one entry. List says the
	// entries are positions rather than names.
	In   []string
	List bool

	// Claimed is every key under In that is the holder's own field rather than an
	// entry.
	Claimed []string
}

// Completer supplies a DSL's field completion sources: the fixed candidates (enum
// members) and, for a reference field, the resource group whose
// in-scope instances are candidates. Injected by the binding.
type Completer interface {
	// Field returns a field's fixed candidates and, if it references a resource
	// group, that group. found is false when the field is unknown (no such
	// attribute path).
	Field(group string, field []string) (values []string, refGroup string, found bool)
}

// Layout maps a resource address to its document. nil means every address is a
// leaf document.
type Layout interface {
	// ContainerDoc returns the document name (e.g. "config") a container group's
	// instances keep their own fields in; "" if group is not a container.
	ContainerDoc(group string) string
	// RootDoc is the project root document name (e.g. "config").
	RootDoc() string
	// Kinds lists the DSL's resource kinds — its top-level groups.
	Kinds() []Kind
}

// IdentifiedLayout is a Layout that also says what a resource's id and name are called.
// Optional, because a DSL of plain documents has neither; a session over one simply
// cannot look a resource up by name.
type IdentifiedLayout interface {
	Layout
	Identity() (id, name string)
}

// Kind is one kind the DSL names: Group is its directory key, Name the declared
// singular, accepted as an alias. Container kinds hold resources of their own
// and are not compiled resources; editing is uniform, so branch on Container,
// never on a kind's name.
type Kind struct {
	Name      string `json:"name"`
	Group     string `json:"group"`
	Container bool   `json:"container"`
}

// Generator mints values for the DSL's generated fields (a resource id), so
// every consumer stops carrying its own "how do I make an id".
type Generator interface {
	// GeneratedBy returns the generator id declared for a field, or "" if the
	// field is authored rather than generated.
	GeneratedBy(group string, field []string) string
	// Generate mints a value for that generator, mixing in seed for uniqueness.
	Generate(kind string, seed ...any) (string, error)
}

// Bindings wires a Session to a specific DSL: how to compile it (required), how
// to partial-validate its fields, how to complete field values, how its
// documents are laid out, and how its generated fields are minted (all optional).
type Bindings struct {
	CompilerFor    CompilerFor
	FieldValidator FieldValidator
	Completer      Completer
	Layout         Layout
	Generator      Generator
	// Viewer cuts a compiled configuration into the views its destinations hold. Unset,
	// a session has one view, the whole configuration.
	Viewer Viewer
}

// CompileOptions are the per-compile parameters (empty Branch uses the compiler's
// default).
type CompileOptions struct {
	Branch string
	Cloud  string
	// Environment is the deployment compiled for, when the DSL compiles per
	// deployment; empty compiles the bare keys.
	Environment string
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

// docPath maps a resource address to the segments of its document, rejecting
// malformed addresses (which once created files beside the real directory). A
// container instance's address is its group form; its fields' document is
// layout. The leaf-suffixed form is a legacy alias.
func (s *Session) docPath(res []string) ([]string, error) {
	if len(res) == 0 {
		return nil, errors.New("session: empty resource address")
	}
	l := s.bind.Layout
	if l == nil {
		return res, nil // no layout wired: every address is a leaf document
	}
	bad := func(why string) error {
		return fmt.Errorf("session: %q is not a resource address (%s)", strings.Join(res, "/"), why)
	}

	// Scopes nest, so addresses do too.
	rest := res
	for len(rest) >= 2 && l.ContainerDoc(rest[0]) != "" {
		rest = rest[2:]
	}

	switch len(rest) {
	case 0:
		// the address ended on a scope: its own fields live in the document the layout
		// names, which is layout rather than something a caller should have to spell
		return append(append([]string{}, res...), l.ContainerDoc(res[len(res)-2])), nil
	case 1:
		// [.., container, instance, containerDoc] — the legacy explicit form, and the
		// root document, which is the one address with no group above it
		if len(res) == len(rest) {
			if rest[0] != l.RootDoc() {
				return nil, bad("a resource needs a group and a name")
			}
			return res, nil
		}
		if doc := l.ContainerDoc(res[len(res)-3]); doc == "" || rest[0] != doc {
			return nil, bad("only a container instance's document is addressable three deep")
		}
	case 2:
		// [.., group, name] — a resource, at whatever depth its scopes put it
		if l.ContainerDoc(rest[0]) != "" {
			return nil, bad(strconv.Quote(rest[0]) + " is a scope, and a scope holds resources rather than being one at this depth")
		}
	default:
		return nil, bad("a resource is a kind and a name, inside as many scopes as it lives in")
	}
	return res, nil
}

func (s *Session) query(res, field []string) (*yaseer.Query, error) {
	doc, err := s.docPath(res)
	if err != nil {
		return nil, err
	}
	q := s.seer.Get(doc[0])
	for _, seg := range doc[1:] {
		q = q.Get(seg)
	}
	q = q.Document()
	for _, seg := range field {
		q = q.Get(seg)
	}
	return q, nil
}

// Get reads a field of a resource; a nil/absent value returns (nil, error).
func (s *Session) Get(res, field []string) (any, error) {
	q, err := s.query(res, field)
	if err != nil {
		return nil, err
	}
	var v any
	if err := q.Value(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Set writes a field of a resource (raw write — no validation; see Validate).
func (s *Session) Set(res, field []string, value any) error {
	q, err := s.query(res, field)
	if err != nil {
		return err
	}
	return q.Set(value).Commit()
}

// Delete removes a whole resource (field == nil/empty) or a single field of it.
// Deleting a container instance removes its directory, and with it whatever
// resources it still held.
func (s *Session) Delete(res, field []string) error {
	if len(field) > 0 {
		q, err := s.query(res, field)
		if err != nil {
			return err
		}
		return q.Delete().Commit()
	}
	if _, err := s.docPath(res); err != nil {
		return err
	}
	// Deletion addresses the resource ITSELF, not its document: a leaf resource
	// is its file, a container instance is its whole directory.
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

// Compile is the configuration as it runs, lowerings applied, so nobody can
// forget to lower.
func (s *Session) Compile(ctx context.Context, opts CompileOptions) (interp.Object, []interp.NextValidation, error) {
	c, err := s.compiler(opts)
	if err != nil {
		return nil, nil, err
	}
	return c.Compile(ctx)
}

// Authored is the configuration as it was WRITTEN — no lowerings applied. It is what an
// editor shows, and what decompile round-trips: a generated resource must never be
// written back to somebody's repository.
func (s *Session) Authored(ctx context.Context, opts CompileOptions) (interp.Object, []interp.NextValidation, error) {
	c, err := s.compiler(opts)
	if err != nil {
		return nil, nil, err
	}
	return c.Authored(ctx)
}

// Validate compiles for diagnostics only, validating what runs: a lowering that
// produces a rejected document cannot be deployed.
func (s *Session) Validate(ctx context.Context, opts CompileOptions) ([]interp.NextValidation, error) {
	_, validations, err := s.Compile(ctx, opts)
	return validations, err
}

func (s *Session) compiler(opts CompileOptions) (*interp.Compiler, error) {
	if err := s.seer.Sync(); err != nil {
		return nil, err
	}
	return s.bind.CompilerFor(s.fs, opts)
}

// ValidateField checks one field against value without compiling: the DSL's
// single-value validator and, for a reference, that the name exists in scope
// (the resource's own app, then root). nil when partial validation is not wired
// or the field has no constraint.
func (s *Session) ValidateField(res, field []string, value any) error {
	group := s.resGroup(res)
	if s.bind.FieldValidator != nil {
		if err := s.bind.FieldValidator.ValidateField(group, field, value); err != nil {
			return err
		}
	}
	if s.bind.Completer != nil {
		if _, refGroup, _ := s.bind.Completer.Field(group, field); refGroup != "" {
			if err := s.checkRef(res, refGroup, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkRef verifies that every referenced name in value names a refGroup resource
// visible from res (its app + root).
func (s *Session) checkRef(res []string, refGroup string, value any) error {
	inScope := map[string]bool{}
	for _, n := range s.scopedNames(res, refGroup) {
		inScope[n] = true
	}
	for _, name := range asStrings(value) {
		if name == "" {
			continue
		}
		if !inScope[name] {
			return fmt.Errorf("no %s named %q in scope", refGroup, name)
		}
	}
	return nil
}

func asStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// FieldIssue is one failed check, attributed to the field that failed it. Field
// is the authored path ("trigger/domains" as ["trigger","domains"]); empty means
// the issue is about the resource as a whole.
type FieldIssue struct {
	Field   []string `json:"field"`
	Message string   `json:"message"`
}

// ValidateResource checks every constrained field of one resource, compile-free,
// returning each failure attributed to its declared field; deferred external
// checks stay in Validate.
func (s *Session) ValidateResource(res []string) []FieldIssue {
	if s.bind.FieldValidator == nil {
		return nil
	}
	group := s.resGroup(res)
	var issues []FieldIssue
	// A required field that is absent has no value to check, so it is looked for by
	// name.
	for _, required := range s.bind.FieldValidator.RequiredFields(group) {
		for _, rf := range s.atEachEntry(res, required) {
			if !s.requiredNow(res, rf) || s.hasValue(res, rf.FieldRef) {
				continue
			}
			at := s.authoredAt(res, rf.FieldRef)
			msg := fmt.Sprintf("required field %q is missing", strings.Join(at, "/"))
			switch {
			case rf.When != nil:
				msg = fmt.Sprintf("%s is required when %s is %s", strings.Join(at, "/"),
					strings.Join(rf.When.Path, "/"), strings.Join(rf.WhenIn, " or "))
			}
			issues = append(issues, FieldIssue{Field: at, Message: msg})
		}
	}
	for _, f := range s.bind.FieldValidator.Fields(group) {
		v, err := s.Get(res, f)
		if err != nil {
			continue // absent -> already reported above if required
		}
		if e := s.ValidateField(res, f, v); e != nil {
			issues = append(issues, FieldIssue{Field: f, Message: e.Error()})
		}
	}
	return issues
}

// atEachEntry is a required field once for the resource, or, for a field of a group's
// entries or a list's elements, once per entry the document has, addressed from the
// resource. An entry that is not there owes nothing.
func (s *Session) atEachEntry(res []string, rf RequiredField) []RequiredField {
	if len(rf.In) == 0 {
		return []RequiredField{rf}
	}
	v, err := s.Get(res, rf.In)
	if err != nil {
		return nil
	}
	var entries []string
	switch held := v.(type) {
	case []any:
		for i := range held {
			entries = append(entries, strconv.Itoa(i))
		}
	case map[string]any:
		for name := range held {
			if !slices.Contains(rf.Claimed, name) {
				entries = append(entries, name)
			}
		}
		sort.Strings(entries)
	}
	out := make([]RequiredField, 0, len(entries))
	for _, entry := range entries {
		at := append(append([]string{}, rf.In...), entry)
		under := func(p []string) []string { return append(append([]string{}, at...), p...) }
		one := rf
		one.In = nil
		one.Path = under(rf.Path)
		one.AnyOf = nil
		for _, p := range rf.AnyOf {
			one.AnyOf = append(one.AnyOf, under(p))
		}
		if rf.When != nil {
			one.When = &FieldRef{Path: under(rf.When.Path)}
		}
		if rf.WhenBranch {
			one.WhenBranchAt = under(rf.WhenBranchAt)
		}
		out = append(out, one)
	}
	return out
}

// hasValue reports whether a field is authored at any of its locations.
func (s *Session) hasValue(res []string, f FieldRef) bool {
	// Candidates: every location the DSL allows. Built
	// with an explicit loop rather than appending to AnyOf — that slice comes
	// from the binding and appending to it can write into its backing array.
	for _, p := range f.locations() {
		if len(p) == 0 {
			continue
		}
		// Present but empty is not present: an empty domains list routes
		// nothing. Matches what the compiler rejects at load.
		if v, err := s.Get(res, p); err == nil && !isBlank(v) {
			return true
		}
	}
	return false
}

// locations is every place a field may be authored: its candidate paths (one,
// unless the DSL branches).
func (f FieldRef) locations() [][]string {
	if len(f.AnyOf) > 0 {
		return f.AnyOf
	}
	return [][]string{f.Path}
}

// authoredAt is where to report a missing field. With one location that is the
// path; with several it is the branch the author actually opened — a storage
// that says `streaming:` is missing streaming/size, not object/size — so an
// editor marks a field the document actually has a place for.
func (s *Session) authoredAt(res []string, f FieldRef) []string {
	if len(f.AnyOf) == 0 {
		return f.Path
	}
	for _, p := range f.AnyOf {
		if len(p) < 2 {
			continue
		}
		if _, err := s.Get(res, p[:len(p)-1]); err == nil {
			return p
		}
	}
	return f.Path
}

// isBlank reports a value that carries no information. Only strings and lists
// can be blank — a false bool or a zero int are legitimate values.
func isBlank(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []string:
		return len(t) == 0
	case []any:
		return len(t) == 0
	}
	return false
}

// requiredNow evaluates a conditional requirement against the resource's current
// values. An absent discriminator means the condition cannot hold — a resource
// with no trigger type owes no trigger-specific field, and saying otherwise
// would flood a half-created resource with issues it cannot act on yet.
func (s *Session) requiredNow(res []string, rf RequiredField) bool {
	// A captured branch is read as a key, not a value: the author selects it by
	// writing it.
	if rf.WhenBranch {
		return s.branchIn(res, rf.WhenBranchAt, rf.WhenIn)
	}
	if rf.When == nil {
		return true
	}
	v, err := s.Get(res, rf.When.Path)
	if err != nil {
		return false
	}
	got, ok := v.(string)
	return ok && slices.Contains(rf.WhenIn, got)
}

// branchIn reports whether the resource selected one of `in` at this path, by
// writing it as a key.
func (s *Session) branchIn(res []string, at, in []string) bool {
	v, err := s.Get(res, at)
	if err != nil {
		return false
	}
	block, ok := v.(map[string]any)
	if !ok {
		return false
	}

	for _, want := range in {
		if _, written := block[want]; written {
			return true
		}
	}
	return false
}

// Location reports where a resource's document lives, the path Serialize returns,
// without flushing or reading it.
func (s *Session) Location(res []string) (string, error) {
	q, err := s.query(res, nil)
	if err != nil {
		return "", err
	}
	// Resolve as a read so yaseer populates the document's path. An empty
	// document resolves to no value but still has one, so the read error is
	// not fatal — an absent path is the honest "no such document".
	var v any
	q.Value(&v)
	fp := q.FilePath()
	if fp == "" {
		return "", fmt.Errorf("session: no document for %q", strings.Join(res, "/"))
	}
	return strings.TrimPrefix(fp, "/"), nil
}

// Serialize flushes pending edits and returns one resource's document: its
// repo-relative path and its exact YAML, comments preserved.
func (s *Session) Serialize(res []string) (path string, data []byte, err error) {
	if err = s.seer.Sync(); err != nil {
		return "", nil, err
	}
	if path, err = s.Location(res); err != nil {
		return "", nil, err
	}
	if data, err = afero.ReadFile(s.fs, "/"+path); err != nil {
		return "", nil, err
	}
	return path, data, nil
}

// SetResource makes res's document equal doc with the minimal field writes and
// deletes, so untouched YAML and comments survive; explicit nil writes a null,
// absence deletes. Applied through a fork so a failure leaves nothing half
// written.
func (s *Session) SetResource(res []string, doc map[string]any) error {
	if _, err := s.docPath(res); err != nil {
		return err
	}
	prev, _ := s.Get(res, nil) // absent resource -> no previous fields, all sets
	prevDoc, _ := prev.(map[string]any)
	ops := diffOps(prevDoc, doc, nil)
	if len(ops) == 0 {
		return nil
	}
	fork, err := s.Fork()
	if err != nil {
		return err
	}
	for _, o := range ops {
		if o.del {
			err = fork.Delete(res, o.path)
		} else {
			err = fork.Set(res, o.path, o.value)
		}
		if err != nil {
			fork.Close()
			return err
		}
	}
	return fork.Merge()
}

type fieldOp struct {
	path  []string
	value any
	del   bool
}

// diffOps is the minimal set/delete ops turning prev into next. Maps recurse;
// arrays and scalars are leaves; keys gone from next become deletes.
func diffOps(prev, next map[string]any, base []string) []fieldOp {
	var ops []fieldOp
	at := func(k string) []string { return append(append([]string{}, base...), k) }
	for k, nv := range next {
		if nm, ok := nv.(map[string]any); ok {
			pm, _ := prev[k].(map[string]any)
			ops = append(ops, diffOps(pm, nm, at(k))...)
			continue
		}
		if pv, had := prev[k]; !had || !sameValue(pv, nv) {
			ops = append(ops, fieldOp{path: at(k), value: nv})
		}
	}
	for k := range prev {
		if _, ok := next[k]; !ok {
			ops = append(ops, fieldOp{path: at(k), del: true})
		}
	}
	return ops
}

func sameValue(a, b any) bool {
	as, aok := asStrings(a), isList(a)
	bs, bok := asStrings(b), isList(b)
	if aok || bok {
		if aok != bok || len(as) != len(bs) {
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

func isList(v any) bool {
	switch v.(type) {
	case []string, []any:
		return true
	}
	return false
}

// Kinds lists the resource kinds this DSL defines. A consumer that routes on a
// kind STRING — a UI rendering whatever its route names — reads the vocabulary
// here instead of keeping its own kind table beside the schema.
func (s *Session) Kinds() []Kind {
	if s.bind.Layout == nil {
		return nil
	}
	return s.bind.Layout.Kinds()
}

// kind resolves a kind name to its Kind: by Group (canonical) or by the declared
// singular, case-insensitively. Unknown names error listing what is valid, so a
// typo'd route never silently addresses a directory the DSL never defined.
func (s *Session) kind(name string) (Kind, error) {
	kinds := s.Kinds()
	for _, k := range kinds {
		if k.Group == name {
			return k, nil
		}
	}
	lower := strings.ToLower(name)
	for _, k := range kinds {
		if k.Name != "" && k.Name == lower {
			return k, nil
		}
	}
	valid := make([]string, 0, len(kinds))
	for _, k := range kinds {
		valid = append(valid, k.Group)
	}
	if len(valid) == 0 {
		return Kind{}, errors.New("session: no layout wired, so no kinds are known")
	}
	return Kind{}, fmt.Errorf("session: unknown kind %q (have %s)", name, strings.Join(valid, ", "))
}

// container is the kind whose instances hold resources of their own — the scope
// an application-scoped address is nested under.
func (s *Session) container() (Kind, bool) {
	for _, k := range s.Kinds() {
		if k.Container {
			return k, true
		}
	}
	return Kind{}, false
}

// Root is the address of the config's own root document — the one resource with
// no kind above it. A caller that wants to edit it should ask for it rather than
// spell the document name, which is the DSL's to choose.
func (s *Session) Root() []string {
	if s.bind.Layout == nil {
		return nil
	}
	return []string{s.bind.Layout.RootDoc()}
}

// Address is the canonical address of one resource by kind. A container kind is
// addressed like any other; passing app for one is an error, since containers do
// not nest. An empty app means project scope.
func (s *Session) Address(kind, name, app string) ([]string, error) {
	k, err := s.kind(kind)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("session: a %s needs a name", k.Group)
	}
	if k.Container {
		if app != "" {
			return nil, fmt.Errorf("session: %s is a container kind and cannot be scoped to %q", k.Group, app)
		}
		return []string{k.Group, name}, nil
	}
	if app == "" {
		return []string{k.Group, name}, nil
	}
	c, ok := s.container()
	if !ok {
		return nil, fmt.Errorf("session: this DSL has no container kind to scope %q under", app)
	}
	// Scopes NEST, so the scope is a path: "shop/local" is two of them, and addressing it as
	// one produced a scope literally called "shop/local" that exists nowhere.
	out := []string{}
	for _, level := range strings.Split(app, "/") {
		if level == "" {
			return nil, fmt.Errorf("session: %q is not a scope path", app)
		}
		out = append(out, c.Group, level)
	}
	return append(out, k.Group, name), nil
}

// Names lists a kind's instances in scope: the project when app is empty, one
// application's own otherwise. Scoping a container kind is an error, as in
// Address.
func (s *Session) Names(kind, app string) ([]string, error) {
	k, err := s.kind(kind)
	if err != nil {
		return nil, err
	}
	dir := []string{k.Group}
	if app != "" {
		if k.Container {
			return nil, fmt.Errorf("session: %s is a container kind and cannot be scoped to %q", k.Group, app)
		}
		c, ok := s.container()
		if !ok {
			return nil, fmt.Errorf("session: this DSL has no container kind to scope %q under", app)
		}
		dir = []string{c.Group, app, k.Group}
	}
	names, err := s.List(dir)
	if err != nil {
		return []string{}, nil
	}
	return names, nil
}

// Exists reports whether a resource is already in the config — what tells an
// editor "this is a new resource" without consulting a file listing. It asks the
// resource's own group, so a container instance counts as existing as soon as
// its directory does, even before it has a document.
func (s *Session) Exists(res []string) bool {
	if _, err := s.docPath(res); err != nil {
		return false
	}
	if len(res) == 1 { // the project root document
		_, err := s.Get(res, nil)
		return err == nil
	}
	names, err := s.List(res[:len(res)-1])
	if err != nil {
		return false
	}
	return slices.Contains(names, res[len(res)-1])
}

// ResourceAt maps a repo-relative YAML path to its canonical resource address;
// ok is false for anything not a resource document under the layout:
//
//	config.yaml                     -> [rootDoc]
//	functions/x.yaml                -> [functions, x]
//	applications/a/config.yaml      -> [applications, a]
//	applications/a/functions/x.yaml -> itself
func (s *Session) ResourceAt(filePath string) ([]string, bool) {
	l := s.bind.Layout
	if l == nil {
		return nil, false
	}
	// ".yaml" only — that is the sole extension yaseer reads or writes, so any
	// other spelling names a file this session can never round-trip.
	stem, ok := strings.CutSuffix(strings.TrimPrefix(filePath, "/"), ".yaml")
	if !ok {
		return nil, false
	}
	segs := strings.Split(stem, "/")
	if slices.Contains(segs, "") {
		return nil, false
	}
	if len(segs) == 1 {
		return segs, segs[0] == l.RootDoc()
	}
	if !s.hasGroup(segs[0]) {
		return nil, false
	}
	// Peel the scopes off the front, however many there are: a file two namespaces deep
	// is still a resource, and reading only four segments called it nothing.
	rest := segs
	for len(rest) >= 2 && l.ContainerDoc(rest[0]) != "" {
		rest = rest[2:]
	}
	switch len(rest) {
	case 0: // <container>/<instance>.yaml: a scope's document lives inside its directory, never beside it
		return nil, false
	case 1: // .../<container>/<instance>/<containerDoc> addresses the scope itself
		if len(segs) == len(rest) {
			return nil, false
		}
		return segs[:len(segs)-1], rest[0] == l.ContainerDoc(segs[len(segs)-3])
	case 2:
		return segs, s.hasGroup(rest[0]) && l.ContainerDoc(rest[0]) == ""
	}
	return nil, false
}

// hasGroup reports whether name is a group the DSL declares.
func (s *Session) hasGroup(name string) bool {
	for _, k := range s.Kinds() {
		if k.Group == name {
			return true
		}
	}
	return false
}

// Generate mints a value for one of a resource's generated fields, mixing in the
// address and any seed; the generator supplies its own entropy, so uniqueness
// never depends on the seed.
func (s *Session) Generate(res, field []string, seed ...any) (string, error) {
	if s.bind.Generator == nil {
		return "", errors.New("session: no generator wired")
	}
	if _, err := s.docPath(res); err != nil {
		return "", err
	}
	kind := s.bind.Generator.GeneratedBy(s.resGroup(res), field)
	if kind == "" {
		return "", fmt.Errorf("session: %q is not a generated field", strings.Join(field, "/"))
	}
	return s.bind.Generator.Generate(kind, append(seed, strings.Join(res, "/"))...)
}

// resGroup is the resource-kind name in a resource address: res[len-2] — the
// folder above the instance name, whether or not the address is
// application-scoped. A container instance's legacy config-suffixed address
// names the CONTAINER, not the instance ([applications, x, config] ->
// "applications"), so partial validation of an application works either way.
func (s *Session) resGroup(res []string) string {
	if l := s.bind.Layout; l != nil && len(res) == 3 && res[2] == l.ContainerDoc(res[0]) {
		return res[0]
	}
	if len(res) < 2 {
		return ""
	}
	return res[len(res)-2]
}

// Complete returns completion candidates for a field's value matching partial
// (case-insensitive prefix): enum members, and for a reference the target
// group's instances in scope. An unknown field is an error; (nil, nil) when
// completion is not wired.
func (s *Session) Complete(res, field []string, partial string) ([]string, error) {
	if s.bind.Completer == nil {
		return nil, nil
	}
	group := s.resGroup(res)
	values, refGroup, found := s.bind.Completer.Field(group, field)
	if !found {
		return nil, fmt.Errorf("unknown field %q on %q", strings.Join(field, "/"), group)
	}
	cands := append([]string{}, values...)
	if refGroup != "" {
		for _, name := range s.scopedNames(res, refGroup) {
			cands = append(cands, name)
		}
	}
	return filterPrefix(cands, partial), nil
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
