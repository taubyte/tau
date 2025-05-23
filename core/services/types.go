package services

import (
	"github.com/taubyte/tau/core/kvdb"
	peer "github.com/taubyte/tau/p2p/peer"
	http "github.com/taubyte/tau/pkg/http"
)

type Service interface {
	Node() peer.Node
	Close() error
}

type DBService interface {
	Service
	KV() kvdb.KVDB
}

type HttpService interface {
	Service
	Http() http.Service
}

type GitHubAuth interface {
	GitHubTokenHTTPAuth(ctx http.Context) (interface{}, error)
	GitHubTokenHTTPAuthCleanup(ctx http.Context) (interface{}, error)
}
