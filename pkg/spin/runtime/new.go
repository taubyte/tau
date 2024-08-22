package runtime

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"

	helpers "github.com/taubyte/tau/pkg/vm/helpers/wazero"

	"github.com/taubyte/tau/pkg/spin/archive"
	"github.com/taubyte/tau/pkg/spin/embed"

	//lint:ignore ST1001 ignore
	. "github.com/taubyte/tau/pkg/spin"
)

type spin struct {
	ctx  context.Context
	ctxC context.CancelFunc

	lock sync.RWMutex

	registry Registry

	source    []byte
	isRuntime bool

	containers map[string]*container

	module wazero.CompiledModule

	runtime wazero.Runtime
}

type AMD64 struct{}
type RISCV64 struct{}

func Runtime[arch AMD64 | RISCV64](registry Registry) Option[Spin] {
	return func(si Spin) (err error) {
		s := si.(*spin)
		switch any(arch{}).(type) {
		case AMD64:
			s.source, err = embed.RuntimeADM64()
		case RISCV64:
			s.source, err = embed.RuntimeRISCV64()
		}
		s.registry = registry
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

func ModuleZip(path string, filename string) Option[Spin] {
	return func(si Spin) (err error) {
		zipSource, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("opening archive file failed with %w", err)
		}

		ar, err := archive.New(zipSource)
		if err != nil {
			return fmt.Errorf("parsing archive file failed with %w", err)
		}

		s := si.(*spin)
		s.source, err = ar.Module(filename)
		if err != nil {
			return fmt.Errorf("extracting module from archive failed with %w. Available %v", err, ar.List())
		}

		s.isRuntime = false

		return
	}
}

func New(ctx context.Context, options ...Option[Spin]) (Spin, error) {
	s := &spin{
		isRuntime:  true,
		containers: make(map[string]*container),
	}

	for _, opt := range options {
		if err := opt(s); err != nil {
			return nil, fmt.Errorf("option failed with %w", err)
		}
	}

	var err error

	if s.source == nil {
		s.source, err = embed.RuntimeADM64()
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
