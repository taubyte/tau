package resources

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/taubyte/tau/tools/tau/flags"
	projectLib "github.com/taubyte/tau/tools/tau/lib/project"
	"github.com/urfave/cli/v2"
)

type Query[T structureSpec.Structure] struct {
	LibListResources func() ([]T, error)
	TableList        func([]T)

	PromptsGetOrSelect func(ctx *cli.Context) (T, error)
	TableQuery         func(T)
}

func (h *Query[T]) Default() common.Command {
	return common.Create(
		&cli.Command{
			Flags:  h.BasicFlags(),
			Action: h.Action(),
		},
	)
}

func (h *Query[T]) BasicFlags() []cli.Flag {
	return []cli.Flag{
		flags.List,
	}
}

func (h *Query[T]) Action() func(ctx *cli.Context) error {
	PanicIfMissingValue(h)

	return func(ctx *cli.Context) error {
		err := projectLib.ConfirmSelectedProject()
		if err != nil {
			return err
		}

		// will call list if the --list flag is set
		if ctx.Bool(flags.List.Name) {
			return h.list(ctx)
		}

		// Prompts.GetOrSelect will get the struct from --name or offer a selection menu
		resource, err := h.PromptsGetOrSelect(ctx)
		if err != nil {
			return err
		}

		// Table.Query will display a detailed table for the selected resource
		h.TableQuery(resource)

		return nil
	}
}

func (h *Query[T]) list(ctx *cli.Context) error {
	// Lib.ListResources will return a []structureSpec.Resource
	// for accessing values within all of
	// the relative(project/application) values
	resources, err := h.LibListResources()
	if err != nil {
		return err
	}

	// Table.List will display a table of all of the resources
	h.TableList(resources)
	return nil
}
