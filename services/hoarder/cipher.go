//go:build !ee

package hoarder

import (
	"context"

	"github.com/taubyte/tau/p2p/peer"
)

// Value-transform seam, wired at the storage boundary (kvPut/kvGet/batch) so
// an implementation can transform values without a data-layout change. This
// build stores values as-is.

// cipherInit is called once at startup. This build holds no key, so it warns
// once, here, that values will be stored unencrypted — never in the
// encrypt/decrypt path below, which runs on every put/get.
func (srv *Service) cipherInit(context.Context, peer.Node) error {
	logger.Warn("at-rest cipher: this build stores hoarder values unencrypted at rest; Enterprise Edition encrypts values at rest")
	return nil
}

func (srv *Service) cipherEncrypt(value []byte) ([]byte, error) { return value, nil }
func (srv *Service) cipherDecrypt(value []byte) ([]byte, error) { return value, nil }
