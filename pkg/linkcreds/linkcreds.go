// Package linkcreds names the credentials linking a project stores in the secrets
// service, so the CLI that writes them and the services that read them agree.
//
// The credentials are the developer's: `tau link` generates a webhook secret and a
// deploy key locally, installs the public halves on the forge with the developer's
// token, and stores the private halves here through the build frontend's secret
// routes. The cloud generates neither and holds no forge token.
//
// Every name lives in Environment, a scope no deployment can be named ("@" is not in
// a DSL identifier), so a function's own secrets can never reach these.
//
//	(<project>, "@link", "hook-secret-<sha256 hex>")  one repository's webhook
//	                                                  secret
//	(<project>, "@link", "deploy-key-<sha256 hex>")   one repository's deploy key,
//	                                                  private half, OpenSSH PEM
//
// A name carries a digest of the normalised repository key, which may hold several
// segments. The build frontend verifies pushes with the hook secret; the backend
// clones with the deploy key. A Gerrit project's deploy key is the service account's
// ssh key. A repository with no deploy key is cloned with none.
package linkcreds

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/taubyte/tau/pkg/register"
)

// Environment is the secrets scope link credentials live in.
const Environment = "@link"

// HookSecret is the secret name of repository's webhook secret.
func HookSecret(repository string) string { return named("hook-secret-", repository) }

// DeployKey is the secret name of repository's deploy key.
func DeployKey(repository string) string { return named("deploy-key-", repository) }

func named(prefix, repository string) string {
	sum := sha256.Sum256([]byte(register.NormaliseRepository(repository)))
	return prefix + hex.EncodeToString(sum[:])
}
