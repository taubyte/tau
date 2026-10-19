// Package linkcreds names the credentials linking a project stores in the
// secrets service, so the CLI that writes them and the services that read them
// spell them the same way.
//
// THE CREDENTIALS ARE THE DEVELOPER'S. `tau link` generates a webhook secret
// and a deploy key on the developer's machine, installs their public halves on
// the forge with the developer's own token, and stores the private halves here
// through the build frontend's secret routes — authorized, like every other
// secret, against the project's linked repositories. The cloud generates
// neither and holds no forge token.
//
// # The names
//
// Every one lives in Environment, a scope no deployment can be named (an
// environment is a DSL identifier, and "@" is not in one), so a function
// reading its own environment's secrets can never reach these.
//
//	(<project>, "@link", "hook-secret")              the project's webhook secret
//	(<project>, "@link", "deploy-key-<sha256 hex>")  one repository's deploy key,
//	                                                 private half, OpenSSH PEM
//
// The deploy key's name carries a digest of the normalised repository key
// rather than the key itself, because a repository key is `host/owner/name` and
// a secret name is one path segment.
//
// The build frontend reads the hook secret to verify a push to
// POST /hook/<project>; the build backend reads a repository's deploy key to
// clone it. A repository with no deploy key set is cloned with none, which is
// what a public repository needs.
package linkcreds

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/taubyte/tau/pkg/register"
)

const (
	// Environment is the secrets scope link credentials live in.
	Environment = "@link"

	// HookSecret is the project's webhook secret.
	HookSecret = "hook-secret"
)

// DeployKey is the secret name of repository's deploy key.
func DeployKey(repository string) string {
	sum := sha256.Sum256([]byte(register.NormaliseRepository(repository)))
	return "deploy-key-" + hex.EncodeToString(sum[:])
}
