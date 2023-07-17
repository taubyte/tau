package service

import (
	"context"

	streams "bitbucket.org/taubyte/p2p/streams/service"
	kv "github.com/taubyte/go-interfaces/kvdb"
	peer "github.com/taubyte/go-interfaces/p2p/peer"

	"github.com/taubyte/go-interfaces/services/http"

	iface "github.com/taubyte/go-interfaces/services/auth"
	commonIface "github.com/taubyte/go-interfaces/services/common"
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
	commonIface.GenericConfig `yaml:"z,omitempty"`
}
