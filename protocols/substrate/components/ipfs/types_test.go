package ipfs

import (
	iface "github.com/taubyte/go-interfaces/services/substrate/components/ipfs"
)

var _ iface.Service = &Service{}
