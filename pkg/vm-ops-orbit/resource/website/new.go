package website

import (
	webIface "github.com/taubyte/tau/core/services/substrate/components/http"
	"github.com/taubyte/tau/pkg/vm-ops-orbit/common"
)

func New(f common.Factory) *Website {
	return &Website{
		Factory: f,
		callers: make(map[uint32]webIface.Website),
	}
}
