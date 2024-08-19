package spin

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"archive/tar"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/spf13/afero"
	"github.com/spf13/afero/tarfs"
	"github.com/spf13/afero/zipfs"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/sys"

	crand "crypto/rand"

	"github.com/moby/moby/pkg/namesgenerator"
)

type mountPoint string
type mountSource string

type container struct {
	ctx  context.Context
	ctxC context.CancelFunc

	parent *spin

	name string
	cmd  []string

	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer

	closers chan io.Closer

	done chan struct{}

	mounts map[mountPoint]mountSource

	bundle string

	module api.Module
}

func Name(name string) Option[Container] {
	return func(c Container) error {
		c.(*container).name = name
		return nil
	}
}

func Command(cmd ...string) Option[Container] {
	return func(c Container) error {
		c.(*container).cmd = cmd
		return nil
	}
}

func Mount(hostDir, wasmDir string) Option[Container] {
	return func(c Container) error {
		if !path.IsAbs(hostDir) {
			return errors.New("mount host directory must be absolute")
		}
		if !path.IsAbs(wasmDir) {
			return errors.New("mount directory must be absolute")
		}
		c.(*container).mounts[mountPoint(wasmDir)] = mountSource(hostDir)
		return nil
	}
}

func Bundle(path string) Option[Container] {
	return func(ci Container) error {
		c := ci.(*container)
		if !c.parent.isRuntime {
			return errors.New("only runtimes can use bundles")
		}
		c.bundle = path
		return nil
	}
}

func Stdin(r io.Reader) Option[Container] {
	return func(ci Container) error {
		ci.(*container).stdin = r
		return nil
	}
}

func Stdout(w io.Writer) Option[Container] {
	return func(ci Container) error {
		ci.(*container).stdout = w
		return nil
	}
}

func Stderr(w io.Writer) Option[Container] {
	return func(ci Container) error {
		ci.(*container).stderr = w
		return nil
	}
}

func (s *spin) New(options ...Option[Container]) (Container, error) {
	c := &container{
		parent:  s,
		mounts:  make(map[mountPoint]mountSource),
		closers: make(chan io.Closer, 128),
		stdin:   NoStdin, // images need an stdin to boot
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		done:    make(chan struct{}, 1),
	}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if err := s.init(c); err != nil {
		return nil, err
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	s.containers[c.name] = c

	return c, nil
}

func (s *spin) init(c *container) (err error) {
	s.lock.RLock()
	defer s.lock.RUnlock()

	if c.name == "" {
		c.name = namesgenerator.GetRandomName(0)
		// try one more time
		if _, exists := s.containers[c.name]; exists {
			c.name = namesgenerator.GetRandomName(1)
		}
	}

	if _, exists := s.containers[c.name]; exists {
		return fmt.Errorf("container `%s` alreay exists", c.name)
	}

	c.ctx, c.ctxC = context.WithCancel(s.ctx)

	fsConfig := wazero.NewFSConfig()
	for wasmDir, hostDir := range c.mounts {
		st, err := os.Stat(string(hostDir))
		if err != nil {
			return fmt.Errorf("looking up %s failed with %w", hostDir, err)
		}

		if st.IsDir() {
			fsConfig = fsConfig.WithDirMount(string(hostDir), string(wasmDir))
		} else {
			t, err := filetype.MatchFile(string(hostDir))
			if err != nil {
				return err
			}

			switch t {
			case matchers.TypeZip:
				zipReader, err := zip.OpenReader(string(hostDir))
				if err != nil {
					return err
				}
				c.closers <- zipReader

				fsConfig = fsConfig.WithFSMount(afero.NewIOFS(zipfs.New(&zipReader.Reader)), string(wasmDir))
			case matchers.TypeTar:
				tf, err := os.Open(string(hostDir))
				if err != nil {
					return err
				}
				c.closers <- tf

				fsConfig = fsConfig.WithFSMount(afero.NewIOFS(tarfs.New(tar.NewReader(tf))), string(wasmDir))
			default:
				return errors.New("unsupported file format for mount")
			}
		}
	}

	if s.isRuntime && c.bundle != "" {
		zipReader, err := zip.OpenReader(c.bundle)
		if err != nil {
			return err
		}
		c.closers <- zipReader

		fsConfig = fsConfig.WithFSMount(afero.NewIOFS(zipfs.New(&zipReader.Reader)), "/ext/bundle")
	}

	config := wazero.
		NewModuleConfig().
		WithStdin(c.stdin).
		WithStdout(c.stdout).
		WithStderr(c.stderr).
		WithName(c.name).
		WithFSConfig(fsConfig).
		WithArgs(append([]string{"module"}, c.cmd...)...).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(crand.Reader).
		WithStartFunctions() // don't start yet

	if c.module, err = s.runtime.InstantiateModule(c.ctx, s.module, config); err != nil {
		return fmt.Errorf("instantiate module %s failed with %w", c.name, err)
	}

	return nil
}

func (c *container) Run() error {
	defer func() {
		c.done <- struct{}{}

	}()

	main := c.module.ExportedFunction("_start")
	if main == nil {
		return errors.New("no main")
	}

	if _, err := main.Call(c.ctx); err != nil {
		if se, ok := err.(*sys.ExitError); ok {
			if se.ExitCode() == 0 { // Don't err on success.
				err = nil
			}
			return err
		} else {
			return fmt.Errorf("executing %s failed with %w", main.Definition().Name(), err)
		}

	}

	return nil
}

func (s *spin) remove(c *container) {
	s.lock.Lock()
	defer s.lock.Unlock()

	delete(s.containers, c.name)
}

func (c *container) Stop() {
	c.ctxC()
	<-c.done // wait for it to stop

	close(c.closers)
	for closer := range c.closers {
		closer.Close()
	}

	c.parent.remove(c)
}
