package applicationTable

import (
	"strings"

	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/prompts"
	"github.com/urfave/cli/v2"
)

func Confirm(ctx *cli.Context, app *structureSpec.App, prompt string) bool {
	return prompts.ConfirmData(ctx, prompt, [][]string{
		{"Name", app.Name},
		{"Description", app.Description},
		{"Tags", strings.Join(app.Tags, ", ")},
	})
}
