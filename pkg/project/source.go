package project

import "sort"

// source.go answers the two questions a build asks of a compiled project:
// where does this resource's content come from, and how is it made.
//
// THEY ARE READ RATHER THAN GUESSED. A resource's `source` is an Either — a
// bare path, a git repository, a component, an image, a builtin — and its
// `build` is another one. The compiler flattens both to a `kind` beside the
// branch's own fields, so reading them is a switch on that key and never a
// test for which fields happen to be present.

// Source kinds, as the compiler spells them. A bare path has no kind: it is a
// string where the others are objects.
const (
	SourcePath      = ""
	SourceGit       = "git"
	SourceComponent = "component"
	SourceImage     = "image"
	SourceBuiltin   = "builtin"
)

// Build kinds.
const (
	BuildCommands   = "commands"
	BuildScript     = "script"
	BuildDockerfile = "dockerfile"
)

// Source is where a resource's content comes from.
type Source struct {
	// Declared says the author wrote a source at all.
	//
	// IT IS NOT DERIVABLE FROM THE OTHER FIELDS. A bare path is the common
	// case and is written as a plain string, so a resource that declares NO
	// source reads as a path of "" — which is indistinguishable from a
	// resource whose source is the repository root, and that is a real thing
	// to write. Without this, every resource in a project that happened to
	// carry a build stanza and no source would be built from the checkout
	// root.
	Declared bool

	// Kind is one of the Source* constants. Empty means a path in the
	// project's own code repository, which is the common case and the reason
	// the vocabulary lets it be written as a bare string.
	Kind string

	// Path is the location within whichever repository applies. Empty is that
	// repository's root.
	Path string

	// Git is a clone URL, and it is the whole of what names the forge. There
	// is no provider field: the URL carries the host, so GitLab, Gitea,
	// Codeberg and an internal mirror are values rather than schema edits.
	Git    string
	Branch string

	// Component names another resource of this project whose artifact this one
	// runs; Image is a reference pulled rather than built.
	Component string
	Image     string
}

// Built reports whether this source is something this platform makes. An image
// is pulled and a builtin is already here; a component is built as its own
// resource rather than again as this one.
func (s Source) Built() bool {
	return s.Declared && (s.Kind == SourcePath || s.Kind == SourceGit)
}

// Build is how a resource is made.
type Build struct {
	// Kind is one of the Build* constants, and "" is a resource that declares
	// no build at all.
	Kind string

	// Image is the toolchain the build runs in.
	Image string

	// Install and Run are the two commands, for Kind commands.
	Install string
	Run     string

	// Script is a path in the repository to execute, for Kind script. It is
	// the escape hatch for a build that does not fit two commands.
	Script string

	// Dockerfile is a path to one, for Kind dockerfile.
	Dockerfile string

	// Output is what the build leaves behind: a file for a wasm component, a
	// directory for a website.
	Output string

	// Target names which of a multi-target build to take.
	Target string
}

// Declared reports whether the author said how this is made.
func (b Build) Declared() bool { return b.Kind != "" }

// Source reads a resource's source block.
func (r Resource) Source() Source {
	// A BARE PATH IS A STRING WHERE THE OTHERS ARE OBJECTS, which is the
	// vocabulary letting the common case be written the short way. Reading it
	// as an object would find nothing and report a resource with no source.
	switch v := descend(r, "source").(type) {
	case string:
		return Source{Declared: true, Path: v}
	case map[string]any:
	default:
		// No source block at all.
		return Source{}
	}

	return Source{
		Declared:  true,
		Kind:      r.String("source", "kind"),
		Path:      r.String("source", "path"),
		Git:       r.String("source", "git"),
		Branch:    r.String("source", "branch"),
		Component: r.String("source", "component"),
		Image:     r.String("source", "image"),
	}
}

// Build reads a resource's build block.
func (r Resource) Build() Build {
	return Build{
		Kind:       r.String("build", "kind"),
		Image:      r.String("build", "image"),
		Install:    r.String("build", "install"),
		Run:        r.String("build", "run"),
		Script:     r.String("build", "script"),
		Dockerfile: r.String("build", "dockerfile"),
		Output:     r.String("build", "output"),
		Target:     r.String("build", "target"),
	}
}

// Buildable is one resource this project makes from source.
type Buildable struct {
	// Scope, Kind and Name address it, in the form Resource takes.
	Scope []string
	Kind  string
	Name  string

	// ID is the resource's own id, which is what an artifact is filed under.
	ID string

	Source Source
	Build  Build
}

// Buildables is every resource this project makes from source, in a stable
// order.
//
// IT IS THE WHOLE PROJECT AND NOT ONE REPOSITORY'S WORTH, because which
// repository a resource belongs to is only half in the config: a resource whose
// source is a bare path belongs to the project's own code repository, and what
// that repository IS lives in auth. The caller has that and filters; this reads
// what the config says.
//
// A resource that declares a source it does not build — an image, a builtin, a
// component — is not here. Neither is one with a source and no build: nothing
// says how to make it, and guessing would be this package inventing a toolchain.
func (p *Project) Buildables() []Buildable {
	out := make([]Buildable, 0, len(p.located))

	for id, at := range p.located {
		// THE ADDRESS IS A SCOPE AND `at` TAKES A CURSOR. Handing it the scope
		// directly reads the project root for every resource, whatever
		// namespace it is actually in — which finds a resource of that name if
		// one happens to sit at the root and an empty one otherwise.
		cursor, err := p.scopeCursor(at.scope)
		if err != nil {
			continue
		}
		r, _, err := p.at(cursor, at.kind, at.name)
		if err != nil {
			continue
		}

		source, build := r.Source(), r.Build()
		if !source.Built() || !build.Declared() {
			continue
		}

		out = append(out, Buildable{
			Scope:  at.scope,
			Kind:   at.kind,
			Name:   at.name,
			ID:     id,
			Source: source,
			Build:  build,
		})
	}

	// SORTED, because the source is a map and a build order that changed
	// between two pushes of the same commit is one nobody can read a log of.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}
