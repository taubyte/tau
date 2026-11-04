package hosted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"connectrpc.com/connect"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/forge/drivers"
	"github.com/taubyte/tau/pkg/linkcreds"
	"github.com/taubyte/tau/pkg/tenancy"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/frontend"
	"github.com/taubyte/tau/services/build/frontend/forgeauth"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// maxConnections bounds the streams build keeps to auth and to the
// runner. It is small because every answer is cached in front of it (the hook
// secrets) and everything else is once per job.
const maxConnections = 4

// Service is the build service as an instance runs it.
type Service interface {
	service.Service

	// Addr is where it listens, nil before it starts.
	Addr() net.Addr
}

// ErrNoZoneForCertificates is a cloud that issues certificates and names no zone to
// issue them in.
var ErrNoZoneForCertificates = errors.New("build: certificates are issued for the cloud's zone, and this node names none (cloudfqdn)")

// config is the build service's section, and the cloud-wide keys it reads.
type config struct {
	frontend.Config `yaml:",inline"`

	CloudFqdn string          `yaml:"cloudfqdn,omitempty"`
	Tenancy   []string        `yaml:"tenancy,omitempty"`
	Forges    []drivers.Forge `yaml:"forges,omitempty"`
	CM        cm.Config       `yaml:"cm,omitempty"`
}

// server is the build service: the frontend Start built, and what Start opened
// for it, closed with it.
type server struct {
	*frontend.Service
	also []io.Closer
}

var _ Service = (*server)(nil)

type closer func()

func (c closer) Close() error { c(); return nil }

// Start builds the service from its instance: auth and the runner reached over
// p2p by discovery, the projects in the register, the link credentials in the vault,
// logs and artifacts in storage, domain claims and its certificate from cm.
func (f *server) Start(ctx context.Context, h service.Host) error {
	if f.Service != nil {
		return errors.New("build: already started")
	}
	var cfg config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	options, err := f.wire(h, cfg)
	if err != nil {
		return err
	}
	svc, err := frontend.New(cfg.Config, options...)
	if err != nil {
		return err
	}
	f.Service = svc
	return svc.Start(ctx)
}

func (f *server) wire(h service.Host, cfg config) ([]frontend.Option, error) {
	self := h.Peer().Host()
	auth, err := clients.NewPool(self, clients.Discovered(h, clients.Auth), clients.AuthProtocol, maxConnections)
	if err != nil {
		return nil, err
	}
	f.also = append(f.also, closer(auth.Close))
	backends, err := clients.NewPool(self, clients.Discovered(h, build.Runner.Name), build.Protocol, maxConnections)
	if err != nil {
		return nil, err
	}
	f.also = append(f.also, closer(backends.Close))
	builds, err := clients.Builds(backends)
	if err != nil {
		return nil, err
	}
	identities, err := clients.Identities(auth)
	if err != nil {
		return nil, err
	}
	authCalls, err := clients.AuthCalls(auth)
	if err != nil {
		return nil, err
	}

	served, err := tenancy.Parse(cfg.Tenancy)
	if err != nil {
		return nil, err
	}
	registry, err := drivers.Registry(cfg.Forges)
	if err != nil {
		return nil, err
	}
	forges, err := forgeauth.New(registry)
	if err != nil {
		return nil, err
	}

	table := register.TableOn(h)
	vault := secrets.VaultOn(h)
	assets := storage.StoreOn(h)
	f.also = append(f.also, table, vault, assets)
	projects := linked.Projects{Table: table.Now}

	members := func() []libp2ppeer.ID {
		if dir, ok := service.Get[service.Directory](h); ok {
			return dir.Peers(cm.Name)
		}
		return nil
	}
	local := func() *cm.Service {
		here, _ := service.Get[*cm.Service](h)
		return here
	}
	domains, err := cm.NewDomains(self, members, local)
	if err != nil {
		return nil, err
	}
	f.also = append(f.also, domains)

	options := []frontend.Option{
		// THE PROJECT TABLE IS THE REGISTER'S: who a project's owner is and what
		// roles it granted. This role writes it only through the link and grant
		// routes, after authorizing the caller.
		frontend.WithLinker(projects),
		frontend.WithProjects(projects),
		// WHO A CALLER IS IS AUTH'S TO SAY, from their tau token.
		frontend.WithCallers(identities),
		// SIGNING IN IS AUTH'S, carried here because auth has no ingress.
		frontend.WithAuthAPI(authCalls),
		frontend.WithBackend(builds),
		frontend.WithDeliveries(forges),
		frontend.WithTenancy(served),
		frontend.WithCloudFqdn(cfg.CloudFqdn),
		frontend.WithAssets(assets),
		frontend.WithPublished(published{table.Now}),
		// Build never holds a vault: it is the process strangers reach. This
		// is a stream to a node that does, used for hook secrets and a project's own.
		frontend.WithVault(vault),
		frontend.WithHooks(hooks{linked.Credentials{Projects: projects, Vault: vault}}),
		frontend.WithDomains(claims{domains}),
	}
	// A forge with no signed webhook reports pushes on its own feed (Gerrit's
	// stream-events), subscribed to as a second source (frontend/streams.go).
	if len(registry.EventHosts()) > 0 {
		options = append(options, frontend.WithStreams(forges, projects))
	}
	// TLS WHEN THE CLOUD ISSUES CERTIFICATES: the one cm keeps for this role's name,
	// the zone's wildcard when it issues one, read by cm's client as every public
	// listener reads its own.
	if cfg.CM.Certificate.ACME.Enabled {
		if cfg.CloudFqdn == "" {
			return nil, ErrNoZoneForCertificates
		}
		whole := cfg.CM
		whole.Certificate.Zone = cfg.CloudFqdn
		certificates, err := cm.NewClient(self, members, local, vault.Cloud(cm.VaultService), whole)
		if err != nil {
			return nil, err
		}
		f.also = append(f.also, certificates)
		options = append(options, frontend.WithTLS(certificates.TLSConfig()))
	}
	return options, nil
}

// Close stops the frontend, then what Start opened for it.
func (f *server) Close() error {
	var errs []error
	if f.Service != nil {
		errs = append(errs, f.Service.Close())
	}
	for i := len(f.also) - 1; i >= 0; i-- {
		errs = append(errs, f.also[i].Close())
	}
	f.also = nil
	return errors.Join(errs...)
}

// Addr is nil before Start, as the frontend's own is.
func (f *server) Addr() net.Addr {
	if f.Service == nil {
		return nil
	}
	return f.Service.Addr()
}

// published is what a deployment runs, read back for a developer's build against
// it, resolved per call since which register answers is a routing question.
type published struct {
	table func(context.Context) (register.Table, error)
}

func (p published) Environments(ctx context.Context, project string) ([]string, error) {
	t, err := p.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Environments(ctx, project)
}

// Compiled is the object a deployment's pointer names.
func (p published) Compiled(ctx context.Context, project, environment string) ([]byte, error) {
	t, err := p.table(ctx)
	if err != nil {
		return nil, err
	}
	pointer, err := t.Resolve(ctx, project, environment)
	if err != nil {
		return nil, err
	}
	return t.Object(ctx, pointer.Object)
}

// hooks is a project's webhook as a delivery is checked against it: its
// repositories, from the register, each with its own secret, from the vault.
type hooks struct{ linked.Credentials }

// Hook is ErrNoHook for a project none of whose repositories has a secret set: one
// that can be pushed to was linked AND had a secret set for the repository pushed.
func (l hooks) Hook(ctx context.Context, project string) (frontend.Hook, error) {
	repositories, err := l.Projects.Repositories(ctx, project)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return frontend.Hook{}, fmt.Errorf("%w: %w", frontend.ErrNoHook, err)
	}
	if err != nil {
		return frontend.Hook{}, err
	}
	hook := frontend.Hook{Project: project}
	for _, repository := range repositories {
		secret, found, err := l.Value(ctx, project, linkcreds.HookSecret(repository))
		if err != nil {
			return frontend.Hook{}, err
		}
		if !found {
			continue
		}
		repo := frontend.HookRepository{Key: repository, Secret: secret}
		if repo.Previous, repo.Replaced, err = l.Previous(ctx, project, linkcreds.HookSecret(repository)); err != nil {
			return frontend.Hook{}, err
		}
		hook.Repositories = append(hook.Repositories, repo)
	}
	if len(hook.Repositories) == 0 {
		return frontend.Hook{}, fmt.Errorf("%w: project %s has no hook secret set", frontend.ErrNoHook, project)
	}
	return hook, nil
}

// claims is the claim route over cm's domain client.
type claims struct{ d *cm.Domains }

func (c claims) Claim(ctx context.Context, project, fqdn string) (frontend.DomainClaim, error) {
	claim, err := c.d.Claim(ctx, project, fqdn)
	if errors.Is(err, cm.ErrBadClaim) {
		return frontend.DomainClaim{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return frontend.DomainClaim{}, err
	}
	return frontend.DomainClaim{Entry: claim.GetEntry(), Type: claim.GetType(), Token: claim.GetToken()}, nil
}
