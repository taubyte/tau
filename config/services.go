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
)

var (
	DefaultRoot            = "/tb"
	DefaultP2PListenFormat = "/ip4/0.0.0.0/tcp/%d"
	DefaultHTTPListen      = "0.0.0.0:443"
)

type Node struct {
	Root     string
	Shape    string
	Services []string

	Peers           []string
	P2PListen       []string
	P2PAnnounce     []string
	Ports           map[string]int // TODO: use a struct
	Location        *seerIface.Location
	NetworkFqdn     string
	GeneratedDomain string
	AliasDomains    []string

	HttpListen string

	AliasDomainsRegExp    []*regexp.Regexp
	GeneratedDomainRegExp *regexp.Regexp
	ServicesDomainRegExp  *regexp.Regexp

	CustomAcme               bool
	AcmeUrl                  string
	AcmeKey                  crypto.Signer
	AcmeCAInsecureSkipVerify bool
	AcmeRootCA               *x509.CertPool

	Node       peer.Node
	PrivateKey []byte
	Databases  kvdb.Factory

	ClientNode peer.Node

	SwarmKey []byte

	Http http.Service

	EnableHTTPS bool
	Verbose     bool
	DevMode     bool

	Plugins
	DomainValidation DomainValidation
}

type DomainValidation struct {
	PrivateKey []byte
	PublicKey  []byte
}

func (config *Node) Validate() error {
	if config == nil {
		config = &Node{}
		config.PrivateKey = nil
		config.SwarmKey = nil
		config.DevMode = false
	}

	if config.Root == "" {
		config.Root = DefaultRoot
	}

	// http
	if config.HttpListen == "" {
		if !config.DevMode {
			config.HttpListen = DefaultHTTPListen
		}
	}

	// p2p
	if len(config.P2PListen) == 0 {
		return errors.New("you must define p2p port")
	}

	if config.P2PAnnounce == nil {
		return errors.New("you must define p2p announce")
	}

	if len(config.PrivateKey) == 0 {
		if config.DevMode {
			config.PrivateKey = keypair.NewRaw()
		} else {
			return errors.New("you must provide node private key")
		}
	}

	return nil
}
