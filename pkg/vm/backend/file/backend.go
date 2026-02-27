//TODO: Build Tag only for development

package file

import (
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"

	"github.com/h2non/filetype"
	"github.com/h2non/filetype/matchers"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/specs/builders/wasm"
	"github.com/taubyte/tau/pkg/vm/backend/errors"

	ma "github.com/multiformats/go-multiaddr"
	resolv "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
)

const headerSize = 512

type backend struct{}

func New() vm.Backend {
	return &backend{}
}

func (b *backend) Close() error {
	return nil
}

func (b *backend) Scheme() string {
	return resolv.FILE_PROTOCOL_NAME
}

// zipEntryReadCloser closes both the zip entry reader and the underlying file.
type zipEntryReadCloser struct {
	io.ReadCloser
	file *os.File
}

func (z *zipEntryReadCloser) Close() error {
	z.ReadCloser.Close()
	return z.file.Close()
}

// gzipFileReadCloser closes both the gzip reader and the underlying file.
type gzipFileReadCloser struct {
	*gzip.Reader
	file *os.File
}

func (g *gzipFileReadCloser) Close() error {
	g.Reader.Close()
	return g.file.Close()
}

func (b *backend) Get(multiAddr ma.Multiaddr) (io.ReadCloser, error) {
	protocols := multiAddr.Protocols()
	if protocols[0].Code != resolv.P_FILE {
		return nil, errors.MultiAddrCompliant(multiAddr, resolv.FILE_PROTOCOL_NAME)
	}

	path, err := multiAddr.ValueForProtocol(resolv.P_FILE)
	if err != nil {
		return nil, errors.ParseProtocol(resolv.FILE_PROTOCOL_NAME, err)
	}

	path = path[1:]

	file, err := os.Open(path)
	if err != nil {
		return nil, errors.RetrieveError(path, err, b)
	}

	header := make([]byte, headerSize)
	n, err := file.Read(header)
	if err != nil && err != io.EOF {
		file.Close()
		return nil, fmt.Errorf("reading file header: %w", err)
	}
	header = header[:n]

	if _, err := file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, fmt.Errorf("seeking to start: %w", err)
	}

	kind, err := filetype.Match(header)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("matching file type: %w", err)
	}

	switch kind {
	case matchers.TypeZip:
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("stating file: %w", err)
		}
		zr, err := zip.NewReader(file, info.Size())
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("opening zip: %w", err)
		}
		entryReader, err := zr.Open(wasm.WasmFile)
		if err != nil {
			entryReader, err = zr.Open(wasm.DeprecatedWasmFile)
			if err != nil {
				file.Close()
				return nil, fmt.Errorf("zip has no %q or %q: %w", wasm.WasmFile, wasm.DeprecatedWasmFile, err)
			}
		}
		return &zipEntryReadCloser{ReadCloser: entryReader, file: file}, nil
	case matchers.TypeGz:
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			file.Close()
			return nil, fmt.Errorf("opening gzip: %w", err)
		}
		return &gzipFileReadCloser{Reader: gzReader, file: file}, nil
	default:
		return file, nil
	}
}
