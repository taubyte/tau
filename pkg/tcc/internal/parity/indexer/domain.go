package indexer

import (
	"errors"
	"fmt"

	projectSchema "github.com/taubyte/tau/pkg/schema/project"
	domainSpec "github.com/taubyte/tau/pkg/specs/domain"
	"github.com/taubyte/tau/utils/maps"
)

func Domains(ctx *IndexContext, project projectSchema.Project, urlIndex map[string]interface{}) error {
	if urlIndex == nil {
		return errors.New("urlIndex received is nil")
	}

	if ctx.Obj == nil {
		return errors.New("obj received is nil")
	}

	domObj, ok := ctx.Obj[string(domainSpec.PathVariable)]
	if !ok {
		return nil // This shouldn't be breaking,  it just means there are no domains
	}

	for _, domain := range maps.SafeInterfaceToStringKeys(domObj) {
		name, err := maps.String(maps.SafeInterfaceToStringKeys(domain), "name")
		if err != nil {
			return err
		}

		dom, err := project.Domain(name, ctx.AppName)
		if err != nil {
			return err
		}

		if len(dom.Get().Id()) == 0 {
			return fmt.Errorf("domain `%s` not found", dom.Get().Name())
		}

		fqdn := dom.Get().FQDN()
		err = ctx.validateDomain(fqdn)
		if err != nil {
			return fmt.Errorf("domain `%s` has invalid fqdn `%s`: %v", dom.Get().Name(), fqdn, err)
		}

		indexPath, err := domainSpec.Tns().BasicPath(fqdn)
		if err != nil {
			return err
		}

		// Might want to add Index Value to Index Value?
		// No need for it now, but maybe for later

		urlIndex[indexPath.Versioning().Links().String()] = nil
	}

	return nil
}
