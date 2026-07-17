package indexer

import (
	_ "embed"

	dv "github.com/taubyte/domain-validation"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	"golang.org/x/exp/slices"
)

var (
	//go:embed domain_public.key
	domainValPublicKeyData []byte
)

func (ctx *IndexContext) validateDomain(fqdn string) error {
	ctx.validDomainsLock.Lock()
	defer ctx.validDomainsLock.Unlock()

	if ctx.ValidDomains == nil {
		ctx.ValidDomains = []string{}
	}

	if slices.Contains(ctx.ValidDomains, fqdn) {
		return nil
	}

	var err error
	if ctx.Dev {
		err = domainSpec.ValidateDNS(ctx.GeneratedDomainRegExp, ctx.ProjectId, fqdn, ctx.Dev, dv.PublicKey(domainValPublicKeyData))
	} else {
		err = domainSpec.ValidateDNS(ctx.GeneratedDomainRegExp, ctx.ProjectId, fqdn, ctx.Dev, dv.PublicKey(ctx.DVPublicKey))
	}
	if err != nil {
		return err
	}

	ctx.ValidDomains = append(ctx.ValidDomains, fqdn)
	return nil
}
