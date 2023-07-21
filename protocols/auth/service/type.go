package service

import (
	"context"

	kv "github.com/taubyte/go-interfaces/kvdb"
	"github.com/taubyte/odo/config"
	"github.com/taubyte/p2p/peer"
	streams "github.com/taubyte/p2p/streams/service"

	http "github.com/taubyte/http"

	iface "github.com/taubyte/go-interfaces/services/auth"
	"github.com/taubyte/go-interfaces/services/tns"
)

var _ iface.Service = &AuthService{}

type AuthService struct {
	ctx       context.Context
	node      peer.Node
	db        kv.KVDB
	http      http.Service
	stream    *streams.CommandService
	tnsClient tns.Client

	rootDomain string
	devMode    bool
	webHookUrl string

	dvPrivateKey []byte
	dvPublicKey  []byte

	hostUrl string
}

func (s *AuthService) Node() peer.Node {
	return s.node
}

func (s *AuthService) KV() kv.KVDB {
	return s.db
}

type Config struct {
	config.Protocol `yaml:"z,omitempty"`
}
