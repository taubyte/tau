package resources

import (
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
	"github.com/taubyte/tau/tools/tau/cli/common"
	"github.com/urfave/cli/v2"
)

type List[T structureSpec.Structure] struct {
	LibListResources func() ([]T, error)
	TableList        func([]T)
}

func (h *List[T]) Default() common.Command {
	return common.Create(
		&cli.Command{
			Action: h.list,
		},
	)
}

func (h *List[T]) list(ctx *cli.Context) error {
	resources, err := h.LibListResources()
	if err != nil {
		return err
	}

	h.TableList(resources)
	return nil
}
