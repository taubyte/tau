package url

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	ma "github.com/multiformats/go-multiaddr"
	"github.com/taubyte/tau/core/vm"
	"github.com/taubyte/tau/pkg/vm/backend/errors"
)

type backend struct{}

func New() vm.Backend {
	return &backend{}
}

func (b *backend) Get(multiAddr ma.Multiaddr) (io.ReadCloser, error) {
	uri, err := buildUri(multiAddr)
	if err != nil {
		return nil, fmt.Errorf("building uri failed with: %s", err)
	}

	client := http.DefaultClient
	client.Timeout = vm.GetTimeout
	res, err := client.Get(uri)
	if err != nil {
		return nil, errors.RetrieveError(uri, err, b)
	}
	defer res.Body.Close()

	data, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	return io.NopCloser(bytes.NewBuffer(data)), nil
}

func (b *backend) Scheme() string {
	return "url"
}

func (b *backend) Close() error {
	return nil
}
