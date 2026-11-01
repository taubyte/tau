package interp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/afero"
	"gopkg.in/yaml.v3"

	"github.com/taubyte/tau/pkg/tcc/engine"
	"github.com/taubyte/tau/pkg/tcc/object"
	yaseer "github.com/taubyte/tau/pkg/yaseer"
)

// mount.go is how configuration living with the thing it describes (a site's
// serving behaviour, decided by the build) reaches a compile that cannot fetch.
// tcc has no network, credentials or git, so the compile says what it needs and
// the caller answers:
//
//	mounts, _ := c.Mounts(ctx)        // pass one: what is needed, for THIS environment
//	for _, m := range mounts { ... }  // the caller fetches, however it likes
//	c2, _ := New(..., WithMounted(m.At, fs, commit)...)
//	obj, _, _ := c2.Compile(ctx)      // pass two: one tree, one object
//
// Unlike a validation, a mount's answer changes the object, so an unanswered one
// must not pass silently (see Unfulfilled). A fill cannot mint a resource, so
// two passes suffice: the walk reads the authored object.

// Mount is one request: a resource whose serving block is read from its content
// repository. Branch is resolved for this compiler's environment. At is where
// tcc puts the answer, tcc's to choose since errors are reported against it.
type Mount struct {
	Kind  string
	Name  string
	Scope string
	ID    string

	Repo   string
	Branch string
	Path   string
	Config string

	At string
	// Address is the resource's document as yaseer addresses it, by the vocabulary's
	// layout: a scope level is a directory named after its container.
	Address []string
}

// String is the request as an operator reads it in a log.
func (m Mount) String() string {
	where := m.Kind + "/" + m.Name
	if m.Scope != "" {
		where = m.Scope + "/" + where
	}
	at := m.Repo
	if m.Branch != "" {
		at += "@" + m.Branch
	}
	return where + " reads " + m.Config + " from " + at
}

// Mounts is every resource delegating a block to its source, for this compiler's
// environment. It compiles the authored object: a produced resource has no
// repository of its own, and the authored object is complete before any fill.
func (c *Compiler) Mounts(ctx context.Context) ([]Mount, error) {
	obj, _, err := c.Authored(ctx)
	if err != nil {
		return nil, err
	}

	flat := obj.Flat()
	kinds := scopeKinds(c.compileRoot, flat, nil)
	if inner, ok := flat[ProjectKey].(map[string]any); ok {
		flat = inner
	}

	var out []Mount
	walkResources(c.compileRoot, flat, "", func(kind string, _ *engine.Node, id, name string, res map[string]any, at string) {
		source, ok := res["source"].(map[string]any)
		if !ok {
			return
		}
		config, _ := source["config"].(string)
		if config == "" {
			return
		}
		repo, _ := source["git"].(string)
		branch, _ := source["branch"].(string)
		path, _ := source["path"].(string)

		out = append(out, Mount{
			Kind: kind, Name: name, Scope: at, ID: id,
			Repo: repo, Branch: branch, Path: path, Config: config,
			At:      MountPoint(at, kind, name),
			Address: address(c.layout, at, kind, name, kinds),
		})
	})

	// Stable order, so a caller fetching in sequence and an operator reading a log
	// both see the same thing twice.
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out, nil
}

// MountPoint is where a resource's fill lands in tcc's view, derived so two
// callers produce the same error message.
func MountPoint(scope, kind, name string) string {
	parts := []string{mountRoot}
	if scope != "" {
		parts = append(parts, scope)
	}
	return "/" + strings.Join(append(parts, kind, name), "/")
}

// mountRoot is the directory tcc mounts fills under. It is not a kind and cannot be
// authored: the walk that reads the project reads kind groups, and this is not one.
const mountRoot = ".mounts"

// filled is one answered Mount: what it asked for, and what came back.
type filled struct {
	mount  Mount
	fs     afero.Fs
	commit string
}

// WithMounted answers one Mount with the filesystem its repository was checked
// out to and the commit of that checkout. The commit is required: it is the
// recorded provenance, and a caller that cannot name it has not fetched
// reproducibly.
func WithMounted(m Mount, fs afero.Fs, commit string) Option {
	return func(c *Compiler) error {
		at := m.At
		if at == "" {
			return fmt.Errorf("interp: a mount with no mount point")
		}
		if len(m.Address) == 0 {
			return fmt.Errorf("interp: mount %s has no document address", at)
		}
		if fs == nil {
			return fmt.Errorf("interp: mount %s was answered with no filesystem", at)
		}
		if commit == "" {
			return fmt.Errorf("interp: mount %s was answered with no commit; provenance is not optional", at)
		}
		if c.mounted == nil {
			c.mounted = map[string]filled{}
		}
		c.mounted[at] = filled{mount: m, fs: fs, commit: commit}
		return nil
	}
}

// delegableTop is every top-level key of a kind carrying a delegable attribute:
// the schema marks leaves, a fill writes blocks.
func delegableTop(root *engine.Node, kind string) map[string]bool {
	out := map[string]bool{}
	for _, g := range root.Children {
		if key, _ := g.Match.(string); key != kind || len(g.Children) == 0 {
			continue
		}
		var walk func(n *engine.Node)
		walk = func(n *engine.Node) {
			for _, a := range n.Attributes {
				if !engine.IsDelegable(a) {
					continue
				}
				name := a.Name
				if cut := strings.Index(name, "/"); cut > 0 {
					name = name[:cut]
				}
				out[name] = true
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(g.Children[0])
	}
	return out
}

// ErrFill is a fill saying something it may not, always a refusal: a partly
// applied fill is configuration nobody wrote.
var ErrFill = errors.New("interp: that file may not say this")

// mountedView is the project with every answered fill merged in, before the
// first pass: a delegated block is authored configuration living elsewhere, and
// everything downstream must see it as written here.
func (c *Compiler) mountedView() (afero.Fs, map[string]string, error) {
	base := c.baseFs
	if c.basePath != "" {
		base = afero.NewBasePathFs(base, c.basePath)
	}
	view, err := overlay(base)
	if err != nil {
		return nil, nil, err
	}

	seer, err := yaseer.New(yaseer.VirtualFS(view, "/"))
	if err != nil {
		return nil, nil, err
	}

	// Stable order so two runs of one project produce the same view and the same
	// first refusal.
	points := make([]string, 0, len(c.mounted))
	for point := range c.mounted {
		points = append(points, point)
	}
	sort.Strings(points)

	from := map[string]string{}
	for _, point := range points {
		answer := c.mounted[point]
		m := answer.mount

		fill, err := readFill(answer.fs, m)
		if err != nil {
			return nil, nil, err
		}

		address := m.Address
		var doc map[string]any
		if err := at(seer, address).Value(&doc); err != nil {
			return nil, nil, fmt.Errorf("reading %s: %w", m, err)
		}
		if doc == nil {
			doc = map[string]any{}
		}

		allowed := delegableTop(c.compileRoot, m.Kind)
		for key, value := range fill {
			// WHAT MAY BE DELEGATED IS THE SCHEMA'S, so a key nobody marked is
			// refused by name rather than ignored. Ignoring it would let a fill
			// believe it had set something.
			if !allowed[key] {
				return nil, nil, fmt.Errorf("%w: %s/%s sets %q, which is not delegated — %s may carry %s",
					ErrFill, m.At, m.Config, key, m.Kind, names(allowed))
			}
			// A field with two authors is refused naming both, never merged.
			if _, already := doc[key]; already {
				return nil, nil, fmt.Errorf("%w: %q is set both here and in %s of %s — only one may say it",
					ErrFill, key, m.Config, m.Repo)
			}
			doc[key] = value
		}

		if err := at(seer, address).Set(doc).Commit(); err != nil {
			return nil, nil, fmt.Errorf("merging %s: %w", m, err)
		}
		from[m.At] = answer.commit
	}

	return view, from, seer.Sync()
}

// readFill decodes one fill, and refuses one that is not a mapping of blocks.
func readFill(fs afero.Fs, m Mount) (map[string]any, error) {
	path := m.Config
	if m.Path != "" {
		path = strings.TrimSuffix(m.Path, "/") + "/" + m.Config
	}
	body, err := afero.ReadFile(fs, path)
	if err != nil {
		return nil, fmt.Errorf("%w: %s names %s and it could not be read: %v", ErrFill, m.At, path, err)
	}
	var fill map[string]any
	if err := yaml.Unmarshal(body, &fill); err != nil {
		return nil, fmt.Errorf("%w: %s is not readable as yaml: %v", ErrFill, path, err)
	}
	return fill, nil
}

// names is a sorted, readable list for a refusal.
func names(set map[string]bool) string {
	if len(set) == 0 {
		return "nothing"
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}

// MountedKey is the record of what this compile read from elsewhere, beside the
// project like LoweredKey.
const MountedKey = "mounted"

// Fill is where one resource's delegated block was read from. The commit is the
// point: a branch moves.
type Fill struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch,omitempty"`
	Commit string `json:"commit"`
	Config string `json:"config"`

	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Scope string `json:"scope,omitempty"`
}

// MountedOf reads the record back.
func MountedOf(flat map[string]any) map[string]Fill {
	out := map[string]Fill{}
	rec, ok := flat[MountedKey].(map[string]any)
	if !ok {
		return out
	}
	for id, v := range rec {
		m, _ := v.(map[string]any)
		out[id] = Fill{
			Repo: str(m, "repo"), Branch: str(m, "branch"), Commit: str(m, "commit"),
			Config: str(m, "config"), Kind: str(m, "kind"), Name: str(m, "name"),
			Scope: str(m, "scope"),
		}
	}
	return out
}

// recordMounts writes the record. A compile that mounted nothing writes nothing, so
// an object is unchanged by a feature its project does not use.
func (c *Compiler) recordMounts(obj Object) error {
	if len(c.mounted) == 0 {
		return nil
	}

	ids := make([]string, 0, len(c.mounted))
	byID := map[string]filled{}
	for _, answer := range c.mounted {
		byID[answer.mount.ID] = answer
		ids = append(ids, answer.mount.ID)
	}
	sort.Strings(ids)

	rec := object.New[object.Refrence]()
	for _, id := range ids {
		answer := byID[id]
		m := answer.mount
		one := object.New[object.Refrence]()
		one.Set("repo", m.Repo)
		if m.Branch != "" {
			one.Set("branch", m.Branch)
		}
		one.Set("commit", answer.commit)
		one.Set("config", m.Config)
		one.Set("kind", m.Kind)
		one.Set("name", m.Name)
		if m.Scope != "" {
			one.Set("scope", m.Scope)
		}
		if err := rec.Child(id).Add(one); err != nil {
			return err
		}
	}
	return obj.Child(MountedKey).Add(rec)
}

// Mounter answers a Mount during the compile, so a caller makes one call. Unlike
// Resolver it has no permissive stub: absent, mounts go unanswered and the audit
// says so. tcc still does no git; the callback is the caller's.
type Mounter interface {
	// Mount checks out what a Mount names and answers with the filesystem it landed
	// in and the COMMIT that checkout is of. An error fails the compile: a caller
	// that cannot fetch must not be taken to mean the resource delegates nothing.
	Mount(ctx context.Context, m Mount) (afero.Fs, string, error)
}

// WithMounter makes the two passes internal. A caller checking out on its own
// terms uses Mounts and WithMounted instead.
func WithMounter(f Mounter) Option {
	return func(c *Compiler) error {
		if f == nil {
			return fmt.Errorf("interp: a nil mounter")
		}
		c.mounter = f
		return nil
	}
}

// mountAll is the first pass, run once when a Mounter is wired, lazily: finding
// what to mount is itself a compile, so the compiler asks, then rebuilds over the
// merged view.
func (c *Compiler) mountAll(ctx context.Context) error {
	if c.mounter == nil || c.mountedOnce {
		return nil
	}
	c.mountedOnce = true

	mounts, err := c.Mounts(ctx)
	if err != nil {
		return err
	}
	if len(mounts) == 0 {
		return nil
	}

	for _, m := range mounts {
		// An answer already given by hand wins. A caller mixing the two is telling
		// tcc it has this one and not the others, and asking anyway would throw away
		// the answer it just gave.
		if _, done := c.mounted[m.At]; done {
			continue
		}
		fs, commit, err := c.mounter.Mount(ctx, m)
		if err != nil {
			return fmt.Errorf("mounting %s: %w", m, err)
		}
		if err := WithMounted(m, fs, commit)(c); err != nil {
			return err
		}
	}

	view, from, err := c.mountedView()
	if err != nil {
		return err
	}
	c.seerOptions = []yaseer.Option{yaseer.VirtualFS(view, "/")}
	c.baseFs, c.basePath = view, "/"
	c.from = from

	// Rebuilt rather than patched: one engine over one filesystem.
	c.engine, err = engine.NewWith(c.project, c.varValues, c.seerOptions...)
	return err
}
