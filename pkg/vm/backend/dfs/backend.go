package dfs

import (
	"archive/zip"
	"compress/lzw"
	"context"
	"fmt"
	"io"

	"github.com/ipfs/go-cid"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/vm"
	peer "github.com/taubyte/tau/p2p/peer"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/tau/pkg/vm/backend/errors"
	resolv "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
	"go4.org/readerutil"
)

func New(node peer.Node) vm.Backend {
	return &backend{
		node: node,
	}
}

func (b *backend) Get(multiAddr ma.Multiaddr) (io.ReadCloser, error) {
	protocols := multiAddr.Protocols()
	if protocols[0].Code != resolv.P_DFS {
		return nil, errors.MultiAddrCompliant(multiAddr, resolv.DFS_PROTOCOL_NAME)
	}

	_cid, err := multiAddr.ValueForProtocol(resolv.P_DFS)
	if err != nil {
		return nil, errors.ParseProtocol(resolv.DFS_PROTOCOL_NAME, err)
	}

	__cid, err := cid.Decode(_cid)
	if err != nil {
		return nil, err
	}

	ctx, ctxC := context.WithTimeout(b.node.Context(), vm.GetTimeout)
	handleErr := func(err error) (io.ReadCloser, error) {
		ctxC()
		return nil, err
	}

	ok, err := b.node.DAG().BlockStore().Has(ctx, __cid)
	if !ok || err != nil {
		dagReader, err := b.node.GetFile(ctx, _cid)
		if err != nil {
			return handleErr(fmt.Errorf("caching CID `%s` failed with:  %w", _cid, err))
		}

		dagReader.Close()
	}

	dagReader, err := b.node.GetFile(ctx, _cid)
	if err != nil {
		return handleErr(errors.RetrieveError(_cid, err, b))
	}

	// Backwards compatibility
	size, _ := readerutil.Size(dagReader)
	zipReader, err := zip.NewReader(
		readerutil.NewBufferingReaderAt(dagReader),
		size,
	)
	if err != nil {
		dagReader.Seek(0, io.SeekStart)
		return &zWasmReadCloser{
			dag:        dagReader,
			unCompress: lzw.NewReader(dagReader, lzw.LSB, 8),
		}, nil
	} else {
		// Trying for both main/artifact.wasm
		reader, err := zipReader.Open(wasm.WasmFile)
		if err != nil {
			reader, err = zipReader.Open(wasm.DeprecatedWasmFile)
			if err != nil {
				return handleErr(fmt.Errorf("reading wasm file as aritfact and main failed with: %s", err))
			}
		}

		return &zipReadCloser{
			parent:     dagReader,
			ReadCloser: reader,
		}, nil
	}
}

func (b *backend) Scheme() string {
	return Scheme
}

func (b *backend) Close() error {
	b.node = nil
	return nil
}
