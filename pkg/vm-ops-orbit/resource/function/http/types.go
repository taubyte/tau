package function

import (
	"sync"

	funcIface "github.com/taubyte/tau/core/services/substrate/components/http"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type FunctionHttp struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]funcIface.Function
}
