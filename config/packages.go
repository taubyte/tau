package config

import (
	"context"
	serviceIface "github.com/taubyte/go-interfaces/services"
)

type Package interface {
	New(context.Context, *Protocol) (serviceIface.Service, error)
}
