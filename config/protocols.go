package config

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/taubyte/go-interfaces/p2p/keypair"
	seerIface "github.com/taubyte/go-interfaces/services/seer"
	spec "github.com/taubyte/go-specs/common"
	http "github.com/taubyte/http"
	"github.com/taubyte/p2p/peer"
	"github.com/taubyte/utils/env"
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
	Ports           map[string]int
	Location        *seerIface.Location
	NetworkUrl      string
	HttpListen      string
	GeneratedDomain string
	ServicesDomain  string

	Node       peer.Node
	PrivateKey []byte

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

func (config *Protocol) Build(builder ConfigBuilder) error {
	if config == nil {
		config = &Protocol{}
		config.PrivateKey = nil
		config.SwarmKey = nil
		config.DevMode = false
	}

	if config.Root == "" {
		config.Root = DatabasePath
	}

	config.buildHttp(builder)
	config.buildP2P(builder)

	err := config.buildLocation(builder)
	if err != nil {
		return fmt.Errorf("building location failed with: %s", err)
	}

	err = config.buildKeys(builder)
	if err != nil {
		return fmt.Errorf("building keys failed with: %s", err)
	}

	if config.Branch == "" {
		config.Branch = spec.DefaultBranch
	}

	return nil
}

func (config *Protocol) buildHttp(builder ConfigBuilder) {
	if config.DevMode {
		if config.HttpListen == "" {
			config.HttpListen = fmt.Sprintf("0.0.0.0:%d", builder.DevHttpListenPort)
		}
	}

	if config.HttpListen == "" {
		config.HttpListen = DefaultHTTPListen
	}
}

func (config *Protocol) buildP2P(builder ConfigBuilder) {
	if len(config.P2PListen) == 0 {
		config.P2PListen = []string{fmt.Sprintf(DefaultP2PListenFormat, builder.DefaultP2PListenPort)}
	}

	if config.P2PAnnounce == nil {
		if config.DevMode {
			listenAddrFmt := builder.DevP2PListenFormat
			config.P2PAnnounce = []string{fmt.Sprintf(listenAddrFmt, builder.DefaultP2PListenPort)}

		} else {
			listenAddrFmt, err := env.Get("TAUBYTE_P2P_LISTEN")
			if err != nil {
				panic("No Address to announce")
			}
			config.P2PAnnounce = []string{fmt.Sprintf(listenAddrFmt, builder.DefaultP2PListenPort)}
		}
	}

}

func (config *Protocol) buildKeys(builder ConfigBuilder) error {
	if len(config.PrivateKey) == 0 {
		if config.DevMode {
			config.PrivateKey = keypair.NewRaw()
		}
	}

	envKey := keypair.LoadRawFromEnv()
	if envKey != nil {
		config.PrivateKey = envKey
	}

	if len(config.SwarmKey) == 0 {
		return errors.New("swarm key is needed. Generate one using spore-drive if you dont have one")
	}

	return nil
}

func (config *Protocol) buildLocation(builder ConfigBuilder) error {
	if config.Location == nil {
		_locationJSON, err := env.Get("TAUBYTE_GEO_LOCATION")
		if err == nil {
			config.Location = &seerIface.Location{}
			err = json.Unmarshal([]byte(_locationJSON), config.Location)
			if err != nil {
				return fmt.Errorf("parsing location failed with: %s", err)
			}
		}
	}

	return nil
}
