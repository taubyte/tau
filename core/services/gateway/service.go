package gateway

import (
	services "github.com/taubyte/tau/core/services"
)

type Service interface {
	services.HttpService
}
