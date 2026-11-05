package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"time"

	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/pkg/containers"
	"github.com/taubyte/tau/pkg/forge/drivers"
	pkgregister "github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
	"github.com/taubyte/tau/services/cm"
	"github.com/taubyte/tau/services/plm/artifact"
	"github.com/taubyte/tau/services/plm/deploy"
	"github.com/taubyte/tau/services/plm/linked"
	"github.com/taubyte/tau/services/plm/run"
	"github.com/taubyte/tau/services/plm/uploads"
	"github.com/taubyte/tau/services/register"
	"github.com/taubyte/tau/services/secrets"
	"github.com/taubyte/tau/services/storage"
)

// Config is the builder's section, and the cloud-wide keys it reads.
type Config struct {
	// Uploads is the directory a development cloud keeps a developer's build in
	// (services/plm/uploads): a push of an uploaded commit is confirmed, checked
	// out and built from it. Empty is a cloud with no uploads.
	Uploads string `yaml:"uploads"`

	// Mirror is a file:// directory of repositories, each under its key, cloned from
	// in place of its forge: a development cloud's, so a CI run needs no forge to hold
	// the code. A repository it holds no folder for is its forge's.
	Mirror string `yaml:"mirror,omitempty"`

	// Domains is how a deploy treats the zones a project declares.
	Domains Domains `yaml:"domains"`

	// Logs is where a run's log lives until plm seals it, and its bounds.
	Logs Logs `yaml:"logs"`

	// The cloud-wide keys a builder reads, one copy each on the node: the zone that
	// completes generated names, the forges it clones from, and the container daemon
	// a code build runs in.
	CloudFqdn string              `yaml:"cloudfqdn,omitempty"`
	Forges    []drivers.Forge     `yaml:"forges,omitempty"`
	Container containers.Declared `yaml:"container,omitempty"`
}

// Logs is a builder's logs: where each run's lives, how much it may hold, and how
// long it waits for its seal.
type Logs struct {
	// Dir is where a run's log lives while it is held; empty is a scratch directory,
	// since a log that matters reaches the storage service. The builder owns it:
	// livelog.OwnDir refuses another's directory.
	Dir string `yaml:"dir"`
	// MaxBytes bounds one run's log; past it the log says it was cut and the run
	// carries on. Zero is DefaultMaxLogBytes.
	MaxBytes int64 `yaml:"maxBytes"`
	// SealWithin is how long an ended run's log waits for plm to seal it before
	// it is dropped. Zero is DefaultSealWithin.
	SealWithin time.Duration `yaml:"sealWithin"`
	// KeepWithin bounds handing a sealed log to the storage service. Zero is
	// DefaultKeepWithin.
	KeepWithin time.Duration `yaml:"keepWithin"`
	// KeepSealed is how long a sealed log is still followed whole, for a follower
	// that comes as its run ends. Zero is DefaultKeepSealed.
	KeepSealed time.Duration `yaml:"keepSealed"`
}

const (
	// DefaultMaxLogBytes bounds one run's log: the build script deciding how much to
	// print is a stranger's.
	DefaultMaxLogBytes = 16 << 20
	// DefaultSealWithin is how long an unsealed log is held.
	DefaultSealWithin = 10 * time.Minute
	// DefaultKeepWithin bounds keeping one sealed log.
	DefaultKeepWithin = 10 * time.Second
	// DefaultKeepSealed is how long a sealed log is still followed.
	DefaultKeepSealed = time.Minute
)

// withDefaults fills in the zero values, and refuses a negative one.
func (l Logs) withDefaults() (Logs, error) {
	if l.MaxBytes < 0 || l.SealWithin < 0 || l.KeepWithin < 0 || l.KeepSealed < 0 {
		return l, errors.New("builder: a log bound must not be negative")
	}
	if l.Dir == "" {
		l.Dir = defaultLogDir()
	}
	if l.MaxBytes == 0 {
		l.MaxBytes = DefaultMaxLogBytes
	}
	if l.SealWithin == 0 {
		l.SealWithin = DefaultSealWithin
	}
	if l.KeepWithin == 0 {
		l.KeepWithin = DefaultKeepWithin
	}
	if l.KeepSealed == 0 {
		l.KeepSealed = DefaultKeepSealed
	}
	return l, nil
}

// Domains is the zones a project declares, as a deploy treats them.
type Domains struct {
	// Trusted serves every zone a project declares without its TXT proof: a
	// development cloud's, whose one developer owns every zone it is shown.
	Trusted bool `yaml:"trusted"`
}

// Open is a builder's runs on an instance, configured by cfg: its clone credentials
// in the vault, its artifacts and logs kept in the storage service, its deploys
// published to the register and zones proven by cm, each wherever discovery finds
// them. The builder service serves them; a development cloud running no builder
// calls them in place.
func Open(h service.Host, cfg Config) (*Runs, error) {
	logs, err := cfg.Logs.withDefaults()
	if err != nil {
		return nil, err
	}
	table := register.TableOn(h)
	vault := secrets.VaultOn(h)
	assets := storage.StoreOn(h)
	r := newRuns(nil, nil, assets, h, logs)
	r.also = []io.Closer{table, vault, assets}
	domains, err := provers(h, cfg)
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	r.also = append(r.also, domains)
	r.exec, r.confirm, err = compose(h.Region(), cfg, linked.Projects{Table: table.Now}, table.Now, vault, domains)
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	return r, nil
}

// compose is what a push is read and run with: the forge's clone, and the container
// engine. The section's mirror sits in front of the forge, and uploaded builds in front of both.
func compose(region string, cfg Config, projects linked.Projects, table func(context.Context) (register.Table, error), vault linked.Vault, domains deploy.Domains) (run.Executor, run.Confirmer, error) {
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
	clone := deploy.NewClone(linked.Credentials{Projects: projects, Vault: vault},
		deploy.WithAddress(deploy.Forged{Forges: forges}),
		deploy.WithHostKeys(deploy.Pinned(pinned)))

	var (
		checkout deploy.Checkout = clone
		confirm  run.Confirmer   = clone
		engine   artifact.Engine
	)
	// A BUILDER THAT CANNOT RUN A CONTAINER CANNOT BUILD CODE, and it says so on the
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
	if cfg.Mirror != "" {
		if at, err := url.Parse(cfg.Mirror); err != nil || at.Scheme != "file" {
			return nil, nil, fmt.Errorf("builder: mirror %q is not a file:// directory", cfg.Mirror)
		}
		mirror := deploy.NewMirror(cfg.Mirror, checkout, confirm)
		checkout, confirm = mirror, mirror
	}
	if cfg.Uploads != "" {
		held, err := uploads.Open(cfg.Uploads)
		if err != nil {
			return nil, nil, fmt.Errorf("builder: uploads: %w", err)
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

// prover is a deploy's domain proof, closed with the builder.
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
	return cm.NewDomains(h.Peer().Host(), cm.On(h))
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
