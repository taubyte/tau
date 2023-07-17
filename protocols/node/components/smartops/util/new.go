package smartOpUtil

import iface "github.com/taubyte/go-interfaces/services/substrate/smartops"

var _ iface.Util = &util{}

type util struct{}

// TODO implement
func (u *util) GPU() bool {
	return false
}

func New(srv iface.Service) (iface.Util, error) {
	return &util{}, nil
}
