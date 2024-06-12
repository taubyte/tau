package auto

import (
	"crypto/tls"

	basicHttp "github.com/taubyte/http/basic"
	"github.com/taubyte/tau/config"
	auth "github.com/taubyte/tau/core/services/auth"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	acme "github.com/taubyte/tau/services/auth/acme/store"
)

type Service struct {
	*basicHttp.Service
	err                 error
	certStore           *acme.Store
	authClient          auth.Client
	tnsClient           ifaceTns.Client
	config              *config.Node
	customDomainChecker func(hello *tls.ClientHelloInfo) bool
}
