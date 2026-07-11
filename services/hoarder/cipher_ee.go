//go:build ee

package hoarder

import (
	"context"

	"github.com/taubyte/tau/ee/services/hoarder/cipher"
	"github.com/taubyte/tau/p2p/peer"
)

// cipherInit obtains the fleet key at startup and holds it on the Service;
// startup fails if it cannot be obtained.
func (srv *Service) cipherInit(ctx context.Context, node peer.Node) error {
	key, err := cipher.BootstrapKey(ctx, node)
	if err != nil {
		return err
	}
	srv.atRestKey = key
	return nil
}

func (srv *Service) cipherEncrypt(value []byte) ([]byte, error) {
	return cipher.Encrypt(srv.atRestKey, value)
}

func (srv *Service) cipherDecrypt(value []byte) ([]byte, error) {
	return cipher.Decrypt(srv.atRestKey, value)
}
