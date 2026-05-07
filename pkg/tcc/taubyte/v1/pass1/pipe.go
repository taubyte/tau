package pass1

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

func Pipe(cloud string) []transform.Transformer[object.Refrence] {
	return []transform.Transformer[object.Refrence]{
		Project(),
		Cloud(cloud),
		Applications(),
		utils.Global(Functions()),
		utils.Global(Smartops()),
		utils.Global(Websites()),
		utils.Global(Databases()),
		utils.Global(Storages()),
		utils.Global(Domains()),
		utils.Global(Libraries()),
		utils.Global(Messaging()),
		utils.Global(Services()),
	}
}
