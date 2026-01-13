//go:build !ee

package auth

import (
	kv "github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/p2p/peer"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

// initSecretsService returns nil for non-EE builds
func initSecretsService(db kv.KVDB, node peer.Node, nodePath string) (iface.AuthServiceSecretManager, error) {
	return nil, nil
}

// attachSecretsServiceStreams is a noop for non-EE builds
func attachSecretsServiceStreams(secretsService iface.AuthServiceSecretManager, streamService peerService.CommandService) {
	// No-op: secrets service is not available in non-EE builds
}
