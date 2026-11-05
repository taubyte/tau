package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"

	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	gossh "golang.org/x/crypto/ssh"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/forge/drivers"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/build"
	"github.com/taubyte/tau/services/build/artifact"
	"github.com/taubyte/tau/services/build/deploy"
	"github.com/taubyte/tau/services/build/linked"
	"github.com/taubyte/tau/services/build/run"
	"github.com/taubyte/tau/services/build/uploads"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// Config is the runner's section, and the cloud-wide keys it reads.
type Config struct {
	// Uploads is the directory a development cloud keeps a developer's build in
	// (services/build/uploads): a push of an uploaded commit is confirmed, checked
	// out and built from it. Empty is a cloud with no uploads.
	Uploads string `yaml:"uploads"`

	// Mirror is a file:// directory of repositories, each under its key, cloned from
	// in place of its forge: a development cloud's, so a CI run needs no forge to hold
	// the code. A repository it holds no folder for is its forge's.
	Mirror string `yaml:"mirror,omitempty"`

	// Domains is how a deploy treats the zones a project declares.
	Domains Domains `yaml:"domains"`

	// The cloud-wide keys a runner reads, one copy each on the node: the zone that
	// completes generated names, the forges it clones from, and the container daemon
	// a code build runs in.
	CloudFqdn string              `yaml:"cloudfqdn,omitempty"`
	Forges    []drivers.Forge     `yaml:"forges,omitempty"`
	Container containers.Declared `yaml:"container,omitempty"`
}

// Domains is the zones a project declares, as a deploy treats them.
type Domains struct {
	// Trusted serves every zone a project declares without its TXT proof: a
	// development cloud's, whose one developer owns every zone it is shown.
	Trusted bool `yaml:"trusted"`
}

// Start runs the runner on an instance: its clone credentials in the vault, its
// artifacts kept in the storage service, its deploys published to the register and
// zones proven by cm, each wherever discovery finds them, and the jobs it runs
// handed to it by the build node leading the queue.
func (s *server) Start(ctx context.Context, h service.Host) error {
	if s.stopped != nil {
		return errors.New("runner: already started")
	}
	var cfg Config
	if err := h.Config(&cfg); err != nil {
		return err
	}
	table := register.TableOn(h)
	vault := secrets.VaultOn(h)
	assets := storage.StoreOn(h)
	s.also = append(s.also, table, vault, assets)
	projects := linked.Projects{Table: table.Now}

	domains, err := provers(h, cfg)
	if err != nil {
		return err
	}
	s.also = append(s.also, domains)
	exec, confirm, err := compose(h.Region(), cfg, projects, table.Now, vault, domains)
	if err != nil {
		return err
	}
	s.exec, s.confirm, s.assets, s.self = exec, confirm, assets, h.Peer().Host()
	s.builders = func() []libp2ppeer.ID {
		if dir, ok := service.Get[service.Directory](h); ok {
			return dir.Peers(build.Build.Name)
		}
		return nil
	}

	loop, stop := context.WithCancel(context.WithoutCancel(ctx))
	s.stop, s.stopped = stop, make(chan struct{})
	go s.serve(loop)
	return nil
}

// compose is what a push is read and run with: the forge's clone, and the container
// engine. The section's mirror sits in front of the forge, and uploaded builds in front of both.
func compose(region string, cfg Config, projects linked.Projects, table func(context.Context) (register.Table, error), vault linked.Vault, domains deploy.Domains) (run.Runner, run.Confirmer, error) {
	forges, err := drivers.Registry(cfg.Forges)
	if err != nil {
		return nil, nil, err
	}
	pinned, err := drivers.HostKeys(cfg.Forges)
	if err != nil {
		return nil, nil, err
	}
	// SSH WHERE A DEPLOY KEY IS, HTTPS WHERE NONE IS: a deploy key is an ssh key, and a
	// repository without one is read anonymously or not at all. Both addresses, and the
	// ssh account, are the forge's; an ssh host is pinned.
	address := deploy.Forged(forges)
	if cfg.Mirror != "" {
		if at, err := url.Parse(cfg.Mirror); err != nil || at.Scheme != "file" {
			return nil, nil, fmt.Errorf("runner: mirror %q is not a file:// directory", cfg.Mirror)
		}
		address = deploy.Mirrored(cfg.Mirror, address)
	}
	clone := deploy.NewClone(linked.Credentials{Projects: projects, Vault: vault},
		deploy.WithAddress(address),
		deploy.WithHostKeys(func(host string) []gossh.PublicKey { return pinned[host] }))

	var (
		checkout deploy.Checkout = clone
		confirm  run.Confirmer   = clone
		engine   artifact.Engine
	)
	// A RUNNER THAT CANNOT RUN A CONTAINER CANNOT BUILD CODE, and it says so on the
	// push rather than starting and refusing every one. It still deploys
	// configuration, which is most of what a small cloud does.
	if cfg.Container.Any() {
		daemon, err := containers.Open(cfg.Container)
		if err != nil {
			return nil, nil, err
		}
		if engine, err = artifact.NewEngine(daemon); err != nil {
			return nil, nil, err
		}
	}
	if cfg.Uploads != "" {
		held, err := uploads.Open(cfg.Uploads)
		if err != nil {
			return nil, nil, fmt.Errorf("runner: uploads: %w", err)
		}
		checkout, confirm, engine = held.Checkout(checkout), held.Confirm(confirm), held.Engine(engine)
	}

	// THE CLOUD HAS THE ONE REGION ITS NODES SERVE, so a deploy places everything there.
	regions := schema.WithNoRegions()
	if region != "" {
		regions = schema.WithRegion(region)
	}
	options := []deploy.Option{
		deploy.WithProjects(projects),
		deploy.WithCheckout(checkout),
		// THE ONLY WRITE PATH TO THE REGISTER: a deploy is what a push became.
		deploy.WithRegister(func(ctx context.Context) (deploy.Register, error) { return table(ctx) }),
		deploy.WithCloudFqdn(cfg.CloudFqdn),
		deploy.WithRegions(regions),
		deploy.WithDomains(domains),
	}
	if engine != nil {
		builder, err := artifact.New(
			artifact.WithRegister(tableReader{table}),
			artifact.WithProjects(projects),
			artifact.WithCheckout(checkout),
			artifact.WithEngine(engine),
			// A source's clone URL is keyed by the forge that serves it, the same
			// function every push's repository key came out of.
			artifact.WithKeys(forges),
			artifact.WithSecrets(vault),
		)
		if err != nil {
			return nil, nil, err
		}
		options = append(options, deploy.WithBuilder(builder))
	}
	exec, err := deploy.New(options...)
	if err != nil {
		return nil, nil, err
	}
	return exec, confirm, nil
}

// trusted proves every zone: a cloud whose operator serves declared zones unproven.
type trusted struct{}

func (trusted) Prove(context.Context, string, string) error { return nil }
func (trusted) Close() error                                { return nil }

// prover is a deploy's domain proof, closed with the runner.
type prover interface {
	deploy.Domains
	io.Closer
}

// provers is what a deploy proves a zone with: nothing, in a cloud that trusts its
// zones, else the cm service, beside it or wherever discovery finds one.
func provers(h service.Host, cfg Config) (prover, error) {
	if cfg.Domains.Trusted {
		return trusted{}, nil
	}
	return cm.NewDomains(h.Peer().Host(),
		func() []libp2ppeer.ID {
			if dir, ok := service.Get[service.Directory](h); ok {
				return dir.Peers(cm.Name)
			}
			return nil
		},
		cm.StoreOn(h))
}

// tableReader is the register's table as a code build reads the configuration it
// builds against, reached per call.
type tableReader struct {
	table func(context.Context) (register.Table, error)
}

func (r tableReader) Environments(ctx context.Context, project string) ([]string, error) {
	t, err := r.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Environments(ctx, project)
}

func (r tableReader) Resolve(ctx context.Context, project, environment string) (pkgregister.Pointer, error) {
	t, err := r.table(ctx)
	if err != nil {
		return pkgregister.Pointer{}, err
	}
	return t.Resolve(ctx, project, environment)
}

func (r tableReader) Object(ctx context.Context, id pkgregister.ObjectID) ([]byte, error) {
	t, err := r.table(ctx)
	if err != nil {
		return nil, err
	}
	return t.Object(ctx, id)
}
