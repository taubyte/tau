// Package emit turns an import into a project on disk through pkg/tcc/session, so
// the layout is the DSL's, ids are minted by the schema's generator, and every
// required field left empty is reported by the editor's validator.
//
// Empty required fields are the ordinary case (compose names no memory or volume
// size), so the project is written anyway, with every hole in Missing and Compiles
// saying whether tcc accepts it yet.
package emit

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/tcc/session"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Missing is one field the import could not fill.
type Missing struct {
	Kind  string
	Name  string
	Field string
	Why   string
}

func (m Missing) String() string {
	return m.Kind + "/" + m.Name + ": " + m.Field + " — " + m.Why
}

// Report is what an import produced and what it left for a person.
type Report struct {
	// Project is the name the project was written under.
	Project string

	// Written is every file, relative to the directory it was written into.
	Written []string

	// Missing is every required field the import could not fill. It is the
	// to-do list, and it is sorted so two runs diff cleanly.
	Missing []Missing

	// Compiles says whether tcc accepts what was written. False is the ordinary
	// outcome of an import with holes in it, not an error.
	Compiles bool

	// Why is why it does not compile, when it does not.
	Why string

	// Deferred is every check the vocabulary wants made against the world
	// rather than against the file — does this domain resolve, is this cid
	// fetchable. An import cannot make them and must not pretend it did.
	Deferred []string

	// Findings is every importer's findings, carried through so a caller has
	// one thing to show rather than two.
	Findings []importer.Finding
}

// Blocked is every finding that says the source did something tau cannot
// express. A caller deciding whether an import is worth taking reads this first.
func (r Report) Blocked() []importer.Finding { return r.at(importer.Blocked) }

// Elsewhere is every value that is real and is not in the file — the account
// connections an import still needs.
func (r Report) Elsewhere() []importer.Finding { return r.at(importer.Elsewhere) }

func (r Report) at(l importer.Level) []importer.Finding {
	var out []importer.Finding
	for _, f := range r.Findings {
		if f.Level == l {
			out = append(out, f)
		}
	}
	return out
}

// To writes the results as a project under dir in dst.
//
// SEVERAL RESULTS AT ONCE IS THE ORDINARY CASE, not a convenience: a Coolify
// project is a compose file plus what its API holds, and a repository that
// arrived from Vercel may also have a Dockerfile. They become one project.
func To(dst afero.Fs, dir, name string, results ...importer.Result) (Report, error) {
	if dst == nil {
		return Report{}, fmt.Errorf("emit: nowhere to write")
	}
	if name == "" {
		return Report{}, fmt.Errorf("emit: a project needs a name")
	}

	// AdoptSession over an empty filesystem cannot fail today; the error is threaded since
	// pkg/tcc may change.
	s, err := schema.AdoptSession(afero.NewMemMapFs())
	if err != nil {
		return Report{}, fmt.Errorf("emit: opening a session: %w", err)
	}
	defer s.Close()

	report := Report{Project: name}

	if err := identify(s, name); err != nil {
		return Report{}, err
	}

	for _, r := range results {
		report.Findings = append(report.Findings, r.Findings...)

		for _, resource := range r.Resources {
			if resource.Kind == "" {
				return Report{}, fmt.Errorf("emit: %s produced %q and did not say what kind it is", r.Source, resource.Name)
			}
			missing, err := write(s, resource.Kind, resource.Name, resource.Doc)
			if err != nil {
				return Report{}, err
			}
			report.Missing = append(report.Missing, missing...)
		}
	}

	// THE WHOLE-CONFIG PASS SEES WHAT A PER-RESOURCE ONE CANNOT: a reference to
	// something no result produced, a required field on the project itself. Its
	// answer is REPORTED and not enforced — an import with holes is the
	// ordinary case, and the holes are already named in Missing.
	next, err := s.Validate(context.Background(), session.CompileOptions{})
	report.Compiles = err == nil
	if err != nil {
		report.Why = err.Error()
	}
	for _, n := range next {
		report.Deferred = append(report.Deferred, n.Validator+": "+n.Key)
	}
	sort.Strings(report.Deferred)

	// WHAT WAS WRITTEN IS READ OFF THE SESSION AND NOT OFF THE DESTINATION.
	// The session is the authority on what the project is; the destination is
	// wherever it was copied, and listing that instead would report whatever
	// else happened to be in the directory.
	if err := s.Save(dst, dir); err != nil {
		return Report{}, fmt.Errorf("emit: writing the project: %w", err)
	}
	report.Written = walk(s.FS())

	sort.Slice(report.Missing, func(i, j int) bool {
		a, b := report.Missing[i], report.Missing[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Field < b.Field
	})
	return report, nil
}

// identify writes the project's own document: its name, one environment every
// branch feeds, and the id the schema's generator mints for it.
func identify(s *session.Session, name string) error {
	// The root address is never empty: AdoptSession wires the layout that answers it.
	root := s.Root()

	if err := s.Set(root, []string{"name"}, name); err != nil {
		return fmt.Errorf("emit: naming the project: %w", err)
	}
	if err := s.Set(root, []string{"environments", "prod", "branches"}, []string{"*"}); err != nil {
		return fmt.Errorf("emit: declaring the project's environment: %w", err)
	}
	return mint(s, root, name)
}

// write puts one imported resource into the session and reports what it left
// empty.
func write(s *session.Session, kind, name string, doc map[string]any) ([]Missing, error) {
	at, err := s.Address(kind, name, "")
	if err != nil {
		return nil, fmt.Errorf("emit: addressing %s/%s: %w", kind, name, err)
	}
	if err := s.SetResource(at, doc); err != nil {
		return nil, fmt.Errorf("emit: writing %s/%s: %w", kind, name, err)
	}
	if err := mint(s, at, kind, name); err != nil {
		return nil, err
	}

	var out []Missing
	for _, issue := range s.ValidateResource(at) {
		out = append(out, Missing{
			Kind:  kind,
			Name:  name,
			Field: strings.Join(issue.Field, "/"),
			Why:   issue.Message,
		})
	}
	return out, nil
}

// mint asks the schema's own generator for an id.
//
// IT IS NOT AN ERROR FOR A KIND TO HAVE NONE. Whether a resource carries a
// generated id is the vocabulary's statement, and a kind that declares no such
// field is not a kind this package should be inventing one for.
func mint(s *session.Session, at []string, seed ...any) error {
	id, err := s.Generate(at, []string{"id"}, seed...)
	if err != nil {
		return nil
	}
	if err := s.Set(at, []string{"id"}, id); err != nil {
		return fmt.Errorf("emit: setting the id of %s: %w", strings.Join(at, "/"), err)
	}
	return nil
}

// walk lists the project's files, relative to its root, from the session's own
// in-memory filesystem, which cannot fail.
func walk(fs afero.Fs) []string {
	var out []string
	afero.Walk(fs, "/", func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		out = append(out, strings.TrimPrefix(path, "/"))
		return nil
	})
	sort.Strings(out)
	return out
}
