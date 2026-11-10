package source

import (
	"fmt"
	"io"

	"github.com/taubyte/tau/core/vm"
)

type source struct {
	loader vm.Loader
}

var _ vm.Source = &source{}

func New(loader vm.Loader) vm.Source {
	return &source{
		loader: loader,
	}
}

func (s *source) Module(ctx vm.Context, name string) (vm.SourceModule, error) {
	_reader, err := s.loader.Load(ctx, name)
	if err != nil {
		return nil, err // the loader already names the module
	}

	_source, err := io.ReadAll(_reader)
	_reader.Close()
	if err != nil {
		return nil, fmt.Errorf("reading module `%s`: %w", name, err)
	}

	return _source, nil
}
