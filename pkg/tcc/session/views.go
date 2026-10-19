package session

import (
	"context"
	"sort"

	"github.com/taubyte/tau/pkg/tcc/interp"
)

// WHAT EACH DESTINATION HOLDS.
//
// A compiled configuration is one object, and the places it runs are not always one:
// each receives its own cut of it. What the cut is — which resources go where, and in
// what role — is the DSL's answer, wired as a Viewer; the session asks it the same way
// for a CLI, a console and an explorer, so none of them computes a cut of its own and
// none can disagree with what a deploy publishes.
//
// THE CANONICAL OBJECT TRAVELS WITH THE VIEWS. Tooling that edits or explains the whole
// configuration still needs all of it; a view is what one destination receives, never
// a replacement for the config.

// Viewer cuts a compiled configuration — compiled for one environment, or for none —
// into one view per destination.
type Viewer interface {
	Views(flat map[string]any, environment string) ([]View, []Note, error)
}

// View is one destination's cut of a compiled configuration.
type View struct {
	Destination string `json:"destination"`
	// Object is the compiled object as that destination holds it.
	Object map[string]any `json:"object"`
	// Roles is how each resource the view holds is there, by compiled id. Nil when the
	// DSL describes no cut, and the view is simply everything.
	Roles map[string]string `json:"roles"`
}

// Empty reports whether the destination holds nothing — which is a destination,
// unlike one that does not exist.
func (v View) Empty() bool { return v.Roles != nil && len(v.Roles) == 0 }

// Note is something the Viewer had to say about the cut: a resource it could not place,
// an edge it refused.
type Note struct {
	Severity string `json:"severity"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Scope    string `json:"scope,omitempty"`
	ID       string `json:"id,omitempty"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
}

// Viewed is a compile with its views: the whole object, and what each destination holds.
type Viewed struct {
	Canonical interp.Object
	Views     []View
	Notes     []Note
}

// Views compiles the configuration and cuts it into its views, sorted by destination.
// A session whose DSL wires no Viewer has a single view holding everything, with no
// destination name — there is nothing to cut by.
//
// A cut the Viewer refuses returns its error with the notes that explain it, and the
// canonical object, so a caller can show both.
func (s *Session) Views(ctx context.Context, opts CompileOptions) (Viewed, error) {
	obj, _, err := s.Compile(ctx, opts)
	if err != nil {
		return Viewed{}, err
	}
	out := Viewed{Canonical: obj}
	if s.bind.Viewer == nil {
		out.Views = []View{{Object: obj.Flat()}}
		return out, nil
	}
	views, notes, err := s.bind.Viewer.Views(obj.Flat(), opts.Environment)
	out.Notes = notes
	if err != nil {
		return out, err
	}
	sort.Slice(views, func(i, j int) bool { return views[i].Destination < views[j].Destination })
	out.Views = views
	return out, nil
}
