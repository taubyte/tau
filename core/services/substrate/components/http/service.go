package http

import (
	"net/http"
	"time"

	"github.com/taubyte/tau/core/services/substrate/components"
	structureSpec "github.com/taubyte/tau/pkg/specs/structure"
)

type Service interface {
	components.ServiceComponent
}

type Serviceable interface {
	components.Serviceable
	Handle(w http.ResponseWriter, r *http.Request, serv components.MatchDefinition) (time.Time, error)
	Provision() (Serviceable, error)
	IsProvisioned() bool
}

type Function interface {
	Serviceable
	components.FunctionServiceable
}

type Website interface {
	Serviceable
	Config() *structureSpec.Website
}
