package node

import (
	"fmt"
	"regexp"

	commonIface "github.com/taubyte/go-interfaces/services/common"
	domainSpecs "github.com/taubyte/go-specs/domain"
)

func setNetworkDomains(conf *commonIface.GenericConfig) {
	domainSpecs.WhiteListedDomains = conf.Domains.Whitelisted.Postfix
	domainSpecs.TaubyteServiceDomain = regexp.MustCompile(conf.Domains.Services)
	domainSpecs.SpecialDomain = regexp.MustCompile(conf.Domains.Generated)
	domainSpecs.TaubyteHooksDomain = regexp.MustCompile(fmt.Sprintf(`https://patrick.tau.%s`, conf.NetworkUrl))
}
