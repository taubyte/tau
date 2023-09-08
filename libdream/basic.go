package libdream

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/auth"
	"github.com/taubyte/go-interfaces/services/hoarder"
	"github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/go-interfaces/services/tns"
)

const (
	basicClientName = "client"
)

type basicMultiverse struct {
	multiverse *Universe
	config     *Config
	clients    SimpleConfigClients
}

func BasicMultiverse(name string) *basicMultiverse {
	config := &Config{
		Services: make(map[string]commonIface.ServiceConfig),
	}

	return &basicMultiverse{
		multiverse: NewUniverse(UniverseConfig{Name: name}),
		clients:    SimpleConfigClients{},
		config:     config,
	}
}

func (b *basicMultiverse) start() (*Simple, error) {
	b.config.Simples = map[string]SimpleConfig{
		basicClientName: {
			Clients: b.clients,
		},
	}

	err := b.multiverse.StartWithConfig(b.config)
	if err != nil {
		return nil, err
	}

	simple, err := b.multiverse.Simple(basicClientName)
	if err != nil {
		return nil, err
	}

	return simple, err
}

func (b *basicMultiverse) Service(name string) {
	b.config.Services[name] = commonIface.ServiceConfig{}
}

func (b *basicMultiverse) Auth() (*Universe, auth.Client, error) {
	b.Service("auth")
	b.clients.Auth = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "auth", err)
	}

	return b.multiverse, simple.Auth(), nil
}

func (b *basicMultiverse) Hoarder() (*Universe, hoarder.Client, error) {
	b.Service("hoarder")
	b.clients.Hoarder = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "hoarder", err)
	}

	return b.multiverse, simple.Hoarder(), nil
}
func (b *basicMultiverse) Monkey() (*Universe, monkey.Client, error) {
	b.Service("monkey")
	b.clients.Monkey = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "monkey", err)
	}

	return b.multiverse, simple.Monkey(), nil
}
func (b *basicMultiverse) Patrick() (*Universe, patrick.Client, error) {
	b.Service("patrick")
	b.clients.Patrick = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "patrick", err)
	}

	return b.multiverse, simple.Patrick(), nil
}
func (b *basicMultiverse) Seer() (*Universe, seer.Client, error) {
	b.Service("seer")
	b.clients.Seer = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "seer", err)
	}

	return b.multiverse, simple.Seer(), nil
}

func (b *basicMultiverse) Tns() (*Universe, tns.Client, error) {
	b.Service("tns")
	b.clients.TNS = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "tns", err)
	}

	return b.multiverse, simple.TNS(), nil
}
