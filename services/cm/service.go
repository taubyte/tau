package cm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/service"
	"github.com/taubyte/tau/p2p/peer"
	pkgkvdb "github.com/taubyte/tau/pkg/kvdb"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/services/secrets"
)

// Service is cm as an instance runs it: the consensus the services beside it
// order their writes with, and, where the cloud names its zone, the certificate
// store and the proofs of a project's domains.
type Service interface {
	service.Service
	Consensus

	// Store is the certificate store this node keeps, nil where the cloud names
	// no zone to keep certificates for.
	Store() *Store
}

// ChallengeDatabase is where DNS-01 answers live while an order is proved; a node
// running cm holds it, so the answers replicate through a node that is always up.
const ChallengeDatabase = "cm-challenges"

// Section is cm's own section of an instance's config, beside the cloud-wide
// `cm:` keys every node reads.
type Section struct {
	Consensus ConsensusConfig `yaml:"consensus"`
}

// hostConfig is what cm reads off its instance: the cloud's zone, the cloud-wide
// `cm:` keys, and its own section.
type hostConfig struct {
	Section   `yaml:",inline"`
	CloudFqdn string `yaml:"cloudfqdn,omitempty"`
	CM        Config `yaml:"cm,omitempty"`
}

// New is cm, unstarted. Start builds it from its instance.
func New() Service { return &server{} }

type server struct {
	mu     sync.Mutex
	peer   peer.Node
	root   string
	timing ConsensusConfig
	opened []raft.Cluster
	closed bool

	store *Store
	also  []io.Closer
}

// Start is cm on an instance: its groups on the instance's peer under its
// directory, and the certificate store where the cloud names a zone, its key in
// the secrets service, its records in the databases the instance shares.
func (s *server) Start(ctx context.Context, h service.Host) error {
	var cfg hostConfig
	if err := h.Config(&cfg); err != nil {
		return err
	}
	s.mu.Lock()
	s.peer, s.root, s.timing = h.Peer(), h.Root(), cfg.Consensus
	s.mu.Unlock()
	if cfg.CloudFqdn == "" {
		// A cloud that issues certificates issues them for its zone, and one that
		// names none keeps nothing here.
		if cfg.CM.Certificate.ACME.Enabled {
			return fmt.Errorf("%w, and this node names none (cloudfqdn)", ErrNoZone)
		}
		return nil
	}

	db, err := s.database(h, Name)
	if err != nil {
		return err
	}
	if _, err := s.database(h, ChallengeDatabase); err != nil {
		return err
	}
	vault := secrets.VaultOn(h)
	s.also = append(s.also, vault)
	whole := cfg.CM
	whole.Certificate.Zone = cfg.CloudFqdn
	options := []Option{WithVault(vault.Cloud(VaultService)), WithClock(h.Now)}
	if r, ok := service.Get[Resolver](h); ok {
		options = append(options, WithResolver(r))
	}
	store, err := Open(h.Peer().Host(), db, whole, options...)
	if err != nil {
		return err
	}
	if err := store.Start(ctx); err != nil {
		return err
	}
	s.store = store
	return nil
}

// database is the database called name, the instance's own when it shares them,
// else one opened here and closed with cm.
func (s *server) database(h service.Host, name string) (kvdb.Instance, error) {
	if dbs, ok := service.Get[service.Databases](h); ok {
		return dbs.Database(name)
	}
	db, err := pkgkvdb.Open(h.Peer(), name)
	if err != nil {
		return nil, err
	}
	s.also = append(s.also, closer(func() error { db.Close(); return nil }))
	return db, nil
}

func (s *server) Store() *Store { return s.store }

// StoreOn is the certificate store the cm beside h keeps, asked at call time: nil
// where none runs, or it keeps none.
func StoreOn(h service.Host) func() *Store {
	return func() *Store {
		if local, ok := service.Get[Service](h); ok {
			return local.Store()
		}
		return nil
	}
}

// Close stops the store, then every group still open, then what Start opened.
func (s *server) Close() error {
	var errs []error
	if s.store != nil {
		errs = append(errs, s.store.Close())
	}
	s.mu.Lock()
	opened := s.opened
	s.opened, s.closed = nil, true
	s.mu.Unlock()
	for _, c := range opened {
		if err := c.Close(); err != nil && !errors.Is(err, raft.ErrAlreadyClosed) {
			errs = append(errs, err)
		}
	}
	for i := len(s.also) - 1; i >= 0; i-- {
		errs = append(errs, s.also[i].Close())
	}
	s.also = nil
	return errors.Join(errs...)
}

type closer func() error

func (c closer) Close() error { return c() }
