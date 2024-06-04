package function

import (
	funcIface "github.com/taubyte/tau/core/services/substrate/components/http"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *FunctionHttp {
	return &FunctionHttp{
		Factory: f,
		callers: make(map[uint32]funcIface.Function),
	}
}
