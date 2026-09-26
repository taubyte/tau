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

// mount.go is how configuration that lives with the THING IT DESCRIBES reaches a
// compile that has no way to fetch it.
//
// # The problem
//
// A resource whose content is in its own repository has serving behaviour that is a
// property of the BUILD: what a directory returns, what an unknown path returns.
// Those change when the site changes and are decided by whoever writes the site. Put
// them in the configuration repository and every such change is a cross-repository
// one, owned by somebody else.
//
// # Why it is two passes and not a fetch
//
// tcc reads a filesystem. It has no network, no credentials and no git, on purpose:
// a compiler that fetched would be non-reproducible, would need secrets, and could
// not be run by an editor. So the compile SAYS WHAT IT NEEDS and the caller — which
// already has a checkout, a token and a cache — answers.
//
//	mounts, _ := c.Mounts(ctx)        // pass one: what is needed, for THIS environment
//	for _, m := range mounts { ... }  // the caller fetches, however it likes
//	c2, _ := New(..., WithMounted(m.At, fs, commit)...)
//	obj, _, _ := c2.Compile(ctx)      // pass two: one tree, one object
//
// This is the shape Resolver and NextValidation already have — tcc states a question
// it cannot answer and the caller answers it — with one difference that matters: a
// validation's answer does not change the object, and a mount's does. So an
// unanswered mount must not silently pass the way an unwired Resolver does; see
// Unfulfilled.
//
// # Two passes, never three
//
// A fill may not mint a resource, so pass two cannot discover a mount pass one did
// not already ask for. That is not a convention this enforces afterwards — it is why
// the walk below reads the AUTHORED object, which is complete without any fill.

// Mount is one request: a resource whose serving block is to be read from the
// repository its content comes from.
//
// Branch is already resolved for the environment this compiler was built for, so a
// caller checks out what it is told rather than re-deriving it. At is where tcc will
// put the answer in its own view, and it is tcc's to choose because it becomes the
// path an error is reported against.
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

// Mounts is every resource in this project that delegates a block to its source, read
// for the environment this compiler was built for.
//
// IT COMPILES THE AUTHORED OBJECT AND NOT THE LOWERED ONE. A lowering produces
// resources, and a produced resource has no repository of its own — its source is the
// thing that produced it. Reading the authored object is also what makes the walk
// complete before any fill exists, which is what bounds this to two passes.
func (c *Compiler) Mounts(ctx context.Context) ([]Mount, error) {
	obj, _, err := c.Authored(ctx)
	if err != nil {
		return nil, err
	}

	flat := obj.Flat()
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
			At: MountPoint(at, kind, name),
		})
	})

	// Stable order, so a caller fetching in sequence and an operator reading a log
	// both see the same thing twice.
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out, nil
}

// MountPoint is where a resource's fill lands in tcc's view.
//
// IT IS DERIVED AND NOT CHOSEN, by tcc and not by the caller, because it is the path
// an error carries: a fill that declares something it may not is reported against
// this, and two callers of the same project have to produce the same message.
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

// WithMounted answers one Mount: the filesystem its repository was checked out to,
// and the COMMIT that checkout is of.
//
// THE COMMIT IS NOT OPTIONAL. It is what the compiled object records as provenance,
// and it is the only thing that makes "this serving block came from there" a fact
// rather than a claim — a branch name says what was asked for, a commit says what was
// read. A caller that cannot say which commit it fetched has not fetched
// reproducibly, and the compile refuses rather than recording a blank.
func WithMounted(m Mount, fs afero.Fs, commit string) Option {
	return func(c *Compiler) error {
		at := m.At
		if at == "" {
			return fmt.Errorf("interp: a mount with no mount point")
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

// delegableTop is every TOP-LEVEL key of a kind that carries a delegable attribute.
//
// The schema marks leaves — `serve/index`, `serve/mode` — and a fill writes blocks,
// so the answer is the first segment of each marked name. Read off the compile root
// every time rather than cached: a vocabulary that marks another field gets it here
// with nothing to update.
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

// ErrFill is a fill that says something it may not. Every one of these is a refusal
// and never a warning: a fill that was partly applied is a configuration nobody
// wrote and nobody can read back.
var ErrFill = errors.New("interp: that file may not say this")

// mountedView is the project with every answered fill merged into it.
//
// THE FILL IS MERGED BEFORE THE FIRST PASS AND NOT AFTER, which is the whole reason
// this is not part of lowering. A delegated block is AUTHORED configuration that
// happens to live elsewhere; everything downstream — references, the lowering plan,
// the audit — has to see it exactly as if it had been written here. Merging it
// afterwards would give the first pass one project and the second another.
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

		address := m.address()
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
			// A FIELD WITH TWO AUTHORS IS THE SAME DEFECT AS A SWITCH WITH TWO
			// BRANCHES OPEN: there is no rule for which wins that is not a
			// surprise to one of them. Refused naming both, never merged.
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

// address is where this resource's document lives, as yaseer addresses one.
func (m Mount) address() []string {
	var out []string
	if m.Scope != "" {
		out = append(out, strings.Split(m.Scope, "/")...)
	}
	return append(out, m.Kind, m.Name)
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

// MountedKey is the record of what this compile read from somewhere else. It sits
// beside the project the way LoweredKey does, and for the same reason: a consumer
// has to be able to answer "where did this come from" without re-running anything.
const MountedKey = "mounted"

// Fill is where one resource's delegated block was read from.
//
// THE COMMIT IS THE POINT. A branch says what was asked for and moves; a commit says
// what was read and does not. An object that recorded only the branch would make
// "this deployment serves what that repository said" unfalsifiable a week later.
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

// Mounter answers a Mount while the compile is running, so a caller makes ONE call
// instead of driving two passes itself.
//
// IT IS THE SHAPE Resolver HAS and deliberately not Resolver itself: a resolver
// answers a question ABOUT a value tcc already read, and its stub says yes on
// purpose. This answers with configuration, so a stub that said yes would compile a
// project missing the blocks somebody delegated. There is no stub; absent, the
// mounts go unanswered and the audit says so.
//
// TCC STILL DOES NO GIT. The callback is the caller's, with the caller's credentials
// and the caller's cache; tcc only says which repository at which commit-ish it needs
// and where the answer goes.
type Mounter interface {
	// Mount checks out what a Mount names and answers with the filesystem it landed
	// in and the COMMIT that checkout is of. An error fails the compile: a caller
	// that cannot fetch must not be taken to mean the resource delegates nothing.
	Mount(ctx context.Context, m Mount) (afero.Fs, string, error)
}

// WithMounter makes the two passes internal: Compile discovers what it needs, asks
// this, merges the answers and compiles the whole. A caller that wants to check out on
// its own terms — in parallel, from a warm cache, or not at all — uses Mounts and
// WithMounted instead, which is what this is built on.
func WithMounter(f Mounter) Option {
	return func(c *Compiler) error {
		if f == nil {
			return fmt.Errorf("interp: a nil mounter")
		}
		c.mounter = f
		return nil
	}
}

// mountAll is the first pass, run once, when a Mounter is wired.
//
// IT IS LAZY AND NOT PART OF New, because discovering what to mount is itself a
// compile: the engine has to exist before it can say which resources delegate. So
// the compiler is built unmounted, asks, and rebuilds itself over the merged view —
// which is the same thing a caller driving both passes does by hand, with the same
// two compiles, done where a caller cannot forget one.
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

	// The engine reads documents, so it is the thing that has to be looking at the
	// merged tree. Rebuilt rather than patched: one engine over one filesystem is
	// the invariant that makes a delegated block indistinguishable from an authored
	// one everywhere downstream.
	c.engine, err = engine.NewWith(c.project, c.varValues, c.seerOptions...)
	return err
}
