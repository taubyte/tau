package ipfs

import (
	"context"
	"io"

	"github.com/ipfs/go-cid"
	peer "github.com/taubyte/tau/p2p/peer"
)

type Service interface {
	GetFile(ctx context.Context, cid cid.Cid) (peer.ReadSeekCloser, error)
	AddFile(r io.Reader) (cid.Cid, error)
	Close()
}
