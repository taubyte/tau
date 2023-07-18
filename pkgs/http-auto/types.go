package auto

import (
	"crypto/tls"

	ifaceTns "github.com/taubyte/go-interfaces/services/tns"
	basicHttp "github.com/taubyte/http/basic"
	authP2P "github.com/taubyte/odo/clients/p2p/auth"
	acme "github.com/taubyte/odo/protocols/auth/acme/store"
)

type Service struct {
	*basicHttp.Service
	err                 error
	certStore           *acme.Store
	authClient          *authP2P.Client
	tnsClient           ifaceTns.Client
	customDomainChecker func(hello *tls.ClientHelloInfo) bool
}
