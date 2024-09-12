package functionTable

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Confirm(ctx *cli.Context, function *structureSpec.Function, prompt string) bool {
	return prompts.ConfirmData(ctx, prompt, getTableData(function, false))
}
