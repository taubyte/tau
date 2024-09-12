package domainLib

import client "github.com/taubyte/tau/clients/http/auth"

type Validator interface {
	ValidateFQDN(fqdn string) (response client.DomainResponse, err error)
}
