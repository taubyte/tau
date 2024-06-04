package config

import (
	"context"

	serviceIface "github.com/taubyte/tau/core/services"
)

type ProtoCommandIface interface {
	New(context.Context, *Node) (serviceIface.Service, error)
}
