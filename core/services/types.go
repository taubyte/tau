package services

import (
	http "github.com/taubyte/http"
	peer "github.com/taubyte/p2p/peer"
	"github.com/taubyte/tau/core/kvdb"
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
