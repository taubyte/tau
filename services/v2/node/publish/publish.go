// Package publish compiles a config repository and puts it in the register.
//
// # The step nothing else does
//
// tcc compiles; the register holds; a worker reads. Nothing joined them, so a
// project could be written, imported, validated and served by a fleet that had
// never been told about it. This is that join, and it is one call:
//
//	compile the directory
//	  -> work out the names it answers on
//	    -> publish both under the next revision
//
// # A revision is derived, not typed
//
// The register refuses a revision that does not move forward, which is what
// makes a late retry harmless and a rollback a forward operation. That is a
// property worth having and not a number anybody should be tracking, so this
// reads what is published and adds one. Somebody who wants to say which
// revision still can.
//
// # Publishing the same thing twice changes nothing
//
// A compiled project is content-addressed, so a republish of an unchanged
// directory produces the same object id. Bumping the revision for it would
// churn every reader's pointer to say the same thing — so it is reported as
// unchanged and the pointer is left alone.
package publish

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/afero"

	"github.com/taubyte/tau/pkg/project"
	pkgregister "github.com/taubyte/tau/pkg/register"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// Register is the slice of the register a publish needs: read what is there,
// then write.
//
// Both *pkgregister.Register and *services/register.Client satisfy it, so
// publishing to a node's own register and to one across the swarm are the same
// code.
type Register interface {
	Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error)
	Publish(ctx context.Context, p pkgregister.Publication) (pkgregister.ObjectID, error)
}

// Options are what a publish needs beyond the directory.
type Options struct {
	// Project and Environment address what is being published. Required.
	Project     string
	Environment string

	// Branch is the git branch the compile reads. Empty means the compiler's
	// default.
	Branch string

	// CloudFqdn is the zone this cloud serves, and it completes the names a
	// project cannot know: a generated host is `<label>.<project label>` in
	// the config and this is the rest.
	//
	// Empty publishes only the names whose zones the project itself declares,
	// which is right for a project that owns its domains and wrong for one
	// relying on the cloud's — so a generated host with no fqdn to complete it
	// is reported rather than dropped.
	CloudFqdn string

	// Revision, when non-zero, is the revision to publish under. Zero derives
	// it: one more than what is there.
	Revision uint64
}

// Result is what a publish did.
type Result struct {
	Object   pkgregister.ObjectID
	Revision uint64

	// Hosts are the names this environment now answers on.
	Hosts []string

	// Incomplete are generated names that could not be completed because no
	// cloud fqdn was given. They are NOT published: a partial name in a
	// routing table is a name no request will ever carry.
	Incomplete []string

	// Unchanged says the directory compiled to what is already published, so
	// nothing moved.
	Unchanged bool
}

var (
	ErrNoProject     = errors.New("publish: a publish needs a project and an environment")
	ErrNoRegister    = errors.New("publish: nowhere to publish to")
	ErrNothingToRead = errors.New("publish: no configuration in that directory")
)

// Dir compiles a config directory and publishes it.
func Dir(ctx context.Context, r Register, fs afero.Fs, dir string, o Options) (Result, error) {
	compiled, p, err := Compile(fs, dir, o.Branch)
	if err != nil {
		return Result{}, err
	}
	return Object(ctx, r, compiled, p, o)
}

// Compile reads a config directory and returns what the register would hold,
// plus a reader over it.
//
// THE BYTES ARE JSON OF THE COMPILED OBJECT, which is what pkg/project reads
// back. Saying so in one place is what keeps the writer and the reader from
// disagreeing about the format nobody else names.
func Compile(fs afero.Fs, dir, branch string) ([]byte, *project.Project, error) {
	options := []schema.Option{schema.WithVirtual(fs, dir)}
	if branch != "" {
		options = append(options, schema.WithBranch(branch))
	}

	c, err := schema.New(options...)
	if err != nil {
		return nil, nil, fmt.Errorf("publish: reading %s: %w", dir, err)
	}

	obj, _, err := c.Compile(context.Background())
	if err != nil {
		return nil, nil, fmt.Errorf("publish: compiling %s: %w", dir, err)
	}

	compiled, err := json.Marshal(obj.Flat())
	if err != nil {
		return nil, nil, fmt.Errorf("publish: %w", err)
	}
	if len(compiled) == 0 {
		return nil, nil, ErrNothingToRead
	}

	p, err := project.Load(compiled)
	if err != nil {
		return nil, nil, fmt.Errorf("publish: reading back what was compiled: %w", err)
	}
	return compiled, p, nil
}

// Object publishes an already-compiled project.
func Object(ctx context.Context, r Register, compiled []byte, p *project.Project, o Options) (Result, error) {
	if o.Project == "" || o.Environment == "" {
		return Result{}, ErrNoProject
	}
	if r == nil {
		return Result{}, ErrNoRegister
	}

	hosts, incomplete := Hosts(p, o.CloudFqdn)

	// WHAT IS ALREADY THERE DECIDES THE REVISION, and whether there is anything
	// to do at all.
	revision := o.Revision
	current, err := r.Resolve(ctx, o.Project, o.Environment)
	switch {
	case err == nil:
		if current.Object == pkgregister.IDOf(compiled) && o.Revision == 0 {
			return Result{
				Object: current.Object, Revision: current.Revision,
				Hosts: hosts, Incomplete: incomplete, Unchanged: true,
			}, nil
		}
		if revision == 0 {
			revision = current.Revision + 1
		}
	case errors.Is(err, pkgregister.ErrNotPublished):
		if revision == 0 {
			revision = 1
		}
	default:
		return Result{}, fmt.Errorf("publish: reading what %s/%s runs: %w", o.Project, o.Environment, err)
	}

	id, err := r.Publish(ctx, pkgregister.Publication{
		Project:     o.Project,
		Environment: o.Environment,
		Revision:    revision,
		Compiled:    compiled,
		Hosts:       hosts,
	})
	if err != nil {
		return Result{}, err
	}

	return Result{Object: id, Revision: revision, Hosts: hosts, Incomplete: incomplete}, nil
}

// Hosts are the fully-qualified names a project answers on, and the generated
// ones that could not be completed.
//
// THE CLOUD'S HALF IS NOT IN THE CONFIG and never will be: a generated host is
// `<label>.<project label>` and the rest belongs to whoever runs the cloud. So
// completing them is this step's job — it is the first place that knows both —
// and one that cannot be completed is named rather than published half-formed.
func Hosts(p *project.Project, cloudFqdn string) (hosts, incomplete []string) {
	for _, h := range p.Hosts() {
		if !h.Generated {
			hosts = append(hosts, h.Fqdn)
			continue
		}
		if cloudFqdn == "" {
			incomplete = append(incomplete, h.Fqdn)
			continue
		}
		hosts = append(hosts, h.Fqdn+"."+cloudFqdn)
	}
	return hosts, incomplete
}
