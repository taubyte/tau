//go:build ee

package auth

import (
	"fmt"

	kv "github.com/taubyte/tau/core/kvdb"
	iface "github.com/taubyte/tau/core/services/auth"
	"github.com/taubyte/tau/ee/services/auth/secrets"
	"github.com/taubyte/tau/p2p/peer"
	peerService "github.com/taubyte/tau/p2p/streams/service"
)

func initSecretsService(db kv.KVDB, node peer.Node, nodePath string) (iface.AuthServiceSecretManager, error) {
	secretsService, err := secrets.New(db, node, nodePath)
	if err != nil {
		return nil, fmt.Errorf("creating secrets service: %w", err)
	}
	return secretsService, nil
}

func attachSecretsServiceStreams(secretsService iface.AuthServiceSecretManager, streamService peerService.CommandService) {
	secretsService.AttachStreams(streamService)
}
