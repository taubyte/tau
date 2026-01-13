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

// stubSecrets is a stub implementation of iface.Secrets for non-EE builds
type stubSecrets struct{}

// Ensure stubSecrets implements iface.Secrets
var _ iface.Secrets = (*stubSecrets)(nil)

// Secrets returns a stub Secrets interface for secret management
func (c *Client) Secrets() iface.Secrets {
	return &stubSecrets{}
}

// Store returns an error indicating Enterprise Edition is required
func (s *stubSecrets) Store(ctx context.Context, secretID string, plaintext []byte) error {
	return errEnterpriseOnly
}

// Retrieve returns an error indicating Enterprise Edition is required
func (s *stubSecrets) Retrieve(ctx context.Context, secretID string) ([]byte, error) {
	return nil, errEnterpriseOnly
}

// Delete returns an error indicating Enterprise Edition is required
func (s *stubSecrets) Delete(ctx context.Context, secretID string) error {
	return errEnterpriseOnly
}

// Exists returns an error indicating Enterprise Edition is required
func (s *stubSecrets) Exists(ctx context.Context, secretID string) (bool, error) {
	return false, errEnterpriseOnly
}

// List returns an error indicating Enterprise Edition is required
func (s *stubSecrets) List(ctx context.Context) ([]string, error) {
	return nil, errEnterpriseOnly
}

// PublicKeys returns an error indicating Enterprise Edition is required
func (s *stubSecrets) PublicKeys(ctx context.Context, opts ...iface.PublicKeyOption) ([]iface.DistributedKey, error) {
	return nil, errEnterpriseOnly
}
