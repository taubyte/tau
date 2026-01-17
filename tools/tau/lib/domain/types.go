package domainLib

import (
	"github.com/taubyte/tau/core/services/auth"
)

type Validator interface {
	ValidateFQDN(fqdn string) (response auth.DomainRegistration, err error)
}
