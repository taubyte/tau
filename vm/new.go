package tvm

import (
	commonIface "github.com/taubyte/go-interfaces/services/substrate/common"
)

func New(srv commonIface.Service, serviceable commonIface.Serviceable) commonIface.Function {
	return &Function{
		srv:         srv,
		serviceable: serviceable,
	}
}
