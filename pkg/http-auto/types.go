package auto

import (
	"crypto/x509"
	"time"

	"github.com/jellydator/ttlcache/v3"
	auth "github.com/taubyte/tau/core/services/auth"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/p2p/peer"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
	"golang.org/x/crypto/acme/autocert"
)

type Service struct {
	*basicHttp.Service
	err error

	certStore  autocert.Cache
	authClient auth.Client
	tnsClient  ifaceTns.Client

	clientNode peer.Node

	customDomainChecker func(host string) bool
	autoTrustDomain     func(host string) bool
	skipDomainProof     func(host string) bool

	acme             *options.OptionACME
	acmeCARoots      *x509.CertPool
	acmeCASkipVerify bool

	positiveCache *ttlcache.Cache[string, bool]
	negativeCache *ttlcache.Cache[string, bool]
}

var (
	PositiveTTL = 1 * time.Hour
	NegativeTTL = 1 * time.Minute
)
