package spin

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	helpers "github.com/taubyte/tau/pkg/vm/helpers/wazero"

	crand "crypto/rand"

	"github.com/moby/moby/pkg/namesgenerator"
)

type Spin interface {
	New(options ...Option[Container]) (Container, error)
}

type spin struct {
	ctx  context.Context
	ctxC context.CancelFunc

	lock sync.RWMutex

	source    []byte
	isRuntime bool

	containers map[string]*container

	module wazero.CompiledModule

	runtime wazero.Runtime
}

type Container interface {
	Run() error
	Stop()
}

type container struct {
	ctx  context.Context
	ctxC context.CancelFunc

	parent *spin

	name string
	cmd  []string

	bundle string

	module api.Module
}

type Option[T any] func(T) error

type AMD64 struct{}
type RISCV64 struct{}

func Runtime[arch AMD64 | RISCV64]() Option[Spin] {
	return func(si Spin) (err error) {
		s := si.(*spin)
		switch any(arch{}).(type) {
		case AMD64:
			s.source, err = RuntimeADM64()
		case RISCV64:
			s.source, err = RuntimeRISCV64()
		}
		s.isRuntime = true

		return
	}
}

func Module(source []byte) Option[Spin] {
	return func(si Spin) (err error) {
		s := si.(*spin)
		s.source = source
		s.isRuntime = false
		return
	}
}

func New(ctx context.Context, options ...Option[Spin]) (Spin, error) {
	defaultRuntime, err := RuntimeADM64()
	if err != nil {
		return nil, err
	}

	s := &spin{
		source:     defaultRuntime,
		containers: make(map[string]*container),
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("option failed with %w", err)
		}
	}

	s.ctx, s.ctxC = context.WithCancel(ctx)
	s.runtime = helpers.NewRuntime(ctx, nil)

	if _, err = wasi_snapshot_preview1.NewBuilder(s.runtime).Instantiate(s.ctx); err != nil {
		return nil, fmt.Errorf("instantiating host module failed with: %w", err)
	}

	if s.module, err = s.runtime.CompileModule(s.ctx, s.source); err != nil {
		return nil, fmt.Errorf("compiling runtime failed with: %s", err)
	}

	return s, nil
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

func (s *spin) New(options ...Option[Container]) (Container, error) {
	c := &container{parent: s}

	for _, opt := range options {
		if err := opt(c); err != nil {
			return nil, err
		}
	}

	if err := c.init(); err != nil {
		return nil, err
	}

	s.lock.Lock()
	defer s.lock.Unlock()
	s.containers[c.name] = c

	return c, nil
}

func (c *container) init() (err error) {
	c.parent.lock.RLock()
	defer c.parent.lock.RUnlock()

	if c.name == "" {
		c.name = namesgenerator.GetRandomName(0)
		// try one more time
		if _, exists := c.parent.containers[c.name]; exists {
			c.name = namesgenerator.GetRandomName(1)
		}
	}

	if _, exists := c.parent.containers[c.name]; exists {
		return fmt.Errorf("container `%s` alreay exists", c.name)
	}

	c.ctx, c.ctxC = context.WithCancel(c.parent.ctx)

	args := make([]string, 0, len(c.cmd)+2)
	if c.bundle != "" {
		args = append(args, "--external-bundle", c.bundle)
	}
	args = append(args, c.cmd...)

	config := wazero.
		NewModuleConfig().
		// WithFS(afero.NewIOFS(r.instance.fs)).
		// WithStdout(r.instance.output).
		// WithStderr(r.instance.outputErr).
		WithName(c.name).
		WithArgs(args...).
		WithSysWalltime().
		WithSysNanotime().
		WithSysNanosleep().
		WithRandSource(crand.Reader).
		WithStartFunctions() // don't start yet

	if c.module, err = c.parent.runtime.InstantiateModule(c.ctx, c.parent.module, config); err != nil {
		return fmt.Errorf("instantiate module %s failed with %w", c.name, err)
	}

	return nil
}

func (c *container) Run() error {
	_, err := c.module.ExportedFunction("_start").Call(c.ctx)
	return err
}

func (c *container) Stop() {
	c.ctxC()
}
