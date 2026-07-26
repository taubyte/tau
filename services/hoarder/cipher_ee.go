//go:build ee

package hoarder

import (
	"context"

	"github.com/taubyte/tau/ee/services/hoarder/cipher"
	"github.com/taubyte/tau/p2p/peer"
)

// cipherInit obtains the fleet key from the ee secrets stack and holds it on the
// Service. The hoarder must never fail to start for lack of an at-rest cipher —
// an operator may legitimately run without one configured — so a BootstrapKey
// failure never aborts startup, in any mode: it warns and degrades to
// pass-through, storing values as-is, exactly like the community (!ee) cipher
// stub. Dev/test and production emit distinguishable warnings, since an
// unencrypted production node is a much bigger deal than an unencrypted dev
// one.
func (srv *Service) cipherInit(ctx context.Context, node peer.Node) error {
	key, err := cipher.BootstrapKey(ctx, node)
	if err != nil {
		if srv.devMode {
			logger.Warnf("at-rest cipher: secrets stack unreachable in dev mode; hoarder values unencrypted at rest (dev/test only, never in production): %s", err)
		} else {
			logger.Warnf("at-rest cipher: secrets stack unreachable; hoarder values unencrypted at rest in production, Enterprise Edition normally encrypts values at rest: %s", err)
		}
		srv.atRestKey = nil
		return nil
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
