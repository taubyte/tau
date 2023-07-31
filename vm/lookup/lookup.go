package lookup

import (
	"errors"
	"fmt"

	iface "github.com/taubyte/go-interfaces/services/substrate/components"
)

// Lookup returns the list of serviceables retrieved from cache or tns
//
// Cached serviceables are validated for based on the service requirements, and config commit value
// Serviceables returned by the tns lookup are looked up based on the service requirements, then
// instantiated based on serviceable type
func Lookup(service iface.ServiceComponent, matcher iface.MatchDefinition) ([]iface.Serviceable, error) {
	if service == nil {
		return nil, errors.New("no service provided")
	}
	if matcher == nil {
		return nil, errors.New("no matcher provided")
	}

	picks, err := service.Cache().Get(matcher)
	if err == nil {
		if err = validate(picks, service); err == nil {
			return picks, nil
		}
	}

	return service.CheckTns(matcher)
}

func validate(serviceables []iface.Serviceable, service iface.ServiceComponent) error {
	project, err := serviceables[0].Project()
	if err != nil {
		return fmt.Errorf("validating cached pick project id failed with: %s", err)
	}

	commit, err := service.Tns().Simple().Commit(project.String(), service.Branch())
	if err != nil {
		return err
	}

	for _, serviceable := range serviceables {
		if serviceable.Commit() != commit {
			return fmt.Errorf("cached pick commit `%s` is outdated, latest commit is `%s`", serviceable.Commit(), commit)
		}
	}

	return nil
}
