package config

import (
	"errors"

	"github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/go-interfaces/p2p/keypair"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	spec "github.com/taubyte/go-specs/common"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
)

var (
	DatabasePath           string = "/tb/storage/databases/"
	DefaultP2PListenFormat string = "/ip4/0.0.0.0/tcp/%d"
	DefaultHTTPListen      string = "0.0.0.0:443"
	DefaultCAFileName      string = "/tb/priv/fullchain.pem"
	DefaultKeyFileName     string = "/tb/priv/privkey.pem"
)

type Protocol struct {
	Root      string
	Shape     string
	Branch    string
	Protocols []string

	Peers           []string
	P2PListen       []string
	P2PAnnounce     []string
	Ports           map[string]int // TODO: use a struct
	Location        *seerIface.Location
	NetworkUrl      string
	HttpListen      string
	GeneratedDomain string
	ServicesDomain  string

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

type ConfigBuilder struct {
	// generic
	DefaultP2PListenPort int
	DevP2PListenFormat   string

	// http
	DevHttpListenPort int
}

func (config *Protocol) Validate() error {
	if config == nil {
		config = &Protocol{}
		config.PrivateKey = nil
		config.SwarmKey = nil
		config.DevMode = false
	}

	if config.Root == "" {
		config.Root = DatabasePath
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

	if config.Branch == "" {
		config.Branch = spec.DefaultBranch
	}

	return nil
}
