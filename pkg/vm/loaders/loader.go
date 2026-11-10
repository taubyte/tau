package loaders

import (
	"errors"
	"fmt"
	"io"

	"github.com/taubyte/tau/core/vm"
)

type loader struct {
	backends []vm.Backend
	resolver vm.Resolver
}

var _ vm.Loader = &loader{}

func New(resolver vm.Resolver, backends ...vm.Backend) vm.Loader {
	return &loader{
		backends: backends,
		resolver: resolver,
	}
}

func (l *loader) Load(ctx vm.Context, module string) (io.ReadCloser, error) {
	uri, err := l.resolver.Lookup(ctx, module)
	if err != nil {
		return nil, fmt.Errorf("resolving module %s @ %s: %w", module, ctx.Project(), err)
	}

	if len(l.backends) == 0 {
		return nil, fmt.Errorf("fetching module %s @ %s failed with no backend found", module, ctx.Project())
	}

	var errs []error
	for _, backend := range l.backends {
		reader, err := backend.Get(uri)
		if err == nil && reader != nil {
			return reader, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}

	return nil, fmt.Errorf("fetching module %s @ %s from %s failed: %w", module, ctx.Project(), uri, errors.Join(errs...))
}
