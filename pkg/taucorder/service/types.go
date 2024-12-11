package service

import (
	"context"
	"net/http"
	"sync"

	dream "github.com/taubyte/tau/clients/http/dream"
	authIface "github.com/taubyte/tau/core/services/auth"
	p2p "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/spore-drive/config"
	pbconnect "github.com/taubyte/tau/pkg/taucorder/proto/gen/taucorder/v1/taucorderv1connect"

	hoarderIface "github.com/taubyte/tau/core/services/hoarder"
	monkeyIface "github.com/taubyte/tau/core/services/monkey"
	patrickIface "github.com/taubyte/tau/core/services/patrick"
	seerIface "github.com/taubyte/tau/core/services/seer"
	tnsIface "github.com/taubyte/tau/core/services/tns"
)

type nodeService struct {
	pbconnect.UnimplementedNodeServiceHandler
	*Service
}

type swarmService struct {
	pbconnect.UnimplementedSwarmServiceHandler
	*Service
}

type authService struct {
	pbconnect.UnimplementedAuthServiceHandler
	*Service
}

type projectsService struct {
	pbconnect.UnimplementedProjectsInAuthServiceHandler
	*Service
}

type reposService struct {
	pbconnect.UnimplementedRepositoriesInAuthServiceHandler
	*Service
}

type hooksService struct {
	pbconnect.UnimplementedGitHooksInAuthServiceHandler
	*Service
}

type x509Service struct {
	pbconnect.UnimplementedX509InAuthServiceHandler
	*Service
}

type seerService struct {
	pbconnect.UnimplementedSeerServiceHandler
	*Service
}

type hoarderService struct {
	pbconnect.UnimplementedHoarderServiceHandler
	*Service
}

type tnsService struct {
	pbconnect.UnimplementedTNSServiceHandler
	*Service
}

type patrickService struct {
	pbconnect.UnimplementedPatrickServiceHandler
	*Service
}

type monkeyService struct {
	pbconnect.UnimplementedMonkeyServiceHandler
	*Service
}

type healthService struct {
	pbconnect.UnimplementedHealthServiceHandler
	*Service
}

type instance struct {
	ctx  context.Context
	ctxC context.CancelFunc

	config config.Parser

	dream    *dream.Client
	universe string

	authClient    authIface.Client
	seerClient    seerIface.Client
	hoarderClient hoarderIface.Client
	monkeyClient  monkeyIface.Client
	tnsClient     tnsIface.Client
	patrickClient patrickIface.Client

	p2p.Node
}

type Service struct {
	ctx      context.Context
	lock     sync.RWMutex
	handlers map[string]http.Handler
	nodes    map[string]*instance
	resolver ConfigResolver
}

type ConfigResolver interface {
	Lookup(id string) (config.Parser, error)
}
