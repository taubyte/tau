package pass4

import (
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v1/utils"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

func Pipe(branch string) []transform.Transformer[object.Refrence] {
	return []transform.Transformer[object.Refrence]{
		// Initialize indexes first to ensure it always exists, even with no resources
		utils.Global(InitIndexes()),
		utils.Sub(utils.Global(Functions(branch)), "object"),
		utils.Sub(utils.Global(Websites(branch)), "object"),
		utils.Sub(utils.Global(Libraries(branch)), "object"),
		utils.Sub(utils.Global(Storage(branch)), "object"),
		utils.Sub(utils.Global(Database(branch)), "object"),
		utils.Sub(utils.Global(Messaging(branch)), "object"),
		utils.Sub(utils.Global(Smartops(branch)), "object"),
		utils.Sub(utils.Global(Domains(branch)), "object"),
	}
}
