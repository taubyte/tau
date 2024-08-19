package spin

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	helpers "github.com/taubyte/tau/pkg/vm/helpers/wazero"
)

type spin struct {
	ctx  context.Context
	ctxC context.CancelFunc

	lock sync.RWMutex

	source    []byte
	isRuntime bool

	containers map[string]*container

	registries []string

	module wazero.CompiledModule

	runtime wazero.Runtime
}

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

func ModuleOpen(path string) Option[Spin] {
	return func(si Spin) (err error) {
		s := si.(*spin)
		s.source, err = os.ReadFile(path)
		s.isRuntime = false
		return
	}
}

func Registry(r string) Option[Spin] {
	return func(si Spin) error {
		s := si.(*spin)
		s.registries = append(s.registries, r)
		return nil
	}
}

func Registries(registries ...string) Option[Spin] {
	return func(si Spin) error {
		si.(*spin).registries = registries
		return nil
	}
}

func New(ctx context.Context, options ...Option[Spin]) (Spin, error) {
	s := &spin{
		isRuntime:  true,
		containers: make(map[string]*container),
		registries: []string{"registry.hub.docker.com"},
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("option failed with %w", err)
		}
	}

	var err error

	if s.source == nil {
		s.source, err = RuntimeADM64()
		if err != nil {
			return nil, err
		}
	}

	s.ctx, s.ctxC = context.WithCancel(ctx)
	s.runtime = helpers.NewRuntime(ctx, nil)

	if _, err = wasi_snapshot_preview1.Instantiate(s.ctx, s.runtime); err != nil {
		return nil, fmt.Errorf("instantiating host module failed with: %w", err)
	}

	if s.module, err = s.runtime.CompileModule(s.ctx, s.source); err != nil {
		return nil, fmt.Errorf("compiling runtime failed with: %s", err)
	}

	return s, nil
}

func (s *spin) Close() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.ctxC()

	for _, cont := range s.containers {
		go cont.Stop()
	}
}
