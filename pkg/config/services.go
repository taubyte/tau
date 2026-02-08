package config

import (
	"crypto"
	"crypto/x509"
	"errors"
	"regexp"

	"github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/core/p2p/keypair"
	seerIface "github.com/taubyte/tau/core/services/seer"
	"github.com/taubyte/tau/p2p/peer"
	http "github.com/taubyte/tau/pkg/http"
	"github.com/taubyte/tau/pkg/raft"
	"github.com/taubyte/tau/pkg/sensors"
)

var (
	DefaultRoot            = "/tb"
	DefaultP2PListenFormat = "/ip4/0.0.0.0/tcp/%d"
	DefaultHTTPListen      = "0.0.0.0:443"
)

// Config is the validated node configuration. Created via New; mutable fields can be set by bootstrap (e.g. SetNode, SetRaftCluster).
type Config interface {
	Root() string
	Shape() string
	Services() []string
	Cluster() string
	Peers() []string
	P2PListen() []string
	P2PAnnounce() []string
	Ports() map[string]int
	Location() *seerIface.Location
	NetworkFqdn() string
	GeneratedDomain() string
	AliasDomains() []string
	HttpListen() string
	AliasDomainsRegExp() []*regexp.Regexp
	GeneratedDomainRegExp() *regexp.Regexp
	ServicesDomainRegExp() *regexp.Regexp
	ServicesDomainMatch(s string) bool
	AliasDomainsMatch(dom string) bool
	GeneratedDomainMatch(s string) bool
	CustomAcme() bool
	AcmeUrl() string
	AcmeCAARecord() string
	AcmeKey() crypto.Signer
	AcmeCAInsecureSkipVerify() bool
	AcmeRootCA() *x509.CertPool
	Node() peer.Node
	PrivateKey() []byte
	Databases() kvdb.Factory
	RaftCluster() raft.Cluster
	ClientNode() peer.Node
	SwarmKey() []byte
	Http() http.Service
	Sensors() *sensors.Service
	EnableHTTPS() bool
	Verbose() bool
	DevMode() bool
	Plugins() Plugins
	DomainValidation() DomainValidation
	SensorsRegistry() *sensors.Registry

	SetNode(peer.Node)
	SetRaftCluster(raft.Cluster)
	SetClientNode(peer.Node)
	SetDatabases(kvdb.Factory)
	SetHttp(http.Service)
	SetSensors(*sensors.Service)
}

// Option applies a setting during construction and may validate it.
type Option func(*config) error

// WithRoot sets the node root directory. Validates root is non-empty.
func WithRoot(root string) Option {
	return func(c *config) error {
		if root == "" {
			return errors.New("root cannot be empty")
		}
		c.root = root
		return nil
	}
}

// WithP2PListen sets the p2p listen addresses. Validates at least one address.
func WithP2PListen(addrs []string) Option {
	return func(c *config) error {
		if len(addrs) == 0 {
			return errors.New("p2p listen must have at least one address")
		}
		c.p2pListen = addrs
		return nil
	}
}

// WithP2PAnnounce sets the p2p announce addresses. Validates at least one address.
func WithP2PAnnounce(addrs []string) Option {
	return func(c *config) error {
		if len(addrs) == 0 {
			return errors.New("p2p announce must have at least one address")
		}
		c.p2pAnnounce = addrs
		return nil
	}
}

// WithSwarmKey sets the swarm key.
func WithSwarmKey(key []byte) Option {
	return func(c *config) error {
		c.swarmKey = key
		return nil
	}
}

// WithDevMode sets dev mode (default true when using New).
func WithDevMode(dev bool) Option {
	return func(c *config) error {
		c.devMode = dev
		return nil
	}
}

// WithPrivateKey sets the node private key. Validates non-empty when DevMode is false.
func WithPrivateKey(key []byte) Option {
	return func(c *config) error {
		if !c.devMode && len(key) == 0 {
			return errors.New("private key required when not in dev mode")
		}
		c.privateKey = key
		return nil
	}
}

// WithCluster sets the cluster name.
func WithCluster(cluster string) Option {
	return func(c *config) error {
		c.cluster = cluster
		return nil
	}
}

// WithPorts sets the ports map.
func WithPorts(ports map[string]int) Option {
	return func(c *config) error {
		c.ports = ports
		return nil
	}
}

// WithHttpListen sets the HTTP listen address.
func WithHttpListen(addr string) Option {
	return func(c *config) error {
		c.httpListen = addr
		return nil
	}
}

// WithVerbose sets verbose mode.
func WithVerbose(v bool) Option {
	return func(c *config) error {
		c.verbose = v
		return nil
	}
}

// WithEnableHTTPS sets HTTPS mode.
func WithEnableHTTPS(v bool) Option {
	return func(c *config) error {
		c.enableHTTPS = v
		return nil
	}
}

// WithCustomAcme sets custom ACME mode.
func WithCustomAcme(v bool) Option {
	return func(c *config) error {
		c.customAcme = v
		return nil
	}
}

// WithAcmeUrl sets the ACME directory URL.
func WithAcmeUrl(url string) Option {
	return func(c *config) error {
		c.acmeUrl = url
		return nil
	}
}

// WithAcmeKey sets the ACME account key (crypto.Signer).
func WithAcmeKey(key crypto.Signer) Option {
	return func(c *config) error {
		c.acmeKey = key
		return nil
	}
}

// WithDomainValidation sets domain validation keys.
func WithDomainValidation(dv DomainValidation) Option {
	return func(c *config) error {
		c.domainValidation = dv
		return nil
	}
}

// WithNetworkFqdn sets the network FQDN.
func WithNetworkFqdn(fqdn string) Option {
	return func(c *config) error {
		c.networkFqdn = fqdn
		return nil
	}
}

// WithGeneratedDomainRegExp sets the generated domain regex.
func WithGeneratedDomainRegExp(r *regexp.Regexp) Option {
	return func(c *config) error {
		c.generatedDomainRegExp = r
		return nil
	}
}

// WithServicesDomainRegExp sets the services domain regex.
func WithServicesDomainRegExp(r *regexp.Regexp) Option {
	return func(c *config) error {
		c.servicesDomainRegExp = r
		return nil
	}
}

// WithAliasDomainsRegExp sets the alias domains regex list.
func WithAliasDomainsRegExp(r []*regexp.Regexp) Option {
	return func(c *config) error {
		c.aliasDomainsRegExp = r
		return nil
	}
}

// WithPeers sets the bootstrap peers.
func WithPeers(peers []string) Option {
	return func(c *config) error {
		c.peers = peers
		return nil
	}
}

// WithLocation sets the location.
func WithLocation(loc *seerIface.Location) Option {
	return func(c *config) error {
		c.location = loc
		return nil
	}
}

// WithPlugins sets the plugins.
func WithPlugins(p Plugins) Option {
	return func(c *config) error {
		c.plugins = p
		return nil
	}
}

// New returns a validated config. Defaults are dev-friendly; override with options.
func New(opts ...Option) (Config, error) {
	c := &config{
		devMode:     true,
		root:        DefaultRoot,
		p2pListen:   []string{"/ip4/127.0.0.1/tcp/0"},
		p2pAnnounce: []string{"/ip4/127.0.0.1/tcp/0"},
	}
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return nil, err
		}
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return c, nil
}

type config struct {
	root     string
	shape    string
	services []string
	cluster  string

	peers           []string
	p2pListen       []string
	p2pAnnounce     []string
	ports           map[string]int
	location        *seerIface.Location
	networkFqdn     string
	generatedDomain string
	aliasDomains    []string

	httpListen string

	aliasDomainsRegExp    []*regexp.Regexp
	generatedDomainRegExp *regexp.Regexp
	servicesDomainRegExp  *regexp.Regexp

	customAcme               bool
	acmeUrl                  string
	acmeCAARecord            string
	acmeKey                  crypto.Signer
	acmeCAInsecureSkipVerify bool
	acmeRootCA               *x509.CertPool

	node             peer.Node
	privateKey       []byte
	databases        kvdb.Factory
	raftCluster      raft.Cluster
	clientNode       peer.Node
	swarmKey         []byte
	http             http.Service
	sensors          *sensors.Service
	enableHTTPS      bool
	verbose          bool
	devMode          bool
	plugins          Plugins
	domainValidation DomainValidation
}

func (c *config) Root() string                          { return c.root }
func (c *config) Shape() string                         { return c.shape }
func (c *config) Services() []string                    { return c.services }
func (c *config) Cluster() string                       { return c.cluster }
func (c *config) Peers() []string                       { return c.peers }
func (c *config) P2PListen() []string                   { return c.p2pListen }
func (c *config) P2PAnnounce() []string                 { return c.p2pAnnounce }
func (c *config) Ports() map[string]int                 { return c.ports }
func (c *config) Location() *seerIface.Location         { return c.location }
func (c *config) NetworkFqdn() string                   { return c.networkFqdn }
func (c *config) GeneratedDomain() string               { return c.generatedDomain }
func (c *config) AliasDomains() []string                { return c.aliasDomains }
func (c *config) HttpListen() string                    { return c.httpListen }
func (c *config) AliasDomainsRegExp() []*regexp.Regexp  { return c.aliasDomainsRegExp }
func (c *config) GeneratedDomainRegExp() *regexp.Regexp { return c.generatedDomainRegExp }
func (c *config) ServicesDomainRegExp() *regexp.Regexp  { return c.servicesDomainRegExp }

func (c *config) ServicesDomainMatch(s string) bool {
	if c.servicesDomainRegExp == nil {
		return false
	}
	return c.servicesDomainRegExp.MatchString(s)
}

func (c *config) AliasDomainsMatch(dom string) bool {
	for _, r := range c.aliasDomainsRegExp {
		if r != nil && r.MatchString(dom) {
			return true
		}
	}
	return false
}

func (c *config) GeneratedDomainMatch(s string) bool {
	if c.generatedDomainRegExp == nil {
		return false
	}
	return c.generatedDomainRegExp.MatchString(s)
}

func (c *config) CustomAcme() bool                   { return c.customAcme }
func (c *config) AcmeUrl() string                    { return c.acmeUrl }
func (c *config) AcmeCAARecord() string              { return c.acmeCAARecord }
func (c *config) AcmeKey() crypto.Signer             { return c.acmeKey }
func (c *config) AcmeCAInsecureSkipVerify() bool     { return c.acmeCAInsecureSkipVerify }
func (c *config) AcmeRootCA() *x509.CertPool         { return c.acmeRootCA }
func (c *config) Node() peer.Node                    { return c.node }
func (c *config) PrivateKey() []byte                 { return c.privateKey }
func (c *config) Databases() kvdb.Factory            { return c.databases }
func (c *config) RaftCluster() raft.Cluster          { return c.raftCluster }
func (c *config) ClientNode() peer.Node              { return c.clientNode }
func (c *config) SwarmKey() []byte                   { return c.swarmKey }
func (c *config) Http() http.Service                 { return c.http }
func (c *config) Sensors() *sensors.Service          { return c.sensors }
func (c *config) EnableHTTPS() bool                  { return c.enableHTTPS }
func (c *config) Verbose() bool                      { return c.verbose }
func (c *config) DevMode() bool                      { return c.devMode }
func (c *config) Plugins() Plugins                   { return c.plugins }
func (c *config) DomainValidation() DomainValidation { return c.domainValidation }

func (c *config) SetNode(n peer.Node)            { c.node = n }
func (c *config) SetRaftCluster(rc raft.Cluster) { c.raftCluster = rc }
func (c *config) SetClientNode(n peer.Node)      { c.clientNode = n }
func (c *config) SetDatabases(db kvdb.Factory)   { c.databases = db }
func (c *config) SetHttp(h http.Service)         { c.http = h }
func (c *config) SetSensors(s *sensors.Service)  { c.sensors = s }

func (c *config) SensorsRegistry() *sensors.Registry {
	if c.sensors != nil {
		return c.sensors.Registry()
	}
	return nil
}

func (c *config) validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.root == "" {
		c.root = DefaultRoot
	}
	if c.cluster == "" {
		c.cluster = "main"
	}
	if c.httpListen == "" && !c.devMode {
		c.httpListen = DefaultHTTPListen
	}
	if len(c.p2pListen) == 0 {
		return errors.New("you must define p2p port")
	}
	if c.p2pAnnounce == nil {
		return errors.New("you must define p2p announce")
	}
	if len(c.privateKey) == 0 {
		if c.devMode {
			c.privateKey = keypair.NewRaw()
		} else {
			return errors.New("you must provide node private key")
		}
	}
	return nil
}

type DomainValidation struct {
	PrivateKey []byte
	PublicKey  []byte
}
