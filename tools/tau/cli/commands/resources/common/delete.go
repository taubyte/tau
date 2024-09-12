package resources

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

type Delete[T structureSpec.Structure] struct {
	PromptsGetOrSelect func(ctx *cli.Context) (T, error)
	TableConfirm       func(ctx *cli.Context, resource T, prompt string) bool
	PromptsDeleteThis  string
	LibDelete          func(name string) error
	I18nDeleted        func(name string)
}

func (h *Delete[T]) Default() common.Command {
	return common.Create(
		&cli.Command{
			Flags:  h.BasicFlags(),
			Action: h.Action(),
		},
	)
}

func (h *Delete[T]) BasicFlags() []cli.Flag {
	return []cli.Flag{
		flags.Yes,
	}
}

func (h *Delete[T]) Action() func(ctx *cli.Context) error {
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

		// Table.Confirm will display a table and wait for a confirmation based on the
		// flag provided to `tau delete -y` or offer a y\n selection
		confirm := h.TableConfirm(ctx, resource, h.PromptsDeleteThis)
		if confirm {

			// Lib.Delete handles the seer deletion based on selected project/application
			err = h.LibDelete(resource.GetName())
			if err != nil {
				return err
			}

			// I18n.Deleted will display a message that the resource of name has been deleted
			h.I18nDeleted(resource.GetName())

			return nil
		}

		return nil
	}
}
