package hosted

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"connectrpc.com/connect"
	logging "github.com/ipfs/go-log/v2"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/forge/drivers"
	"github.com/taubyte/tau/pkg/linkcreds"
	"github.com/taubyte/tau/pkg/tenancy"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/clients"
	"github.com/taubyte/tau/services/build/ingress"
	"github.com/taubyte/tau/services/build/ingress/forgeauth"
	"github.com/taubyte/tau/services/build/jobs"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// maxConnections bounds the streams build keeps to auth and to the other build
// nodes. It is small because every answer is cached in front of it (the hook
// secrets) and everything else is once per job.
const maxConnections = 4

// Service is the build service as an instance runs it: the queue and the job
// records, and the routes in front of them.
type Service interface {
	jobs.Service

	// Addr is where it listens, nil before it starts.
	Addr() net.Addr
}

// ErrNoZoneForCertificates is a cloud that issues certificates and names no zone to
// issue them in.
var ErrNoZoneForCertificates = errors.New("build: certificates are issued for the cloud's zone, and this node names none (cloudfqdn)")

// config is the build service's section, and the cloud-wide keys it reads.
type config struct {
	ingress.Config `yaml:",inline"`

	CloudFqdn string          `yaml:"cloudfqdn,omitempty"`
	Tenancy   []string        `yaml:"tenancy,omitempty"`
	Forges    []drivers.Forge `yaml:"forges,omitempty"`
	CM        cm.Config       `yaml:"cm,omitempty"`
}

// server is the build service: the queue and the job records (control), the routes
// in front of them (ingress, which Start built), and what Start opened for them, closed
// with them.
type server struct {
	*ingress.Service
	control jobs.Service
	also    []io.Closer
}

var _ Service = (*server)(nil)

type closer func()

func (c closer) Close() error { c(); return nil }

// Start builds the service from its instance: the queue a raft group of the
// consensus beside it and the job records a database on the peer (the control,
// services/build/jobs), then the routes in front of them, asking that control
// in-process and the other build nodes over p2p when it does not lead; auth
// reached by discovery, the projects in the register, the link credentials in the
// vault, logs and artifacts in storage, domain claims and its certificate from cm.
func (f *server) Start(ctx context.Context, h service.Host) error {
	if f.control != nil {
		return errors.New("build: already started")
	}
	var cfg config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	f.control = jobs.New()
	if err := f.control.Start(ctx, h); err != nil {
		return err
	}
	options, err := f.wire(h, cfg)
	if err != nil {
		return err
	}
	svc, err := ingress.New(cfg.Config, options...)
	if err != nil {
		return err
	}
	f.Service = svc
	return svc.Start(ctx)
}

func (f *server) wire(h service.Host, cfg config) ([]ingress.Option, error) {
	self := h.Peer().Host()
	auth, err := clients.NewPool(self, clients.Discovered(h, clients.Auth), clients.AuthProtocol, maxConnections)
	if err != nil {
		return nil, err
	}
	f.also = append(f.also, closer(auth.Close))
	others, err := clients.NewPool(self, clients.Others(h, build.Build.Name), build.Protocol, maxConnections)
	if err != nil {
		return nil, err
	}
	f.also = append(f.also, closer(others.Close))
	elsewhere, err := clients.Builds(others)
	if err != nil {
		return nil, err
	}
	builds := routed{here: f.control.Local(), elsewhere: elsewhere}
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

	options := []ingress.Option{
		ingress.WithLogger(logging.Logger("tau.build").Infof),
		// THE PROJECT TABLE IS THE REGISTER'S: who a project's owner is and what
		// roles it granted. This role writes it only through the link and grant
		// routes, after authorizing the caller.
		ingress.WithLinker(projects),
		ingress.WithProjects(projects),
		// WHO A CALLER IS IS AUTH'S TO SAY, from their tau token.
		ingress.WithCallers(identities),
		// SIGNING IN IS AUTH'S, carried here because auth has no ingress.
		ingress.WithAuthAPI(authCalls),
		ingress.WithJobs(builds),
		ingress.WithDeliveries(forges),
		ingress.WithTenancy(served),
		ingress.WithCloudFqdn(cfg.CloudFqdn),
		ingress.WithAssets(assets),
		ingress.WithPublished(published{table.Now}),
		// Build never holds a vault: it is the process strangers reach. This
		// is a stream to a node that does, used for hook secrets and a project's own.
		ingress.WithVault(vault),
		ingress.WithHooks(hooks{linked.Credentials{Projects: projects, Vault: vault}}),
		ingress.WithDomains(claims{domains}),
	}
	// A forge with no signed webhook reports pushes on its own feed (Gerrit's
	// stream-events), subscribed to as a second source (ingress/streams.go).
	if len(registry.EventHosts()) > 0 {
		options = append(options, ingress.WithStreams(forges, projects))
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
		options = append(options, ingress.WithTLS(certificates.TLSConfig()))
	}
	return options, nil
}

// Close stops the routes, then the control, then what Start opened for them.
func (f *server) Close() error {
	var errs []error
	if f.Service != nil {
		errs = append(errs, f.Service.Close())
	}
	if f.control != nil {
		errs = append(errs, f.control.Close())
	}
	for i := len(f.also) - 1; i >= 0; i-- {
		errs = append(errs, f.also[i].Close())
	}
	f.also = nil
	return errors.Join(errs...)
}

// Built, WaitForLeader and Local are the control's: the build service is what a
// worker asks what a deployment runs.
func (f *server) Built(project, environment, resource string) (string, error) {
	if f.control == nil {
		return "", jobs.ErrNotStarted
	}
	return f.control.Built(project, environment, resource)
}

func (f *server) WaitForLeader(ctx context.Context) error {
	if f.control == nil {
		return jobs.ErrNotStarted
	}
	return f.control.WaitForLeader(ctx)
}

func (f *server) Local() jobs.Local { return f.control.Local() }

// Addr is nil before Start, as ingress's own is.
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
func (l hooks) Hook(ctx context.Context, project string) (ingress.Hook, error) {
	repositories, err := l.Projects.Repositories(ctx, project)
	if connect.CodeOf(err) == connect.CodeNotFound {
		return ingress.Hook{}, fmt.Errorf("%w: %w", ingress.ErrNoHook, err)
	}
	if err != nil {
		return ingress.Hook{}, err
	}
	hook := ingress.Hook{Project: project}
	for _, repository := range repositories {
		secret, found, err := l.Value(ctx, project, linkcreds.HookSecret(repository))
		if err != nil {
			return ingress.Hook{}, err
		}
		if !found {
			continue
		}
		repo := ingress.HookRepository{Key: repository, Secret: secret}
		if repo.Previous, repo.Replaced, err = l.Previous(ctx, project, linkcreds.HookSecret(repository)); err != nil {
			return ingress.Hook{}, err
		}
		hook.Repositories = append(hook.Repositories, repo)
	}
	if len(hook.Repositories) == 0 {
		return ingress.Hook{}, fmt.Errorf("%w: project %s has no hook secret set", ingress.ErrNoHook, project)
	}
	return hook, nil
}

// claims is the claim route over cm's domain client.
type claims struct{ d *cm.Domains }

func (c claims) Claim(ctx context.Context, project, fqdn string) (ingress.DomainClaim, error) {
	claim, err := c.d.Claim(ctx, project, fqdn)
	if errors.Is(err, cm.ErrBadClaim) {
		return ingress.DomainClaim{}, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if err != nil {
		return ingress.DomainClaim{}, err
	}
	return ingress.DomainClaim{Entry: claim.GetEntry(), Type: claim.GetType(), Token: claim.GetToken()}, nil
}
