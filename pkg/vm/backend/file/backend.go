//TODO: Build Tag only for development

package file

import (
	"io"
	"os"

	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm/backend/errors"

	ma "github.com/multiformats/go-multiaddr"
	resolv "github.com/taubyte/tau/pkg/vm/resolvers/taubyte"
)

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

func (b *backend) Get(multiAddr ma.Multiaddr) (io.ReadCloser, error) {
	protocols := multiAddr.Protocols()
	if protocols[0].Code != resolv.P_FILE {
		return nil, errors.MultiAddrCompliant(multiAddr, resolv.FILE_PROTOCOL_NAME)
	}

	path, err := multiAddr.ValueForProtocol(resolv.P_FILE)
	if err != nil {
		return nil, errors.ParseProtocol(resolv.FILE_PROTOCOL_NAME, err)
	}

	// remove extra slash
	path = path[1:]

	file, err := os.Open(path)
	if err != nil {
		return nil, errors.RetrieveError(path, err, b)
	}

	return file, nil
}
