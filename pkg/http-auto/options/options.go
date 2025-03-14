package options

import (
	"github.com/taubyte/tau/pkg/http/options"
)

type OptionChecker struct {
	Checker func(host string) bool
}

func CustomDomainChecker(checker func(host string) bool) options.Option {
	return func(s options.Configurable) error {
		return s.SetOption(OptionChecker{Checker: checker})
	}
}
