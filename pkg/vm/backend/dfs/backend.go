package dfs

import (
	"archive/zip"
	"bytes"
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
	"github.com/taubyte/tau/utils/readerutil"
)

func New(node peer.Node) vm.Backend {
	return &backend{
		node:  node,
		cache: newModuleCache(CacheSize),
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

	// Content behind a CID is immutable, so decompressed bytes are safe to serve straight from cache.
	if data, ok := b.cache.get(_cid); ok {
		return io.NopCloser(bytes.NewReader(data)), nil
	}

	v, err, _ := b.group.Do(_cid, func() (any, error) {
		data, err := b.fetch(_cid, __cid)
		if err != nil {
			return nil, err
		}

		b.cache.put(_cid, data)
		return data, nil
	})
	if err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewReader(v.([]byte))), nil
}

// fetch performs the actual DAG/zip/LZW retrieval and returns the fully
// decompressed module bytes, closing all underlying readers along the way.
func (b *backend) fetch(_cid string, __cid cid.Cid) ([]byte, error) {
	ctx, ctxC := context.WithTimeout(b.node.Context(), vm.GetTimeout)
	handleErr := func(err error) ([]byte, error) {
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

	var rc io.ReadCloser
	if err != nil {
		dagReader.Seek(0, io.SeekStart)
		rc = &zWasmReadCloser{
			dag:        dagReader,
			unCompress: lzw.NewReader(dagReader, lzw.LSB, 8),
		}
	} else {
		// Trying for both main/artifact.wasm
		reader, err := zipReader.Open(wasm.WasmFile)
		if err != nil {
			reader, err = zipReader.Open(wasm.DeprecatedWasmFile)
			if err != nil {
				dagReader.Close()
				return handleErr(fmt.Errorf("reading wasm file as aritfact and main failed with: %s", err))
			}
		}

		rc = &zipReadCloser{
			parent:     dagReader,
			ReadCloser: reader,
		}
	}

	data, err := io.ReadAll(rc)
	if err != nil {
		rc.Close()
		return handleErr(err)
	}

	if err := rc.Close(); err != nil {
		return handleErr(err)
	}

	ctxC()
	return data, nil
}

func (b *backend) Scheme() string {
	return Scheme
}

func (b *backend) Close() error {
	b.node = nil
	b.cache = nil
	return nil
}
