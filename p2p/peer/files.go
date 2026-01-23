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
	if p.closed.Load() {
		return errorClosed
	}

	_cid, err := cid.Decode(id)
	if err != nil {
		return err
	}

	return p.ipfs.Remove(p.ctx, _cid)
}

func (p *node) AddFile(r io.Reader) (_cid string, err error) {
	if p.closed.Load() {
		return "", errorClosed
	}

	var n ipld.Node
	n, err = p.ipfs.AddFile(p.ctx, r, nil)
	if err == nil {
		_cid = n.Cid().String()
	}
	return
}

// Note: context should have a timeout and depend on the peer context as parent
func (p *node) GetFile(ctx context.Context, id string) (ReadSeekCloser, error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	_cid, err := cid.Decode(id)
	if err != nil {
		return nil, err
	}
	return p.ipfs.GetFile(ctx, _cid)
}

func (p *node) GetFileFromCid(ctx context.Context, cid cid.Cid) (ReadSeekCloser, error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	return p.ipfs.GetFile(ctx, cid)
}

func (p *node) AddFileForCid(r io.Reader) (cid.Cid, error) {
	if p.closed.Load() {
		return cid.Cid{}, errorClosed
	}

	n, err := p.ipfs.AddFile(p.ctx, r, nil)
	if err != nil {
		return cid.Cid{}, err
	}

	return n.Cid(), nil
}
