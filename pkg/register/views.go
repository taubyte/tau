package register

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// views.go is what one DESTINATION of a deployment reads.
//
// A deploy publishes the whole compiled object and, beside it, one view per
// destination — the part of the object a place that runs part of the project holds.
// What a destination is, and how a view is cut, is not this package's: a view is bytes
// under a name, exactly as the whole object is.
//
// # Three things are written, and none is ever rewritten
//
//	o/<id>                                                 every object — views, the whole one, the manifest
//	m/<project>/<env>/<revision>/<manifest>                one revision's manifest
//	d/<destination>/<project>/<env>/<revision>/<manifest>  that destination is in it
//
// THE MANIFEST IS AN OBJECT LIKE ANY OTHER, named by its content: it says which object
// each destination of one revision holds, and which object is the whole. So one revision
// of one deployment is one immutable fact, and the two index rows that point at it carry
// nothing but their keys.
//
// NOTHING HERE HAS A WRITER IT CAN COLLIDE WITH. The store is replicated and merges
// concurrent writes per key with the last writer winning, and Publish's lock is held by
// one process — so two nodes publishing at once are not ordered by anything. Every key
// above carries everything that tells its entry apart, the manifest's own content
// address included, so two publishes write two entries rather than one over the other.
// Which one is CURRENT is resolved on read, identically on every node: the highest
// revision, and the lowest manifest id among equals.
//
// THE REVISION IS ZERO-PADDED so byte order is numeric order, which is the only sort a
// prefix listing has.
//
// # A reader tolerates what has not arrived yet
//
// Replication delivers rows in no particular order, so an index row can arrive before
// the manifest it names, and a manifest before the view it names. A reader that finds
// the newest revision incomplete reads the newest one that is whole instead, and moves
// on when the rest lands — never an error for a deployment that is merely in flight.
//
// # Empty is not unknown
//
// A destination a revision lists holds a view even when the view holds nothing: the
// deployment reached it and placed nothing there, which is how a destination learns a
// resource was REMOVED from it. A destination a revision does not list is not one of
// that deployment's, and asking for its view is ErrNoView.

var (
	// ErrNoView is a destination the current revision of a deployment does not list.
	ErrNoView = errors.New("register: this deployment holds no view for that destination")

	// ErrNoDestination is a view with no destination name.
	ErrNoDestination = errors.New("register: a view with no destination addresses nothing")
)

const (
	manifestPrefix    = "m" + sep
	destinationPrefix = "d" + sep
	// revisionWidth is a uint64's widest decimal.
	revisionWidth = 20
)

// View is one destination's object, as a publisher hands it over.
type View struct {
	Destination string
	Compiled    []byte
}

// Manifest is one revision of one deployment: the whole object and every view.
type Manifest struct {
	Project, Environment string
	Revision             uint64
	// Object is the whole compiled object.
	Object ObjectID
	// Whole is a publication nobody cut: every destination reads Object.
	Whole bool
	// Views is each destination's object.
	Views map[string]ObjectID
}

// ViewPointer is what one destination of one deployment runs now.
type ViewPointer struct {
	Object   ObjectID
	Revision uint64
	// Manifest is the revision's manifest, which names the whole object beside it.
	Manifest ObjectID
}

// Assignment is a deployment a destination holds a view of.
type Assignment struct {
	Project, Environment string
	View                 ViewPointer
}

func revisionSegment(r uint64) string {
	return fmt.Sprintf("%0*d", revisionWidth, r)
}

func manifestKey(project, environment string, revision uint64, id ObjectID) string {
	return manifestPrefix + project + sep + environment + sep + revisionSegment(revision) + sep + string(id)
}

func destinationKey(destination, project, environment string, revision uint64, id ObjectID) string {
	return destinationPrefix + destination + sep + project + sep + environment + sep + revisionSegment(revision) + sep + string(id)
}

// encodeManifest is one line per fact, sorted, so the same manifest is the same bytes
// and the same content address on every node. Nothing outside this package reads it.
func encodeManifest(m Manifest) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "manifest 1\nproject %s\nenvironment %s\nrevision %d\nobject %s\n", m.Project, m.Environment, m.Revision, m.Object)
	if m.Whole {
		b.WriteString("whole\n")
	}
	dests := make([]string, 0, len(m.Views))
	for d := range m.Views {
		dests = append(dests, d)
	}
	sort.Strings(dests)
	for _, d := range dests {
		fmt.Fprintf(&b, "view %s %s\n", d, m.Views[d])
	}
	return []byte(b.String())
}

func decodeManifest(b []byte) (Manifest, error) {
	m := Manifest{Views: map[string]ObjectID{}}
	lines := strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
	if len(lines) == 0 || lines[0] != "manifest 1" {
		return Manifest{}, fmt.Errorf("%w: not a manifest", ErrMalformedPointer)
	}
	for _, line := range lines[1:] {
		word, rest, _ := strings.Cut(line, " ")
		switch word {
		case "project":
			m.Project = rest
		case "environment":
			m.Environment = rest
		case "revision":
			r, err := strconv.ParseUint(rest, 10, 64)
			if err != nil {
				return Manifest{}, fmt.Errorf("%w: manifest revision %q", ErrMalformedPointer, rest)
			}
			m.Revision = r
		case "object":
			m.Object = ObjectID(rest)
		case "whole":
			m.Whole = true
		case "view":
			d, id, ok := strings.Cut(rest, " ")
			if !ok || d == "" || id == "" {
				return Manifest{}, fmt.Errorf("%w: manifest view %q", ErrMalformedPointer, rest)
			}
			m.Views[d] = ObjectID(id)
		default:
			return Manifest{}, fmt.Errorf("%w: manifest line %q", ErrMalformedPointer, line)
		}
	}
	if m.Object == "" {
		return Manifest{}, fmt.Errorf("%w: a manifest naming no object", ErrMalformedPointer)
	}
	return m, nil
}

// publishViews writes a revision's views and its manifest, and indexes both. The whole
// object is already stored. It runs before the pointer moves, so a pointer always has a
// manifest behind it.
func (r *Register) publishViews(ctx context.Context, p Publication, object ObjectID) error {
	m := Manifest{Project: p.Project, Environment: p.Environment, Revision: p.Revision, Object: object,
		Whole: p.Views == nil, Views: map[string]ObjectID{}}
	for _, v := range p.Views {
		if err := checkName(v.Destination, ErrNoDestination); err != nil {
			return err
		}
		if len(v.Compiled) == 0 {
			return fmt.Errorf("%w: the view for %s", ErrEmptyObject, v.Destination)
		}
		if _, dup := m.Views[v.Destination]; dup {
			return fmt.Errorf("register: two views for %s", v.Destination)
		}
		id := IDOf(v.Compiled)
		if err := r.store.Put(ctx, objectKey(id), v.Compiled); err != nil {
			return fmt.Errorf("register: storing the view for %s: %w", v.Destination, err)
		}
		m.Views[v.Destination] = id
	}

	body := encodeManifest(m)
	id := IDOf(body)
	if err := r.store.Put(ctx, objectKey(id), body); err != nil {
		return fmt.Errorf("register: storing the manifest: %w", err)
	}
	// THE DESTINATIONS BEFORE THE MANIFEST'S OWN ROW. A destination listing its rows and
	// a reader resolving the deployment then agree as soon as either can see the
	// manifest at all.
	for d := range m.Views {
		if err := r.store.Put(ctx, destinationKey(d, p.Project, p.Environment, p.Revision, id), nil); err != nil {
			return fmt.Errorf("register: assigning %s/%s to %s: %w", p.Project, p.Environment, d, err)
		}
	}
	if err := r.store.Put(ctx, manifestKey(p.Project, p.Environment, p.Revision, id), nil); err != nil {
		return fmt.Errorf("register: indexing the manifest: %w", err)
	}
	return nil
}

// entry is one index row, read back.
type entry struct {
	revision uint64
	manifest ObjectID
}

// entriesUnder reads `<prefix><revision>/<manifest>` rows, newest first and, among
// equal revisions, lowest manifest first — the one order every node agrees on.
func (r *Register) entriesUnder(ctx context.Context, prefix string) ([]entry, error) {
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var out []entry
	for _, key := range keys {
		// cut wherever the prefix is: a store answers with its own normalised key
		_, rest, found := strings.Cut(key, prefix)
		if !found {
			continue
		}
		rev, id, ok := strings.Cut(rest, sep)
		if !ok || len(rev) != revisionWidth || id == "" || strings.Contains(id, sep) {
			continue
		}
		n, err := strconv.ParseUint(rev, 10, 64)
		if err != nil {
			continue
		}
		out = append(out, entry{revision: n, manifest: ObjectID(id)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].revision != out[j].revision {
			return out[i].revision > out[j].revision
		}
		return out[i].manifest < out[j].manifest
	})
	return out, nil
}

// manifest reads one manifest, reporting whether it has arrived.
func (r *Register) manifest(ctx context.Context, id ObjectID) (Manifest, bool, error) {
	b, ok, err := r.store.Get(ctx, objectKey(id))
	if err != nil || !ok {
		return Manifest{}, false, err
	}
	m, err := decodeManifest(b)
	if err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

func (r *Register) has(ctx context.Context, id ObjectID) (bool, error) {
	_, ok, err := r.store.Get(ctx, objectKey(id))
	return ok, err
}

// Manifest is the current revision of a deployment: the newest whose manifest has
// arrived.
func (r *Register) Manifest(ctx context.Context, project, environment string) (Manifest, ObjectID, error) {
	if err := checkName(project, ErrNoProject); err != nil {
		return Manifest{}, "", err
	}
	if err := checkName(environment, ErrNoEnvironment); err != nil {
		return Manifest{}, "", err
	}
	entries, err := r.entriesUnder(ctx, manifestPrefix+project+sep+environment+sep)
	if err != nil {
		return Manifest{}, "", fmt.Errorf("register: listing %s/%s's revisions: %w", project, environment, err)
	}
	for _, e := range entries {
		m, ok, err := r.manifest(ctx, e.manifest)
		if err != nil {
			return Manifest{}, "", err
		}
		if ok {
			return m, e.manifest, nil
		}
	}
	return Manifest{}, "", fmt.Errorf("%w: %s/%s", ErrNotPublished, project, environment)
}

// ResolveView is the read a destination makes: which object it holds of this
// deployment, now.
//
// THE NEWEST REVISION WHOSE VIEW HAS ARRIVED. The newest manifest decides whether the
// destination is one of the deployment's at all — a destination it does not list is
// ErrNoView, whatever older revisions said, which is how a deployment withdrawn from a
// destination stops being served there. Its view not having arrived yet is not that:
// the destination keeps reading the newest revision it can, and moves on when it lands.
func (r *Register) ResolveView(ctx context.Context, project, environment, destination string) (ViewPointer, error) {
	if err := checkName(destination, ErrNoDestination); err != nil {
		return ViewPointer{}, err
	}
	if err := checkName(project, ErrNoProject); err != nil {
		return ViewPointer{}, err
	}
	if err := checkName(environment, ErrNoEnvironment); err != nil {
		return ViewPointer{}, err
	}
	entries, err := r.entriesUnder(ctx, manifestPrefix+project+sep+environment+sep)
	if err != nil {
		return ViewPointer{}, fmt.Errorf("register: listing %s/%s's revisions: %w", project, environment, err)
	}
	decided := false
	for _, e := range entries {
		m, ok, err := r.manifest(ctx, e.manifest)
		if err != nil {
			return ViewPointer{}, err
		}
		if !ok {
			continue
		}
		id, listed := m.Views[destination]
		if m.Whole {
			id, listed = m.Object, true
		}
		if !decided && !listed {
			return ViewPointer{}, fmt.Errorf("%w: %s/%s at revision %d does not list %s", ErrNoView, project, environment, m.Revision, destination)
		}
		decided = true
		if !listed {
			continue
		}
		arrived, err := r.has(ctx, id)
		if err != nil {
			return ViewPointer{}, err
		}
		if arrived {
			return ViewPointer{Object: id, Revision: m.Revision, Manifest: e.manifest}, nil
		}
	}
	return ViewPointer{}, fmt.Errorf("%w: %s/%s for %s", ErrNotPublished, project, environment, destination)
}

// Assigned is every deployment a destination currently holds a view of, sorted.
//
// IT IS HOW A DESTINATION LEARNS OF A DEPLOYMENT NEWLY ITS BUSINESS without reading
// every project: one prefix of its own. A row says the destination was in some revision;
// whether it still is, is the deployment's current manifest's to say, so a deployment
// withdrawn from the destination is not listed even though its old rows remain.
func (r *Register) Assigned(ctx context.Context, destination string) ([]Assignment, error) {
	if err := checkName(destination, ErrNoDestination); err != nil {
		return nil, err
	}
	prefix := destinationPrefix + destination + sep
	keys, err := r.store.List(ctx, prefix)
	if err != nil {
		return nil, fmt.Errorf("register: listing what %s holds: %w", destination, err)
	}
	seen := map[Deployment]bool{}
	for _, key := range keys {
		_, rest, found := strings.Cut(key, prefix)
		if !found {
			continue
		}
		parts := strings.Split(rest, sep)
		if len(parts) != 4 || parts[0] == "" || parts[1] == "" {
			continue
		}
		seen[Deployment{Project: parts[0], Environment: parts[1]}] = true
	}
	deployments := make([]Deployment, 0, len(seen))
	for d := range seen {
		deployments = append(deployments, d)
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].String() < deployments[j].String() })

	var out []Assignment
	for _, d := range deployments {
		v, err := r.ResolveView(ctx, d.Project, d.Environment, destination)
		switch {
		case err == nil:
			out = append(out, Assignment{Project: d.Project, Environment: d.Environment, View: v})
		case errors.Is(err, ErrNoView), errors.Is(err, ErrNotPublished):
		default:
			return nil, err
		}
	}
	return out, nil
}

// InRegion is a Registry that reads one destination's views: what a node serving that
// destination reads everything through, so every consumer of the cache gets the view
// without knowing there is one.
func InRegion(r interface {
	ResolveView(ctx context.Context, project, environment, destination string) (ViewPointer, error)
	Object(ctx context.Context, id ObjectID) ([]byte, error)
}, destination string) Registry {
	return inRegion{r: r, destination: destination}
}

type inRegion struct {
	r interface {
		ResolveView(ctx context.Context, project, environment, destination string) (ViewPointer, error)
		Object(ctx context.Context, id ObjectID) ([]byte, error)
	}
	destination string
}

func (v inRegion) Resolve(ctx context.Context, project, environment string) (Pointer, error) {
	p, err := v.r.ResolveView(ctx, project, environment, v.destination)
	if err != nil {
		return Pointer{}, err
	}
	return Pointer{Object: p.Object, Revision: p.Revision}, nil
}

func (v inRegion) Object(ctx context.Context, id ObjectID) ([]byte, error) {
	return v.r.Object(ctx, id)
}
