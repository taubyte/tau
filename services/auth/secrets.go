//go:build !ee

package auth

import (
	kv "github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/peer"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

func initSecretsService(db kv.KVDB, node peer.Node, nodePath string) (iface.AuthServiceSecretManager, error) {
	return nil, nil
}

func attachSecretsServiceStreams(secretsService iface.AuthServiceSecretManager, streamService peerService.CommandService) {
}
