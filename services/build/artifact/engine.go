package artifact

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/containers/core"
	"github.com/taubyte/tau/pkg/vault"
)

// engine.go runs a build in a container. The checkout is bind-mounted and the
// product read back off the host. Egress on a node is always restricted to the
// public internet (pkg/containers, fail-closed): a build reaching the node's
// register could deploy itself; only `tau build` uses OpenEgress. stdout and
// stderr stream to the job log in order.

// mount is where the checkout appears inside the build, fixed so no output can
// embed a host temp path.
const mount = "/src"

// shell runs the author's commands, joined with && so the first failure ends the
// build.
var shell = []string{"/bin/sh", "-c"}

// Containers is an Engine over pkg/containers.
type Containers struct {
	client *containers.Client
	open   bool
}

// EngineOption configures a Containers.
type EngineOption func(*Containers)

// OpenEgress is a build on a developer's own machine: the commands are theirs,
// and the restriction needs privileges a developer's docker lacks.
func OpenEgress() EngineOption {
	return func(c *Containers) { c.open = true }
}

var _ Engine = (*Containers)(nil)

// NewEngine builds one over backend; nil is the machine's default.
func NewEngine(backend core.Backend, opts ...EngineOption) (*Containers, error) {
	options := []containers.Option{}
	if backend != nil {
		options = append(options, containers.WithBackend(backend))
	}

	client, err := containers.New(options...)
	if err != nil {
		return nil, fmt.Errorf("artifact: %w", err)
	}
	c := &Containers{client: client}
	for _, apply := range opts {
		apply(c)
	}
	return c, nil
}

// Make runs one build, a step at a time, and returns what it produced.
//
// Every step has the vault at /.taubyte when the spec has one, and the log masks each
// value it serves from then on, across steps.
func (c *Containers) Make(ctx context.Context, spec Spec, log io.Writer) (io.ReadCloser, error) {
	var mounts []containers.ContainerOption
	flush := func() {}
	if spec.Vault != nil {
		redact := vault.NewRedactor(log)
		served, err := vault.Serve(spec.Vault, redact.Add)
		if err != nil {
			return nil, err
		}
		defer served.Close()
		log, flush = redact, func() { redact.Flush() }
		mounts = append(mounts, containers.ReadOnlyVolume(served.Dir(), vault.Dir))
	}

	for _, step := range spec.Steps {
		err := c.run(ctx, spec, step, mounts, log)
		// What the log holds back is a step's own last bytes, not the next step's.
		flush()
		if err != nil {
			return nil, fmt.Errorf("step %s: %w", step.Name, err)
		}
	}
	return product(joinWithin(spec.Dir, spec.Path), spec)
}

// run is one step: its image over the checkout, its commands in one shell. What it
// leaves in the checkout is what the next step finds.
func (c *Containers) run(ctx context.Context, spec Spec, step Step, mounts []containers.ContainerOption, log io.Writer) error {
	// Image pulls, and keeps an image the machine already holds when the pull
	// fails — a toolchain built on this machine, or a registry that is down.
	image, err := c.client.Image(ctx, step.Image, containers.Output(log))
	if err != nil {
		return err
	}

	options := []containers.ContainerOption{
		containers.Volume(spec.Dir, mount),
		containers.WorkDir(path.Join(mount, filepath.ToSlash(within(spec.Path)))),
		containers.Command(append(shell, strings.Join(step.Commands, " && "))),
	}
	options = append(options, mounts...)
	if !c.open {
		options = append(options, containers.RestrictedEgress())
	}
	if len(spec.Variables) > 0 {
		options = append(options, containers.Variables(spec.Variables))
	}

	container, err := image.Instantiate(ctx, options...)
	if err != nil {
		return err
	}

	// A log that stops taking bytes says nothing about whether the build
	// worked, so it does not fail the build: the output past that point is
	// dropped and the container runs to its own end.
	sink := &forgiving{w: log}
	return container.Stream(ctx, sink, sink)
}

// forgiving passes writes through until one fails, then swallows the rest.
type forgiving struct {
	w      io.Writer
	failed bool
}

func (f *forgiving) Write(p []byte) (int, error) {
	if !f.failed {
		if _, err := f.w.Write(p); err != nil {
			f.failed = true
		}
	}
	return len(p), nil
}

// collect reads what a build left behind: a file as itself, a directory as a
// tar.gz.
func collect(dir, output string) (io.ReadCloser, error) {
	path := joinWithin(dir, output)

	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		// THE MOST COMMON WAY A BUILD FAILS WITHOUT FAILING: the commands
		// succeeded and put the product somewhere else. Saying what was
		// expected is the whole of what makes that findable.
		return nil, fmt.Errorf("artifact: the build left nothing at %q", output)
	}
	if err != nil {
		return nil, fmt.Errorf("artifact: reading %q: %w", output, err)
	}

	if !info.IsDir() {
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("artifact: reading %q: %w", output, err)
		}
		return file, nil
	}
	return tarball(path)
}

// tarball streams a directory as a gzipped tar through a pipe, so a site is never
// on disk twice.
func tarball(root string) (io.ReadCloser, error) {
	reader, writer := io.Pipe()

	go func() {
		gz := gzip.NewWriter(writer)
		archive := tar.NewWriter(gz)

		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// A SYMLINK IS NOT FOLLOWED. A build controls what it writes, and
			// one that wrote a link to /etc into its output directory would
			// otherwise have this read that file into the artifact.
			if !info.Mode().IsRegular() && !info.IsDir() {
				return nil
			}

			name, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if name == "." {
				return nil
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name = filepath.ToSlash(name)
			if err := archive.WriteHeader(header); err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(archive, file)
			return err
		})

		if err == nil {
			err = archive.Close()
			if err == nil {
				err = gz.Close()
			}
		}
		writer.CloseWithError(err)
	}()

	return reader, nil
}
