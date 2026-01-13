//go:build !ee

package auth

import (
	"context"
	"errors"

	iface "github.com/taubyte/tau/core/services/auth"
)

var (
	errEnterpriseOnly = errors.New("secrets management requires Enterprise Edition")
)

type stubSecrets struct{}

var _ iface.Secrets = (*stubSecrets)(nil)

func (c *Client) Secrets() iface.Secrets {
	return &stubSecrets{}
}

func (s *stubSecrets) Store(ctx context.Context, secretID string, plaintext []byte) error {
	return errEnterpriseOnly
}

func (s *stubSecrets) Retrieve(ctx context.Context, secretID string) ([]byte, error) {
	return nil, errEnterpriseOnly
}

func (s *stubSecrets) Delete(ctx context.Context, secretID string) error {
	return errEnterpriseOnly
}

func (s *stubSecrets) Exists(ctx context.Context, secretID string) (bool, error) {
	return false, errEnterpriseOnly
}

func (s *stubSecrets) List(ctx context.Context) ([]string, error) {
	return nil, errEnterpriseOnly
}

func (s *stubSecrets) PublicKeys(ctx context.Context, opts ...iface.PublicKeyOption) ([]iface.DistributedKey, error) {
	return nil, errEnterpriseOnly
}
