package secrets

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	logging "github.com/ipfs/go-log/v2"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	"github.com/libp2p/go-libp2p/core/host"
	peerCore "github.com/libp2p/go-libp2p/core/peer"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/rpc"
	"github.com/taubyte/tau/pkg/proto/gen/tau/secrets/v2/secretsv2connect"
	"github.com/taubyte/tau/pkg/secrets/tdh"
)

var serviceLog = logging.Logger("tau.secrets.service")

const (
	// defaultBootDelay lets the node finish meshing before it looks for peers
	// to generate a key with. A write that finds no key does not wait for it:
	// asking for the active key forms one (see the Key handler).
	defaultBootDelay = 2 * time.Second

	// bootstrapAttempts covers a member that was not discovered in time: the
	// next attempt sees a wider roster, which is a different round.
	bootstrapAttempts = 3

	// defaultRotateAfter is how long a group key stays active before the
	// cluster generates a new epoch and re-encrypts under it.
	defaultRotateAfter = 30 * 24 * time.Hour

	// defaultReconcileInterval is how often a node checks whether the roster
	// changed, the key aged out, or a migration is unfinished.
	defaultReconcileInterval = 5 * time.Minute

	// callTimeout bounds one call to another member.
	callTimeout = 10 * time.Second

	// EpochTopic is where a member announces a new epoch. The announcement
	// carries no authority; it only saves a listener its tick.
	EpochTopic = "/tau/secrets/v2/epoch"
)

// Config is the operator's configuration of a node's secrets service.
type Config struct {
	// Key protects this node's shares on disk. It is per-node material: it
	// never leaves the node and is never used to reach another one. Empty
	// derives one from the node's identity, which sits beside the shares.
	Key string `yaml:"key"`
}

// Service stores secrets that no single node can read. Clients encrypt under
// the group public key; recovering a plaintext takes partial decryptions from a
// threshold of nodes. See the package doc for what that defends against.
type Service struct {
	host      host.Host
	db        kvdb.KVDB
	keys      *keyStore
	records   *recordStore
	wrappings *wrappingStore
	shares    *shareStore

	members        func() []peerCore.ID
	pubsub         PubSub
	bootDelay      time.Duration
	rotateAfter    time.Duration
	reconcileEvery time.Duration
	now            func() time.Time
	serverOptions  []rpc.ServerOption

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// nudge coalesces reconcile requests: capacity one, never blocking, so a
	// burst of announcements costs a single pass.
	nudge chan struct{}

	// lifecycle guards the start and shutdown state only. It is deliberately
	// not srv.mu: background work is started from paths that already hold
	// that one.
	lifecycle sync.Mutex
	started   bool
	closed    bool
	server    *rpc.Server
	topic     *pubsub.Topic
	sub       *pubsub.Subscription
	closeErr  error

	mu         sync.Mutex
	rounds     map[string]*round
	lastRoster []string

	conns peers
}

// PubSub is where epochs are announced. A node's gossipsub satisfies it.
type PubSub interface {
	Join(topic string, opts ...pubsub.TopicOpt) (*pubsub.Topic, error)
}

type options struct {
	root           string
	atRestKey      string
	members        func() []peerCore.ID
	pubsub         PubSub
	bootDelay      time.Duration
	rotateAfter    time.Duration
	reconcileEvery time.Duration
	now            func() time.Time
	serverOptions  []rpc.ServerOption
}

// Option configures a Service at construction.
type Option func(*options) error

// WithRoot is the directory this node keeps its shares in. Required: a share
// is the one piece of key material a node cannot re-derive, so it lives on
// this machine's disk and nowhere else.
func WithRoot(dir string) Option {
	return func(o *options) error {
		if dir == "" {
			return errors.New("an empty root is not a directory")
		}
		o.root = dir
		return nil
	}
}

// WithConfig applies the operator's configuration.
func WithConfig(c Config) Option {
	return func(o *options) error {
		o.atRestKey = c.Key
		return nil
	}
}

// WithMembers is who else runs this service, asked each time a roster is
// formed. Without it the members are the connected peers that speak the
// protocol.
func WithMembers(members func() []peerCore.ID) Option {
	return func(o *options) error {
		if members == nil {
			return errors.New("a nil member source")
		}
		o.members = members
		return nil
	}
}

// WithPubSub is where a new epoch is announced and heard. Without it a node
// learns of one on its next reconcile tick.
func WithPubSub(ps PubSub) Option {
	return func(o *options) error {
		if ps == nil {
			return errors.New("a nil pubsub")
		}
		o.pubsub = ps
		return nil
	}
}

// WithBootstrapDelay sets how long the node waits for peers before generating
// the cluster's first key.
func WithBootstrapDelay(delay time.Duration) Option {
	return func(o *options) error {
		if delay < 0 {
			return errors.New("bootstrap delay cannot be negative")
		}
		o.bootDelay = delay
		return nil
	}
}

// WithRotationAge sets how long a group key stays active. Zero disables
// age-based rotation; a roster change still rotates.
func WithRotationAge(age time.Duration) Option {
	return func(o *options) error {
		if age < 0 {
			return errors.New("rotation age cannot be negative")
		}
		o.rotateAfter = age
		return nil
	}
}

// WithReconcileInterval sets how often the node re-checks the roster, the key
// age, and whether a migration is unfinished.
func WithReconcileInterval(interval time.Duration) Option {
	return func(o *options) error {
		if interval <= 0 {
			return errors.New("reconcile interval must be positive")
		}
		o.reconcileEvery = interval
		return nil
	}
}

// WithClock injects the clock a key's age is read from.
func WithClock(now func() time.Time) Option {
	return func(o *options) error {
		if now == nil {
			return errors.New("a nil clock")
		}
		o.now = now
		return nil
	}
}

// WithServerOptions passes p2p/rpc server options through to Start.
func WithServerOptions(opts ...rpc.ServerOption) Option {
	return func(o *options) error {
		o.serverOptions = append(o.serverOptions, opts...)
		return nil
	}
}

var (
	ErrNoHost = errors.New("secrets: no host to answer on")
	ErrNoDB   = errors.New("secrets: no database to keep secrets in")
	ErrNoRoot = errors.New("secrets: no directory to keep this node's shares in")
)

// New validates and opens this node's share store. It binds nothing and
// starts nothing until Start.
func New(h host.Host, db kvdb.KVDB, opts ...Option) (*Service, error) {
	if h == nil {
		return nil, ErrNoHost
	}
	if db == nil {
		return nil, ErrNoDB
	}

	o := options{
		bootDelay:      defaultBootDelay,
		rotateAfter:    defaultRotateAfter,
		reconcileEvery: defaultReconcileInterval,
		now:            time.Now,
	}
	for _, apply := range opts {
		if err := apply(&o); err != nil {
			return nil, fmt.Errorf("secrets: option: %w", err)
		}
	}
	if o.root == "" {
		return nil, ErrNoRoot
	}

	atRest, err := newAtRest(h, o.atRestKey)
	if err != nil {
		return nil, err
	}
	serviceLog.Info(atRest.posture())

	shares, err := newShareStore(o.root, atRest, o.now)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	srv := &Service{
		host:           h,
		db:             db,
		keys:           &keyStore{db: db},
		records:        &recordStore{db: db},
		wrappings:      &wrappingStore{db: db},
		shares:         shares,
		members:        o.members,
		pubsub:         o.pubsub,
		bootDelay:      o.bootDelay,
		rotateAfter:    o.rotateAfter,
		reconcileEvery: o.reconcileEvery,
		now:            o.now,
		serverOptions:  o.serverOptions,
		ctx:            ctx,
		cancel:         cancel,
		rounds:         make(map[string]*round),
		nudge:          make(chan struct{}, 1),
		conns:          peers{host: h, open: map[peerCore.ID]*rpc.Client{}},
	}
	return srv, nil
}

// Start listens, joins the epoch topic and starts the background work: the
// first key's bootstrap and the reconcile loop.
func (srv *Service) Start(ctx context.Context) error {
	srv.lifecycle.Lock()
	defer srv.lifecycle.Unlock()

	switch {
	case srv.closed:
		return errors.New("secrets: closed")
	case srv.started:
		return errors.New("secrets: already started")
	}

	server, err := rpc.Listen(ctx, srv.host, Protocol, srv.mount(), srv.serverOptions...)
	if err != nil {
		return fmt.Errorf("secrets: listen on %s: %w", Protocol, err)
	}
	srv.server = server

	if err := srv.watchEpochs(); err != nil {
		return err
	}

	srv.started = true

	// Separate goroutines: bootstrap sleeps between attempts, and
	// reconciliation must not wait those out before it can rotate or finish a
	// migration.
	srv.wg.Add(2)
	go func() {
		defer srv.wg.Done()
		srv.bootstrap(srv.ctx)
	}()
	go func() {
		defer srv.wg.Done()
		srv.reconcileLoop(srv.ctx)
	}()

	return nil
}

// Close stops serving, waits for background work, and closes the share
// store. It does not close the database, which its composer handed it.
func (srv *Service) Close() error {
	srv.cancel()

	// Refuse new background work before waiting, so nothing can still be
	// querying the store once this returns -- pebble panics on a closed handle.
	srv.lifecycle.Lock()
	if srv.closed {
		srv.lifecycle.Unlock()
		return srv.closeErr
	}
	srv.closed = true
	srv.lifecycle.Unlock()

	srv.wg.Wait()

	var errs []error
	if srv.server != nil {
		errs = append(errs, srv.server.Close())
	}
	if srv.sub != nil {
		srv.sub.Cancel()
	}
	if srv.topic != nil {
		errs = append(errs, srv.topic.Close())
	}
	srv.conns.close()
	errs = append(errs, srv.shares.Close())

	srv.closeErr = errors.Join(errs...)
	return srv.closeErr
}

// bootstrap generates the cluster's first key once the node has had a moment to
// find its peers. Every node runs this and they do not need to agree on who
// goes first: a round is named by its roster, so nodes that start together
// start the same round. Retries cover the case where a member was not
// discovered in time -- the next attempt sees a different roster, which is a
// different round, and one that can actually complete.
func (srv *Service) bootstrap(ctx context.Context) {
	for attempt := range bootstrapAttempts {
		select {
		case <-ctx.Done():
			return
		case <-time.After(srv.bootDelay):
		}

		err := srv.ensureKey(ctx)
		if err == nil {
			return
		}
		if attempt == bootstrapAttempts-1 {
			serviceLog.Errorf("generating the first distributed key: %s", err)
		}
	}
}

// watchEpochs listens for other nodes announcing a new epoch.
func (srv *Service) watchEpochs() error {
	if srv.pubsub == nil {
		return nil
	}

	topic, err := srv.pubsub.Join(EpochTopic)
	if err != nil {
		return fmt.Errorf("secrets: joining %s: %w", EpochTopic, err)
	}
	sub, err := topic.Subscribe()
	if err != nil {
		topic.Close()
		return fmt.Errorf("secrets: subscribing to %s: %w", EpochTopic, err)
	}
	srv.topic, srv.sub = topic, sub

	srv.wg.Add(1)
	go func() {
		defer srv.wg.Done()
		for {
			msg, err := sub.Next(srv.ctx)
			if err != nil {
				return
			}
			if msg.ReceivedFrom != srv.host.ID() {
				srv.wake()
			}
		}
	}()
	return nil
}

// announceEpoch tells the other nodes a new epoch exists.
func (srv *Service) announceEpoch(ctx context.Context, epoch uint64) {
	if srv.topic == nil {
		return
	}
	if err := srv.topic.Publish(ctx, binary.BigEndian.AppendUint64(nil, epoch)); err != nil {
		serviceLog.Errorf("announcing epoch %d: %s", epoch, err)
	}
}

// background runs fn under the service's lifetime, so Close waits for it rather
// than leaving it to touch a store that is about to shut.
func (srv *Service) background(fn func()) {
	srv.lifecycle.Lock()
	if srv.closed {
		srv.lifecycle.Unlock()
		return
	}
	srv.wg.Add(1)
	srv.lifecycle.Unlock()

	go func() {
		defer srv.wg.Done()
		fn()
	}()
}

// wake asks the reconcile loop to run now, without blocking if it already owes
// a pass.
func (srv *Service) wake() {
	select {
	case srv.nudge <- struct{}{}:
	default:
	}
}

// reconcileLoop keeps the cluster's epochs converged: it rotates when the
// roster changes or the key ages out, carries secrets forward, and retires
// superseded epochs. Every node runs it, and every write it makes is justified
// by a local fact, so they need no turn-taking.
func (srv *Service) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(srv.reconcileEvery)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-srv.nudge:
		}

		if err := srv.reconcile(ctx); err != nil {
			serviceLog.Errorf("reconciling epochs: %s", err)
		}
	}
}

// ActiveKey returns the key this node offers for new secrets: the newest epoch
// it holds a share of.
//
// A node speaks only for itself. It does not count how many other members have
// confirmed anything -- it answers with a key it can actually serve a partial
// for, which is a fact it can check locally and nobody else needs to agree to.
// Two nodes may briefly offer different epochs while a round settles; both are
// readable, so that costs nothing and converges on its own.
func (srv *Service) ActiveKey(ctx context.Context) (*KeyMeta, error) {
	keys, err := srv.held(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, errNoKey
	}
	return keys[0], nil
}

var errNoKey = errors.New("this node holds no distributed key")

// held lists the keys this node can serve partials for, newest epoch first.
func (srv *Service) held(ctx context.Context) ([]*KeyMeta, error) {
	keys, err := srv.keys.Newest(ctx)
	if err != nil {
		return nil, err
	}

	held := make([]*KeyMeta, 0, len(keys))
	for _, key := range keys {
		if _, err := srv.shares.Get(ctx, key.KeyID); err == nil {
			held = append(held, key)
		}
	}

	return held, nil
}

// Set records a secret a client has already encrypted, with its data key
// wrapped to the epoch the client used. The service never sees a plaintext.
func (srv *Service) Set(ctx context.Context, ref Ref, ciphertext []byte, keyID string, wrapping *tdh.Wrapping) error {
	if wrapping == nil {
		return errors.New("missing wrapping")
	}

	key, err := srv.keys.Get(ctx, keyID)
	if err != nil {
		return err
	}

	if err := srv.records.Put(ctx, &Record{
		Project:     ref.Project,
		Environment: ref.Environment,
		Name:        ref.Name,
		Ciphertext:  ciphertext,
		CreatedAt:   srv.now(),
	}); err != nil {
		return err
	}

	return srv.wrappings.Put(ctx, ref, key.KeyID, srv.self(), wrapping)
}

// Get returns a stored secret, still encrypted, with a wrapping this cluster
// can still open. Older epochs stay readable until reconciliation retires them.
func (srv *Service) Get(ctx context.Context, ref Ref) (*Record, *KeyMeta, *tdh.Wrapping, string, error) {
	record, err := srv.records.Get(ctx, ref)
	if err != nil {
		return nil, nil, nil, "", err
	}

	key, wrapping, writer, err := srv.readable(ctx, ref)
	if err != nil {
		return nil, nil, nil, "", err
	}

	return record, key, wrapping, writer, nil
}

// readable finds the newest key this node holds that the secret is wrapped
// under.
//
// Matching is by key id, never by epoch: two keys can share an epoch, so an
// epoch match can pair a wrapping with a group key that did not make it.
func (srv *Service) readable(ctx context.Context, ref Ref) (*KeyMeta, *tdh.Wrapping, string, error) {
	keys, err := srv.held(ctx)
	if err != nil {
		return nil, nil, "", err
	}

	for _, key := range keys {
		wrapping, writer, err := srv.wrappings.Get(ctx, ref, key.KeyID)
		if err != nil {
			continue
		}

		return key, wrapping, writer, nil
	}

	return nil, nil, "", fmt.Errorf("secret %s has no wrapping under a key this node holds", ref)
}

// Partial applies this node's share to a stored secret. It takes a reference
// and a key id, never a ciphertext: a holder must never apply its share to a
// value a caller made up, and never to a key other than the one the caller
// named.
func (srv *Service) Partial(ctx context.Context, ref Ref, keyID, writer string) (*tdh.Partial, error) {
	// The caller names the exact wrapping. Picking one locally would let two
	// nodes answer against different ephemeral keys, and partials taken against
	// different R values combine to nothing.
	wrapping, err := srv.wrappings.GetFrom(ctx, ref, keyID, writer)
	if err != nil {
		return nil, err
	}

	share, err := srv.shares.Get(ctx, keyID)
	if err != nil {
		return nil, fmt.Errorf("this node holds no share of key %s: %w", keyID, err)
	}

	return tdh.PartialDecrypt(share, wrapping)
}

func (srv *Service) self() string {
	return srv.host.ID().String()
}

// Delete removes a secret: its wrappings first, so a half-done delete leaves a
// record nothing can open rather than a wrapping of nothing.
func (srv *Service) Delete(ctx context.Context, ref Ref) error {
	if err := srv.wrappings.DeleteAll(ctx, ref); err != nil {
		return err
	}

	return srv.records.Delete(ctx, ref)
}

// List names the secrets of one project environment.
func (srv *Service) List(ctx context.Context, project, environment string) ([]Ref, error) {
	return srv.records.List(ctx, project, environment)
}

// closing reports whether the service is shutting down, so a handler does not
// write to a closed datastore.
func (srv *Service) closing() error {
	select {
	case <-srv.ctx.Done():
		return fmt.Errorf("service is closing: %w", srv.ctx.Err())
	default:
		return nil
	}
}

// roster is the membership this node can see, itself included.
func (srv *Service) roster(context.Context) ([]string, error) {
	members := make([]string, 0, maxMembers)
	for _, p := range srv.others() {
		members = append(members, p.String())
	}
	members = append(members, srv.self())
	slices.Sort(members)

	return members, nil
}

// others are the other members this node can see, at most maxMembers-1.
func (srv *Service) others() []peerCore.ID {
	var found []peerCore.ID
	if srv.members != nil {
		found = srv.members()
	} else {
		found = srv.speakers()
	}

	out := make([]peerCore.ID, 0, len(found))
	for _, p := range found {
		if p == srv.host.ID() || slices.Contains(out, p) {
			continue
		}
		if len(out) == maxMembers-1 {
			break
		}
		out = append(out, p)
	}
	return out
}

// speakers are the connected peers that advertise this service's protocol.
func (srv *Service) speakers() []peerCore.ID {
	var out []peerCore.ID
	for _, pid := range srv.host.Network().Peers() {
		protos, err := srv.host.Peerstore().GetProtocols(pid)
		if err == nil && slices.Contains(protos, Protocol) {
			out = append(out, pid)
		}
	}
	return out
}

// secretsAt and dealerAt are clients onto another member.
func (srv *Service) secretsAt(to peerCore.ID) (secretsv2connect.SecretsServiceClient, error) {
	c, err := srv.conns.get(to)
	if err != nil {
		return nil, err
	}
	return secretsv2connect.NewSecretsServiceClient(c.HTTP(), c.URL()), nil
}

func (srv *Service) dealerAt(to peerCore.ID) (secretsv2connect.DealerServiceClient, error) {
	c, err := srv.conns.get(to)
	if err != nil {
		return nil, err
	}
	return secretsv2connect.NewDealerServiceClient(c.HTTP(), c.URL()), nil
}

// peers holds one p2p/rpc client per member, opened on first use.
type peers struct {
	host host.Host
	mu   sync.Mutex
	open map[peerCore.ID]*rpc.Client
}

func (p *peers) get(to peerCore.ID) (*rpc.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.open[to]; ok {
		return c, nil
	}
	c, err := rpc.NewClient(p.host, to, Protocol)
	if err != nil {
		return nil, err
	}
	p.open[to] = c
	return c, nil
}

func (p *peers) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, c := range p.open {
		c.Close()
		delete(p.open, id)
	}
}
