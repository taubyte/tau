//go:build ee

package hoarder

import (
	"context"

	"github.com/taubyte/tau/ee/services/hoarder/cipher"
	"github.com/taubyte/tau/p2p/peer"
)

// cipherInit obtains the fleet key from the ee secrets stack and holds it on the
// Service. Production (DevMode=false) fails closed — the hoarder never serves
// values it cannot encrypt. In dev/test (DevMode=true) the secrets stack is
// often absent (auth-less fleets), so rather than refuse to start it degrades to
// pass-through: values are stored as-is, exactly like the OSS (!ee) cipher stub.
// It never stores plaintext when DevMode is false.
func (srv *Service) cipherInit(ctx context.Context, node peer.Node) error {
	key, err := cipher.BootstrapKey(ctx, node)
	if err != nil {
		if srv.devMode {
			logger.Warnf("at-rest cipher: secrets stack unreachable in dev mode; storing values unencrypted like the OSS build (dev/test only, never in production): %s", err)
			srv.atRestKey = nil
			return nil
		}
		return err
	}
	srv.atRestKey = key
	return nil
}

func (srv *Service) cipherEncrypt(value []byte) ([]byte, error) {
	if srv.atRestKey == nil {
		return value, nil // dev pass-through (see cipherInit)
	}
	return cipher.Encrypt(srv.atRestKey, value)
}

func (srv *Service) cipherDecrypt(value []byte) ([]byte, error) {
	if srv.atRestKey == nil {
		return value, nil // dev pass-through (see cipherInit)
	}
	return cipher.Decrypt(srv.atRestKey, value)
}
