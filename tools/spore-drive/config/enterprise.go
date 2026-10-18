package config

import (
	"errors"

	"gopkg.in/yaml.v3"
)

// enterprise config lives in its own document (enterprise.yaml), keyed by
// service name. spore-drive stores it opaquely — it knows nothing about any
// service's schema; the ee client builds the paths and the service decodes its
// own config. The mechanism is generic (no service schema), so it lives in the
// community build; only the ee client/handler exercise it.

// SetEnterprisePath sets enterprise.<service>.<path...> = value.
func SetEnterprisePath(p Parser, service string, path []string, value any) error {
	pp, ok := p.(*parser)
	if !ok {
		return errors.New("unexpected parser type")
	}
	q := pp.Get("enterprise").Document().Get(service)
	for _, k := range path {
		q = q.Get(k)
	}
	return q.Set(value).Commit()
}

// EnterpriseServices returns the whole enterprise config keyed by service, as
// yaml nodes, for the deploy emit (Source.Enterprise). Empty when unset.
func EnterpriseServices(p Parser) map[string]yaml.Node {
	pp, ok := p.(*parser)
	if !ok {
		return nil
	}
	var m map[string]yaml.Node
	pp.Get("enterprise").Document().Value(&m)
	return m
}
