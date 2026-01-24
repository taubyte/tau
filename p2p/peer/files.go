package peer

import (
	"context"
	"errors"
	"fmt"
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
		return fmt.Errorf("decoding CID %q failed: %w", id, err)
	}

	if err := p.ipfs.Remove(p.ctx, _cid); err != nil {
		return fmt.Errorf("removing file with CID %q failed: %w", id, err)
	}

	return nil
}

func (p *node) AddFile(r io.Reader) (_cid string, err error) {
	if p.closed.Load() {
		return "", errorClosed
	}

	var n ipld.Node
	n, err = p.ipfs.AddFile(p.ctx, r, nil)
	if err != nil {
		return "", fmt.Errorf("adding file to IPFS failed: %w", err)
	}
	_cid = n.Cid().String()
	return
}

// Note: context should have a timeout and depend on the peer context as parent
func (p *node) GetFile(ctx context.Context, id string) (ReadSeekCloser, error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	_cid, err := cid.Decode(id)
	if err != nil {
		return nil, fmt.Errorf("decoding CID %q failed: %w", id, err)
	}

	file, err := p.ipfs.GetFile(ctx, _cid)
	if err != nil {
		return nil, fmt.Errorf("getting file with CID %q failed: %w", id, err)
	}

	return file, nil
}

func (p *node) GetFileFromCid(ctx context.Context, cid cid.Cid) (ReadSeekCloser, error) {
	if p.closed.Load() {
		return nil, errorClosed
	}

	file, err := p.ipfs.GetFile(ctx, cid)
	if err != nil {
		return nil, fmt.Errorf("getting file with CID %q failed: %w", cid.String(), err)
	}

	return file, nil
}

func (p *node) AddFileForCid(r io.Reader) (cid.Cid, error) {
	if p.closed.Load() {
		return cid.Cid{}, errorClosed
	}

	n, err := p.ipfs.AddFile(p.ctx, r, nil)
	if err != nil {
		return cid.Cid{}, fmt.Errorf("adding file to IPFS failed: %w", err)
	}

	return n.Cid(), nil
}
