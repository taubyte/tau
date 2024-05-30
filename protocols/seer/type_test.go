package seer

import (
	iface "github.com/taubyte/go-interfaces/services/seer"
)

var _ iface.Service = &Service{}
