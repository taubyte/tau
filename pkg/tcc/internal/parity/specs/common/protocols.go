package common

import (
	"fmt"

	"golang.org/x/exp/slices"
)

type ValidateOption func(string) error

func ValidateServices(services []string, ops ...ValidateOption) error {
	for _, service := range services {
		if !slices.Contains(Services, service) {
			return fmt.Errorf("`%s` is not a valid service", service)
		}

		for _, op := range ops {
			if err := op(service); err != nil {
				return err
			}
		}
	}

	return nil
}

func ValidateHttp() ValidateOption {
	return func(service string) error {
		if !slices.Contains(HTTPServices, service) {
			return fmt.Errorf("`%s` is not a http service", service)
		}
		return nil
	}
}

func ValidateP2P() ValidateOption {
	return func(service string) error {
		if !slices.Contains(P2PStreamServices, service) {
			return fmt.Errorf("`%s` is not a p2p stream service", service)
		}
		return nil
	}
}
