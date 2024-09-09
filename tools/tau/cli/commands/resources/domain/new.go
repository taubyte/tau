package domain

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	domainFlags "github.com/taubyte/tau/tools/tau/flags/domain"
	domainI18n "github.com/taubyte/tau/tools/tau/i18n/domain"
	domainLib "github.com/taubyte/tau/tools/tau/lib/domain"
	domainPrompts "github.com/taubyte/tau/tools/tau/prompts/domain"
	domainTable "github.com/taubyte/tau/tools/tau/table/domain"
)

func (link) New() common.Command {
	return (&resources.New[*structureSpec.Domain]{
		PromptsNew:        domainPrompts.New,
		TableConfirm:      domainTable.Confirm,
		PromptsCreateThis: domainPrompts.CreateThis,
		I18nCreated:       domainI18n.Created,
		UniqueFlags: flags.Combine(
			domainFlags.Generated,
			domainFlags.GeneratedPrefix,
			domainFlags.FQDN,
			domainFlags.CertType,
			domainFlags.Certificate,
			domainFlags.Key,
		),

		// Wrapping method to handle registration
		LibNew: func(resource *structureSpec.Domain) error {
			validator, err := domainLib.New(resource)
			if err != nil {
				return err
			}

			// Skipping registration check for generated FQDN
			isGeneratedFqdn, err := domainLib.IsAGeneratedFQDN(resource.Fqdn)
			if err != nil {
				return err
			}
			if isGeneratedFqdn {
				return nil
			}

			// Validate the fqdn provided
			clientResponse, err := validator.ValidateFQDN(resource.Fqdn)
			if err != nil {
				return err
			}

			domainTable.Registered(resource.Fqdn, clientResponse)
			return nil
		},
	}).Default()
}
