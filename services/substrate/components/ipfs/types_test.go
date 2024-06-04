package ipfs

import (
	iface "github.com/taubyte/tau/core/services/substrate/components/ipfs"
)

var _ iface.Service = &Service{}
