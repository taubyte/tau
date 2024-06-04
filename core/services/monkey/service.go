package monkey

import (
	services "github.com/taubyte/tau/core/services"
	"github.com/taubyte/tau/core/services/hoarder"
	"github.com/taubyte/tau/core/services/patrick"
)

type Service interface {
	services.Service
	Delete(jid string)
	Dev() bool

	Hoarder() hoarder.Client
	Patrick() patrick.Client
}
