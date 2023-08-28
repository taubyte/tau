package lookup

import (
	"errors"

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

	picks, err := service.Cache().Get(matcher, iface.GetOptions{Validation: true})
	if err == nil {
		return picks, nil
	}

	return service.CheckTns(matcher)
}
