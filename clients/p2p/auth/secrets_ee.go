//go:build ee

package auth

import (
	iface "github.com/taubyte/tau/core/services/auth"
	secretsPkg "github.com/taubyte/tau/ee/clients/p2p/auth/secrets"
)

func (c *Client) Secrets() iface.Secrets {
	secrets, err := secretsPkg.New(c.node, c.client)
	if err != nil {
		return nil
	}
	return secrets
}
