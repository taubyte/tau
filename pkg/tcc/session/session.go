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
	"fmt"
	"os"
	"path"
	"slices"
	"strconv"
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
	// RequiredFields returns a resource group's required fields — asked for
	// separately because a missing field has no value for the value-driven
	// checks to run against.
	RequiredFields(group string) []RequiredField
}

// FieldRef is where a field may be authored: its canonical path plus the legacy
// alias that also satisfies it.
type FieldRef struct {
	Path   []string
	Compat []string
	// AnyOf is every location the field may be authored at, when the DSL gives
	// it more than one — a storage's size lives at object/size or
	// streaming/size depending on which block the author wrote. AnyOf[0] ==
	// Path; nil when there is only one location.
	AnyOf [][]string
}

// RequiredField is a field whose absence is an error, and the condition under
// which that applies. When is nil when the field is always required; otherwise
// it names the sibling to read, and WhenIn the values that make it required —
// a function's pubsub channel is required for a pubsub trigger and meaningless
// for an http one.
type RequiredField struct {
	FieldRef
	When   *FieldRef
	WhenIn []string
	// Unless inverts both the sense and the default: required UNLESS the
	// discriminator (a bool) is true, and an ABSENT discriminator means
	// required, because an unset bool is false.
	Unless bool
}

// Completer supplies a DSL's field completion sources: the fixed candidates (enum
// members, shape literals) and, for a reference field, the resource group whose
// in-scope instances are candidates. Injected by the binding.
type Completer interface {
	// Field returns a field's fixed candidates and, if it references a resource
	// group, that group + the prefix to prepend to each referenced name. found is
	// false when the field is unknown (no such attribute path).
	Field(group string, field []string) (values []string, refGroup, refPrefix string, found bool)
}

// Layout describes the DSL's document layout so the session can map a resource
// address to the document that holds it. Injected by the binding: "a container's
// instances are directories with a config document inside" is DSL knowledge, and
// the session core stays layout-agnostic without it (nil Layout = every resource
// address is a leaf document, the pre-Layout behaviour).
type Layout interface {
	// ContainerDoc returns the document name (e.g. "config") a container group's
	// instances keep their own fields in; "" if group is not a container.
	ContainerDoc(group string) string
	// RootDoc is the project root document name (e.g. "config").
	RootDoc() string
	// Kinds lists the DSL's resource kinds — its top-level groups.
	Kinds() []Kind
}

// Kind is one kind of thing the DSL names. Group is the canonical key (the
// config directory it is authored under); Name is the singular the DSL declares
// for one instance, lowercased, and is accepted as an alias wherever a kind is
// named.
//
// Container is NOT cosmetic, and a consumer should branch on it rather than
// treat every kind alike. A container's instances hold resources of their own;
// in this DSL that is the application, and it is deliberately not declared as a
// resource — it has no compiled resource type and never appears in the compiled
// object as one. What IS uniform is everything about editing it: its address,
// its document, its file, its local validation. So:
//
//	address / doc / serialize / validate  — same for every kind, container or not
//	"which kinds can I create as resources" — filter out Container
//
// Branching on Container is right; branching on a kind's NAME is what this
// exists to remove.
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

// docPath canonicalizes a resource address to the segments of the document that
// holds it, and rejects structurally bogus addresses. THE canonical address of a
// container instance is its DSL-group form ([applications, x]) — the container
// IS the resource; that its fields live in applications/x/config.yaml is layout,
// mapped here. The config-suffixed form is accepted as a legacy alias.
//
// Rejecting the malformed shapes here is what closes the silent-corruption hole:
// Set(["applications"], ["a","id"], v) used to create /applications.yaml, a
// sibling of the real directory, with no error.
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
	switch len(res) {
	case 1:
		if res[0] != l.RootDoc() {
			return nil, bad("a resource needs a group and a name")
		}
	case 2:
		if doc := l.ContainerDoc(res[0]); doc != "" {
			return append(append([]string{}, res...), doc), nil
		}
	case 3: // legacy [container, name, containerDoc]
		if doc := l.ContainerDoc(res[0]); doc == "" || res[2] != doc {
			return nil, bad("only a container instance's document is addressable three deep")
		}
	case 4: // [container, instance, group, name]
		if l.ContainerDoc(res[0]) == "" {
			return nil, bad(strconv.Quote(res[0]) + " is not a container")
		}
	default:
		return nil, bad("too deep")
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

// ValidateField checks one field of a resource against value WITHOUT compiling —
// the cheap live-edit path. It runs the DSL's single-value validator (enum, string
// shape, cid, fqdn, ...) AND, for a reference field, that the value names a
// resource that actually exists IN SCOPE (the resource's own app + root/global —
// the same scope the compiler resolves against, so siblings don't count). Returns
// nil when partial validation isn't wired or the field carries no constraint.
func (s *Session) ValidateField(res, field []string, value any) error {
	group := s.resGroup(res)
	if s.bind.FieldValidator != nil {
		if err := s.bind.FieldValidator.ValidateField(group, field, value); err != nil {
			return err
		}
	}
	if s.bind.Completer != nil {
		if _, refGroup, refPrefix, _ := s.bind.Completer.Field(group, field); refGroup != "" {
			if err := s.checkRef(res, refGroup, refPrefix, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkRef verifies that every referenced name in value names a refGroup resource
// visible from res (its app + root). Values that don't carry the ref prefix are
// literals (e.g. a source of ".") and are left to the shape validator.
func (s *Session) checkRef(res []string, refGroup, refPrefix string, value any) error {
	inScope := map[string]bool{}
	for _, n := range s.scopedNames(res, refGroup) {
		inScope[n] = true
	}
	for _, v := range asStrings(value) {
		name := v
		if refPrefix != "" {
			if !strings.HasPrefix(v, refPrefix) {
				continue // a literal, not a reference
			}
			name = strings.TrimPrefix(v, refPrefix)
		}
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

// ValidateResource checks every constrained field of one resource against its
// current values — single-value validators and reference existence — returning
// every failure ATTRIBUTED to its field (empty slice = valid). Scoped to the one
// file and compile-free. It does not run whole-config concerns beyond references
// (e.g. deferred external checks); those stay in Validate.
//
// Attribution is per declared field ("domains", not "domains/0"); the message
// already names the offending value.
func (s *Session) ValidateResource(res []string) []FieldIssue {
	if s.bind.FieldValidator == nil {
		return nil
	}
	group := s.resGroup(res)
	var issues []FieldIssue
	// A required field that is absent has no value to check, so it must be
	// looked for by name — otherwise a resource with nothing in it validates
	// clean here and is then rejected by the compiler.
	for _, rf := range s.bind.FieldValidator.RequiredFields(group) {
		if !s.requiredNow(res, rf) || s.hasValue(res, rf.FieldRef) {
			continue
		}
		at := s.authoredAt(res, rf.FieldRef)
		msg := fmt.Sprintf("required field %q is missing", strings.Join(at, "/"))
		switch {
		case rf.Unless:
			msg = fmt.Sprintf("%s is required unless %s is true", strings.Join(at, "/"),
				strings.Join(rf.When.Path, "/"))
		case rf.When != nil:
			msg = fmt.Sprintf("%s is required when %s is %s", strings.Join(at, "/"),
				strings.Join(rf.When.Path, "/"), strings.Join(rf.WhenIn, " or "))
		}
		issues = append(issues, FieldIssue{Field: at, Message: msg})
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

// hasValue reports whether a field is authored at either of its locations. The
// compat alias counts: the compiler reads it as a fallback, so a field present
// only under its legacy key is present, not missing.
func (s *Session) hasValue(res []string, f FieldRef) bool {
	// Candidates: every location the DSL allows, plus the legacy alias. Built
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
// unless the DSL branches) followed by its legacy alias.
func (f FieldRef) locations() [][]string {
	out := make([][]string, 0, len(f.AnyOf)+2)
	if len(f.AnyOf) > 0 {
		out = append(out, f.AnyOf...)
	} else {
		out = append(out, f.Path)
	}
	if len(f.Compat) > 0 {
		out = append(out, f.Compat)
	}
	return out
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
	if rf.When == nil {
		return true
	}
	v, err := s.Get(res, rf.When.Path)
	if err != nil && len(rf.When.Compat) > 0 {
		v, err = s.Get(res, rf.When.Compat)
	}
	if rf.Unless {
		// Required unless the discriminator reads as true. Absent, unreadable
		// or non-bool all mean required — an unset bool is false. Read as a
		// real bool on both sides, so the two paths cannot drift over casing
		// or stringification.
		b, ok := v.(bool)
		return err != nil || !ok || !b
	}
	if err != nil {
		return false
	}
	got, ok := v.(string)
	return ok && slices.Contains(rf.WhenIn, got)
}

// Serialize flushes pending edits and returns ONE resource's document: its
// repo-relative path and its exact YAML, comments preserved. Where the document
// lives is the DSL's business, so the caller never names the file — which is how
// a caller ends up reading a path tcc never wrote.
func (s *Session) Serialize(res []string) (path string, data []byte, err error) {
	if err = s.seer.Sync(); err != nil {
		return "", nil, err
	}
	q, err := s.query(res, nil)
	if err != nil {
		return "", nil, err
	}
	// Resolve as a read so yaseer populates the document's file path. An empty
	// document resolves to no value but still has a path, so the read error is
	// not fatal here — ReadFile below is the honest existence check.
	var v any
	_ = q.Value(&v)
	fp := q.FilePath()
	if fp == "" {
		return "", nil, fmt.Errorf("session: no document for %q", strings.Join(res, "/"))
	}
	if data, err = afero.ReadFile(s.fs, fp); err != nil {
		return "", nil, err
	}
	return strings.TrimPrefix(fp, "/"), data, nil
}

// SetResource makes res's document equal doc, as the minimal set of field writes
// and deletes: maps recurse, arrays and scalars are leaves, and a key missing
// from doc is deleted. Untouched YAML — comments included — survives. An
// explicit nil writes a null; absence deletes.
//
// The ops are applied through a fork so a diff that fails partway can't leave
// the resource half-written.
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
		if !sameValue(prev[k], nv) {
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

// Address is the canonical address of one resource, by kind. This is the entry
// point for a caller that knows a kind only as a string: it needs no group-name
// table, no plural rule, and no special case for the container kind (an
// application is addressed like anything else — passing app for one is an
// error, since containers don't nest). An empty app means project scope.
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
	return []string{c.Group, app, k.Group, name}, nil
}

// Names lists the instances of a kind in scope — the whole project when app is
// empty, one application's own otherwise. An absent directory is simply empty.
//
// Scoping a CONTAINER kind is an error, exactly as it is in Address: containers
// don't nest, so "the applications inside this application" is not a question
// with an answer. Answering it anyway — by quietly ignoring app and returning
// all of them — would hide the caller's bug, and is the flattening of the
// container/resource distinction this API exists to avoid.
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
// ok is false when the path is not a resource document under the DSL's layout
// (an unknown directory, a non-YAML file, .git, ...). The inverse of the
// addressing Serialize returns.
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
	switch len(segs) {
	case 2:
		return segs, true
	case 3: // a container instance's own document addresses the container
		return segs[:2], segs[2] == l.ContainerDoc(segs[0])
	case 4:
		return segs, l.ContainerDoc(segs[0]) != "" && s.hasGroup(segs[2])
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

// Generate mints a value for one of a resource's DSL-generated fields (its id),
// so no consumer carries its own "how do I make an id". The resource's address
// is mixed in, plus any seed the caller adds; a generator is required to supply
// its own entropy on top (see engine.Generate), so uniqueness never depends on
// the seed — which is why this needs to know nothing about the DSL's fields.
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

// Complete returns completion candidates for a field's value, filtered by the
// partial string the user has typed (case-insensitive prefix; "" = all). Fixed
// candidates come from the DSL (enum members, shape literals); reference fields
// also offer the target group's instances IN SCOPE (the resource's own app plus
// root/global), each prefixed. An unknown field path is an error (so a typo isn't
// mistaken for "no suggestions"); a known field with no candidates returns an
// empty slice. Returns (nil, nil) if completion isn't wired.
func (s *Session) Complete(res, field []string, partial string) ([]string, error) {
	if s.bind.Completer == nil {
		return nil, nil
	}
	group := s.resGroup(res)
	values, refGroup, refPrefix, found := s.bind.Completer.Field(group, field)
	if !found {
		return nil, fmt.Errorf("unknown field %q on %q", strings.Join(field, "/"), group)
	}
	cands := append([]string(nil), values...)
	if refGroup != "" {
		for _, name := range s.scopedNames(res, refGroup) {
			cands = append(cands, refPrefix+name)
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
