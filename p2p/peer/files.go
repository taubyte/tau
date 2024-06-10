package peer

import (
	"context"
	"errors"
	"io"

	cid "github.com/ipfs/go-cid"
	ipld "github.com/ipfs/go-ipld-format"
)

type ReadSeekCloser interface {
	io.ReadSeekCloser
	io.WriterTo
}

var errorClosed = errors.New("node is closed")

func (p *node) DeleteFile(id string) error {
	if !p.closed {
		_cid, err := cid.Decode(id)
		if err != nil {
			return err
		}

		return p.ipfs.Remove(p.ctx, _cid)
	}

	return errorClosed
}

func (p *node) AddFile(r io.Reader) (_cid string, err error) {
	if !p.closed {
		var n ipld.Node
		n, err = p.ipfs.AddFile(p.ctx, r, nil)
		if err == nil {
			_cid = n.Cid().String()
		}
		return
	}

	err = errorClosed
	return
}

// Note: context should have a timeout and depend on the peer context as parent
func (p *node) GetFile(ctx context.Context, id string) (ReadSeekCloser, error) {
	if !p.closed {
		_cid, err := cid.Decode(id)
		if err != nil {
			return nil, err
		}
		return p.ipfs.GetFile(ctx, _cid)
	}

	return nil, errorClosed
}

func (p *node) GetFileFromCid(ctx context.Context, cid cid.Cid) (ReadSeekCloser, error) {
	if !p.closed {
		return p.ipfs.GetFile(ctx, cid)
	}

	return nil, errorClosed
}

func (p *node) AddFileForCid(r io.Reader) (cid.Cid, error) {
	if !p.closed {
		n, err := p.ipfs.AddFile(p.ctx, r, nil)
		if err != nil {
			return cid.Cid{}, err
		}

		return n.Cid(), nil
	}

	return cid.Cid{}, errorClosed
}
