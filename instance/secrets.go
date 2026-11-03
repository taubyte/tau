package instance

import (
	secretsService "github.com/taubyte/tau/services/secrets"
)

// The vault is services/secrets'; the instance only keeps one for the services it
// still builds itself.

// newVault is a vault over this instance's services: its own secrets service when it
// runs one, otherwise whichever nodes discovery names.
func newVault(n *Instance) *secretsService.Vault {
	return secretsService.VaultOn(&added{node: n})
}

// sharedVault is the instance's one vault for its built-ins, made on first use. One,
// not one per consumer: it holds a stream to a remote secrets service, and a second
// would be a second stream to the same peer.
func (n *Instance) sharedVault() *secretsService.Vault {
	if n.vault == nil {
		n.vault = newVault(n)
		n.closers = append(n.closers, n.vault)
	}
	return n.vault
}
