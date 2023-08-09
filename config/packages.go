package config

import (
	"context"

	serviceIface "github.com/taubyte/go-interfaces/services"
)

type ProtoCommandIface interface {
	New(context.Context, *Node) (serviceIface.Service, error)
}
