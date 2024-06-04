package seer

import (
	iface "github.com/taubyte/tau/core/services/seer"
)

var _ iface.Service = &Service{}
