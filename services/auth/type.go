package auth

import (
	"context"

	kv "github.com/taubyte/tau/core/kvdb"
	"github.com/taubyte/tau/p2p/peer"
	streams "github.com/taubyte/tau/p2p/streams/service"

	http "github.com/taubyte/tau/pkg/http"

	accountsIface "github.com/taubyte/tau/core/services/accounts"
	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/core/services/tns"
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

	rootDomain string
	devMode    bool
	webHookUrl string

	dvPrivateKey []byte
	dvPublicKey  []byte

	hostUrl string

	newGitHubClient func(context.Context, string) (GitHubClient, error)

	secretsService iface.AuthServiceSecretManager

	// accountsClient (when non-nil) is consulted by GitHubTokenHTTPAuth after
	// validating a github token to enforce the universal "no tau account
	// linked" rule. Nil when Accounts.VerifyOnAuth = false (community + dream
	// tests) or when the accounts service isn't reachable at startup.
	accountsClient accountsIface.Client
	accountsURL    string
}

func (s *AuthService) Node() peer.Node {
	return s.node
}

func (s *AuthService) KV() kv.KVDB {
	return s.db
}
