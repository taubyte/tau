package auto

import (
	"crypto/tls"

	basicHttp "github.com/taubyte/http/basic"
	authP2P "github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/config"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	acme "github.com/taubyte/tau/services/auth/acme/store"
)

type Service struct {
	*basicHttp.Service
	err                 error
	certStore           *acme.Store
	authClient          *authP2P.Client
	tnsClient           ifaceTns.Client
	config              *config.Node
	customDomainChecker func(hello *tls.ClientHelloInfo) bool
}
