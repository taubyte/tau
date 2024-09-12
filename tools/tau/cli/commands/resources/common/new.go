package resources

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

type New[T structureSpec.Structure] struct {
	PromptsNew        func(ctx *cli.Context) (T, error)
	TableConfirm      func(ctx *cli.Context, service T, prompt string) bool
	PromptsCreateThis string
	LibNew            func(service T) error
	I18nCreated       func(name string)

	UniqueFlags []cli.Flag
}

func (h *New[T]) Default() common.Command {
	return common.Create(
		&cli.Command{
			Flags:  h.BasicFlags(),
			Action: h.Action(),
		},
	)
}

func (h *New[T]) BasicFlags() []cli.Flag {
	return append(append([]cli.Flag{
		flags.Description,
		flags.Tags,
	},
		// Insert unique flags between basic and Yes
		h.UniqueFlags...),

		flags.Yes,
	)
}

func (h *New[T]) Action() func(ctx *cli.Context) error {
	PanicIfMissingValue(h)

	return func(ctx *cli.Context) error {
		err := projectLib.ConfirmSelectedProject()
		if err != nil {
			return err
		}

		// Prompts.New will prompt for relative values for a new resource
		resource, err := h.PromptsNew(ctx)
		if err != nil {
			return err
		}

		// Table.Confirm will display a table and wait for a confirmation based on the
		// flag provided to `tau new -y` or offer a y\n selection
		confirm := h.TableConfirm(ctx, resource, h.PromptsCreateThis)
		if confirm {

			// Lib.New handles the seer creation and id generation on selected project/application
			err := h.LibNew(resource)
			if err != nil {
				return err
			}

			// I18n.Created will display a message that the resource of name has been created
			h.I18nCreated(resource.GetName())

			return nil
		}

		return nil
	}
}
