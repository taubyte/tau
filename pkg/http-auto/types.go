package auto

import (
	"time"

	"github.com/jellydator/ttlcache/v3"
	auth "github.com/taubyte/tau/core/services/auth"
	ifaceTns "github.com/taubyte/tau/core/services/tns"
	"github.com/taubyte/tau/pkg/config"
	basicHttp "github.com/taubyte/tau/pkg/http/basic"
	"github.com/taubyte/tau/pkg/http/options"
	acme "github.com/taubyte/tau/services/auth/acme/store"
)

type Service struct {
	*basicHttp.Service
	err                 error
	certStore           *acme.Store
	authClient          auth.Client
	tnsClient           ifaceTns.Client
	config              config.Config
	customDomainChecker func(host string) bool
	acme                *options.OptionACME

	positiveCache *ttlcache.Cache[string, bool]
	negativeCache *ttlcache.Cache[string, bool]
}

var (
	PositiveTTL = 1 * time.Hour
	NegativeTTL = 1 * time.Minute
)
