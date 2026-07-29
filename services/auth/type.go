package auth

import (
	"context"

	kv "github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"

	http "github.com/taubyte/tau/pkg/http"

	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/core/services/tns"
	tauConfig "github.com/taubyte/tau/pkg/config"
)

var _ iface.Service = &AuthService{}

type AuthService struct {
	ctx       context.Context
	node      peer.Node
	db        kv.KVDB
	http      http.Service
	stream    streams.CommandService
	tnsClient tns.Client
	dbFactory kv.Factory

	config     tauConfig.Config
	devMode    bool
	webHookUrl string

	dvPrivateKey []byte
	dvPublicKey  []byte

	newGitHubClient func(context.Context, string) (GitHubClient, error)

	// identityClientNode is the node initIdentity builds its client on, when
	// the build needs one.
	identityClientNode peer.Node

	// Set only by the build that answers identity through a separate service;
	// see identity_ee.go. The type is aliased per build so this file names
	// nothing that build alone provides. Left zero otherwise.
	accountsClient identityClient
	accountsURL    string

	// tenancy names the namespace that owns this cloud; membership answers
	// whether a caller belongs to it. Both are set by the build that answers
	// identity from the namespace; see identity.go, which refuses to start
	// outside dev mode unless the tenancy is configured and usable.
	tenancy    tauConfig.Tenancy
	membership MembershipVerifier
}

func (s *AuthService) Node() peer.Node {
	return s.node
}

func (s *AuthService) KV() kv.KVDB {
	return s.db
}
