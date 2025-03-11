package options

import (
	"crypto/tls"

	"github.com/taubyte/tau/pkg/http/options"
)

type OptionChecker struct {
	Checker func(hello *tls.ClientHelloInfo) bool
}

func CustomDomainChecker(checker func(hello *tls.ClientHelloInfo) bool) options.Option {
	return func(s options.Configurable) error {
		return s.SetOption(OptionChecker{Checker: checker})
	}
}
