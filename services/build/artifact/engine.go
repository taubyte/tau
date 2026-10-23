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
)

// engine.go runs a build in a container.
//
// # The source is bind-mounted and the product is read back off the host
//
// A build writes into the checkout, so what it produced is on this machine when
// the container exits and there is nothing to copy out of a stopped container.
// That is also why Checkout hands back a directory rather than a filesystem.
//
// # Egress is restricted on a node, always
//
// A build runs a stranger's commands — `cargo build` fetches from crates.io and
// runs whatever build.rs it downloaded — so it gets the public internet and
// nothing else: no node-local services, no cloud metadata endpoint, no
// private or link-local ranges, enforced fail-closed by pkg/containers. A node
// cannot turn it off: a build that could reach the node's own register is a
// build that can deploy itself. Only `tau build`, on the developer's own
// machine, runs with OpenEgress.
//
// # What the log does, and does not, do
//
// The container's output arrives when the build ENDS, because that is what
// pkg/containers offers — Run starts, waits and then hands over the logs. A
// build being watched live therefore goes quiet for the length of the build and
// then prints all of it. Closing that means streaming logs while a container
// runs, which is a change in pkg/containers rather than here.

// mount is where the checkout appears inside the build.
//
// IT IS A FIXED PATH AND NOT THE HOST'S. The host path is a temporary directory
// whose name nothing should depend on, and a build that saw it could write a
// path into its own output that means nothing anywhere else.
const mount = "/src"

// shell runs the author's commands. They are joined with && so the first
// failure ends the build, which is what anybody writing two lines of a build
// expects and what a container's exit code then reports.
var shell = []string{"/bin/sh", "-c"}

// Containers is an Engine over pkg/containers.
type Containers struct {
	client *containers.Client
	open   bool
}

// EngineOption configures a Containers.
type EngineOption func(*Containers)

// OpenEgress is a build on a developer's own machine: the toolchain reaches
// what that machine reaches. The restriction protects a node's own services
// and its peers from a stranger's commands; on a developer's machine the
// commands are the developer's, run as they would run them by hand, and the
// restriction needs privileges a developer's docker does not have.
func OpenEgress() EngineOption {
	return func(c *Containers) { c.open = true }
}

var _ Engine = (*Containers)(nil)

// NewEngine builds one over a backend the caller already chose.
//
// A NIL BACKEND IS THE MACHINE'S DEFAULT, which is what a development cloud
// wants; a node whose operator named one passes it, so a build runs on the same
// daemon its workloads do.
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

// Make runs one build and returns what it produced.
func (c *Containers) Make(ctx context.Context, spec Spec, log io.Writer) (io.ReadCloser, error) {
	// Image pulls, and keeps an image the machine already holds when the pull
	// fails — a toolchain built on this machine, or a registry that is down.
	image, err := c.client.Image(ctx, spec.Image, containers.Output(log))
	if err != nil {
		return nil, err
	}

	options := []containers.ContainerOption{
		containers.Volume(spec.Dir, mount),
		containers.WorkDir(path.Join(mount, filepath.ToSlash(within(spec.Path)))),
		containers.Command(append(shell, strings.Join(spec.Commands, " && "))),
	}
	if !c.open {
		options = append(options, containers.RestrictedEgress())
	}
	if env := spec.environment(); len(env) > 0 {
		options = append(options, containers.Variables(env))
	}

	container, err := image.Instantiate(ctx, options...)
	if err != nil {
		return nil, err
	}

	output, err := container.Run(ctx)
	// THE BUILD'S OWN OUTPUT IS THE JOB'S LOG, stdout and stderr together —
	// which is what somebody reading a build wants, in the order the build
	// wrote them. Run hands the output over with an exit-code error too, and
	// a failed build's output is the part worth reading, so it is copied
	// before the error is returned. A copy that fails is not checked: it
	// says nothing about whether the build did.
	if output != nil {
		combined := output.Combined()
		io.Copy(log, combined)
		combined.Close()
		output.Close()
	}
	if err != nil {
		return nil, err
	}

	return collect(joinWithin(spec.Dir, spec.Path), spec.Output)
}

// environment is what the build sees: its variables and its secrets, alike.
func (s Spec) environment() map[string]string {
	if len(s.Variables)+len(s.Secrets) == 0 {
		return nil
	}
	out := make(map[string]string, len(s.Variables)+len(s.Secrets))
	for k, v := range s.Variables {
		out[k] = v
	}
	for k, v := range s.Secrets {
		out[k] = v
	}
	return out
}

// collect reads what a build left behind.
//
// A FILE IS ITSELF AND A DIRECTORY IS A TAR.GZ. A wasm component is one file
// and a website is a tree, and the vocabulary lets an author name either — so
// the artifact's shape follows what was declared rather than forcing a website
// to be zipped by hand into a file the config would then have to name.
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

// tarball streams a directory as a gzipped tar.
//
// IT IS A PIPE AND NOT A TEMPORARY FILE, because the caller hands what comes
// back straight to a content-addressed store that reads to EOF — so a site's
// worth of files never has to exist twice on this disk.
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
