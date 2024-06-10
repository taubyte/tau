package ipfs

import (
	"context"
	"io"

	"github.com/ipfs/go-cid"
	"github.com/taubyte/tau/p2p/peer"
)

func (s *Service) AddFile(r io.Reader) (cid.Cid, error) {
	return s.Node.AddFileForCid(r)
}

func (s *Service) GetFile(ctx context.Context, cid cid.Cid) (peer.ReadSeekCloser, error) {
	return s.Node.GetFileFromCid(ctx, cid)
}
