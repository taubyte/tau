package pass2

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

func Pipe() []transform.Transformer[object.Refrence] {
	return []transform.Transformer[object.Refrence]{
		utils.Global(Functions()),
		utils.Global(Smartops()),
		utils.Global(Websites()),
	}
}
