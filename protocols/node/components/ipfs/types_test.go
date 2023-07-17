package ipfs

import (
	iface "github.com/taubyte/go-interfaces/services/substrate/ipfs"
)

var _ iface.Service = &Service{}
