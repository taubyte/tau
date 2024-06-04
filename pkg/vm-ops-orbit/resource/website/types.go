package website

import (
	"sync"

	webIface "github.com/taubyte/tau/core/services/substrate/components/http"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

type Website struct {
	common.Factory

	callersLock sync.RWMutex
	callers     map[uint32]webIface.Website
}
