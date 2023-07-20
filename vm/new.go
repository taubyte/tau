package tvm

import (
	"github.com/taubyte/go-interfaces/services/substrate"
	commonIface "github.com/taubyte/go-interfaces/services/substrate/components"
)

func New(srv substrate.Service, serviceable commonIface.Serviceable) commonIface.Function {
	return &Function{
		srv:         srv,
		serviceable: serviceable,
	}
}
