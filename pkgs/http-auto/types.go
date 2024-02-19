package auto

import (
	"crypto/tls"

	ifaceTns "github.com/taubyte/go-interfaces/services/tns"
	basicHttp "github.com/taubyte/http/basic"
	authP2P "github.com/taubyte/tau/clients/p2p/auth"
	"github.com/taubyte/tau/config"
	acme "github.com/taubyte/tau/protocols/auth/acme/store"
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
