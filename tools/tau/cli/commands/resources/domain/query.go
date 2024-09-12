package domain

import (
	"fmt"

	"github.com/pterm/pterm"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	domainI18n "github.com/taubyte/tau/tools/tau/i18n/domain"
	domainLib "github.com/taubyte/tau/tools/tau/lib/domain"
	domainPrompts "github.com/taubyte/tau/tools/tau/prompts/domain"
	domainTable "github.com/taubyte/tau/tools/tau/table/domain"
)

func (link) Query() common.Command {
	return (&resources.Query[*structureSpec.Domain]{
		LibListResources:   domainLib.ListResources,
		TableList:          domainTable.List,
		PromptsGetOrSelect: domainPrompts.GetOrSelect,

		// Wrapping TableQuery to display registration information
		TableQuery: func(domain *structureSpec.Domain) {
			domainTable.Query(domain)

			validator, err := domainLib.NewValidator(domain.Name)
			if err != nil {
				pterm.Warning.Println(domainI18n.NewDomainValidatorFailed(domain.Name, err).Error())
				return
			}

			isGeneratedFqdn, err := domainLib.IsAGeneratedFQDN(domain.Fqdn)
			if err != nil {
				pterm.Error.Printfln(domainI18n.IsGeneratedFQDNFailed(domain.Fqdn, err).Error())
				return
			}
			if isGeneratedFqdn {
				return
			}

			clientResponse, err := validator.ValidateFQDN(domain.Fqdn)
			if err != nil {
				pterm.Warning.Println(domainI18n.ValidateFQDNFailed(domain.Fqdn, err).Error())
				return
			}
			// Display the register table without showing the `add to dns entry help`
			fmt.Println(domainTable.GetRegisterTable(clientResponse))
		},
	}).Default()
}

func (link) List() common.Command {
	return (&resources.List[*structureSpec.Domain]{
		LibListResources: domainLib.ListResources,
		TableList:        domainTable.List,
	}).Default()
}
