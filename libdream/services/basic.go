package services

import (
	"fmt"

	commonIface "github.com/taubyte/go-interfaces/common"
	"github.com/taubyte/go-interfaces/services/auth"
	"github.com/taubyte/go-interfaces/services/hoarder"
	"github.com/taubyte/go-interfaces/services/monkey"
	"github.com/taubyte/go-interfaces/services/patrick"
	"github.com/taubyte/go-interfaces/services/seer"
	"github.com/taubyte/go-interfaces/services/tns"
	"github.com/taubyte/tau/libdream/common"
)

const (
	basicClientName = "client"
)

type basicMultiverse struct {
	multiverse common.Universe
	config     *common.Config
	clients    common.SimpleConfigClients
}

func BasicMultiverse(name string) *basicMultiverse {
	config := &common.Config{
		Services: make(map[string]commonIface.ServiceConfig),
	}

	return &basicMultiverse{
		multiverse: Multiverse(name),
		clients:    common.SimpleConfigClients{},
		config:     config,
	}
}

func (b *basicMultiverse) start() (common.Simple, error) {
	b.config.Simples = map[string]common.SimpleConfig{
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

func (b *basicMultiverse) Auth() (common.Universe, auth.Client, error) {
	b.Service("auth")
	b.clients.Auth = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "auth", err)
	}

	return b.multiverse, simple.Auth(), nil
}

func (b *basicMultiverse) Hoarder() (common.Universe, hoarder.Client, error) {
	b.Service("hoarder")
	b.clients.Hoarder = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "hoarder", err)
	}

	return b.multiverse, simple.Hoarder(), nil
}
func (b *basicMultiverse) Monkey() (common.Universe, monkey.Client, error) {
	b.Service("monkey")
	b.clients.Monkey = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "monkey", err)
	}

	return b.multiverse, simple.Monkey(), nil
}
func (b *basicMultiverse) Patrick() (common.Universe, patrick.Client, error) {
	b.Service("patrick")
	b.clients.Patrick = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "patrick", err)
	}

	return b.multiverse, simple.Patrick(), nil
}
func (b *basicMultiverse) Seer() (common.Universe, seer.Client, error) {
	b.Service("seer")
	b.clients.Seer = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "seer", err)
	}

	return b.multiverse, simple.Seer(), nil
}

func (b *basicMultiverse) Tns() (common.Universe, tns.Client, error) {
	b.Service("tns")
	b.clients.TNS = &commonIface.ClientConfig{}

	simple, err := b.start()
	if err != nil {
		return nil, nil, fmt.Errorf("getting %s client failed with: %s", "tns", err)
	}

	return b.multiverse, simple.TNS(), nil
}
