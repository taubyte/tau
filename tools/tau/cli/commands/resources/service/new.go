package service

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	resources "github.com/taubyte/tau/tools/tau/cli/commands/resources/common"
	"github.com/taubyte/tau/tools/tau/cli/common"
	serviceFlags "github.com/taubyte/tau/tools/tau/flags/service"
	serviceI18n "github.com/taubyte/tau/tools/tau/i18n/service"
	serviceLib "github.com/taubyte/tau/tools/tau/lib/service"
	servicePrompts "github.com/taubyte/tau/tools/tau/prompts/service"
	serviceTable "github.com/taubyte/tau/tools/tau/table/service"
	"github.com/urfave/cli/v2"
)

func (link) New() common.Command {
	return (&resources.New[*structureSpec.Service]{
		PromptsNew:        servicePrompts.New,
		TableConfirm:      serviceTable.Confirm,
		PromptsCreateThis: servicePrompts.CreateThis,
		LibNew:            serviceLib.New,
		I18nCreated:       serviceI18n.Created,

		UniqueFlags: []cli.Flag{
			serviceFlags.Protocol,
		},
	}).Default()
}
