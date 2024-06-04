package errors

import (
	"fmt"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/vm"
)

func RetrieveError(path string, err error, backend vm.Backend) error {
	return fmt.Errorf("retrieving ReadCloser through `%s` backend from `%s` failed with: %s", backend.Scheme(), path, err)
}

func MultiAddrCompliant(multiAddr ma.Multiaddr, protocol string) error {
	return fmt.Errorf("multi address `%s` is not %s compliant ", multiAddr.String(), protocol)
}

func ParseProtocol(protocol string, err error) error {
	return fmt.Errorf("parsing %s failed with: %s", protocol, err)
}
