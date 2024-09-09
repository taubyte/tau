package resources

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

type Edit[T structureSpec.Structure] struct {
	PromptsGetOrSelect func(ctx *cli.Context) (T, error)
	PromptsEdit        func(ctx *cli.Context, prev T) error
	TableConfirm       func(ctx *cli.Context, resource T, prompt string) bool
	PromptsEditThis    string
	LibSet             func(resource T) error
	I18nEdited         func(name string)

	UniqueFlags []cli.Flag
}

func (h *Edit[T]) Default() common.Command {
	return common.Create(
		&cli.Command{
			Flags:  h.BasicFlags(),
			Action: h.Action(),
		},
	)
}

func (h *Edit[T]) BasicFlags() []cli.Flag {
	return append(append([]cli.Flag{
		flags.Description,
		flags.Tags,
	},
		// Insert unique flags between basic and Yes
		h.UniqueFlags...),

		flags.Yes,
	)
}

func (h *Edit[T]) Action() func(ctx *cli.Context) error {
	PanicIfMissingValue(h)

	return func(ctx *cli.Context) error {
		err := projectLib.ConfirmSelectedProject()
		if err != nil {
			return err
		}

		// Prompts.GetOrSelect will get the struct from --name or offer a selection menu
		resource, err := h.PromptsGetOrSelect(ctx)
		if err != nil {
			return err
		}

		// Prompts.Edit will get the relative items from flags
		// and edit the real values in the passed in struct pointer
		// resource Edit does not return an error
		err = h.PromptsEdit(ctx, resource)
		if err != nil {
			return err
		}

		// Table.Confirm will display a table and wait for a confirmation based on the
		// flag provided to `tau edit -y` or offer a y\n selection
		confirm := h.TableConfirm(ctx, resource, h.PromptsEditThis)
		if confirm {

			// Lib.Set handles the seer set based on selected project/application
			err = h.LibSet(resource)
			if err != nil {
				return err
			}
			// I18n.Edited will display a message that the resource of name has been edited
			h.I18nEdited(resource.GetName())

			return nil
		}

		return nil
	}
}
