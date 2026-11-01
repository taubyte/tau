// Package coolify imports a Coolify application. Coolify's app definition is a compose
// file, so this delegates to pkg/importer/compose and reports what Coolify keeps
// elsewhere: environment, domains, certificates (Elsewhere findings).
//
// A Coolify `read` token silently returns redacted environment values, so
// `read:sensitive` is required; importing blank secrets is worse than refusing.
package coolify

import (
	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/compose"
)

// Source is what this importer is called in a finding.
const Source = "coolify"

// RequiredScopes are the token scopes an API-backed import needs.
//
// `read` alone is not enough and is the trap this package exists to name.
var RequiredScopes = []string{"read", "read:sensitive"}

// Import reads a Coolify application's compose file and reports everything
// Coolify holds elsewhere.
func Import(composeFile []byte) (importer.Result, error) {
	r, err := compose.Import(composeFile)
	if err != nil {
		return importer.Result{}, err
	}
	r.Source = Source
	addPlatformFindings(&r)
	r.Sort()
	return r, nil
}

func addPlatformFindings(r *importer.Result) {
	r.Add(importer.Finding{Key: "env", Level: importer.Elsewhere,
		Code: "SOURCE_IN_API:env",
		Detail: "environment variables live in Coolify's database, not the compose file; reading them needs a " +
			"team-scoped token with read:sensitive, because a read-only token returns REDACTED values rather " +
			"than an error and would import blanks"})

	r.Add(importer.Finding{Key: "domains", Level: importer.Elsewhere,
		Code:   "SOURCE_IN_API:domains",
		Detail: "domains and their certificates are Coolify state; the compose file does not name them"})

	// SERVICE_PASSWORD_* MUST BE READ, NEVER REGENERATED. Coolify's service
	// templates generate these once and the running service is already using
	// them; minting new ones on import produces a service whose credentials no
	// longer match its own data.
	r.Add(importer.Finding{Key: "service_password", Level: importer.Elsewhere,
		Code: "SOURCE_GENERATED_ONCE:service-password",
		Detail: "Coolify's service templates generate SERVICE_PASSWORD_* once and the running service is already " +
			"using them; they must be read across, never regenerated, or the imported service will not match its own data"})

	// Coolify's Project -> Environment -> Resource is a 1:1 map onto tau's own
	// environments, which is worth saying because it is the rare structural
	// thing that carries perfectly.
	r.Add(importer.Finding{Key: "environments", Level: importer.Approximated,
		Code: "STRUCTURE_MAPS_ONE_TO_ONE",
		Detail: "Coolify's Project -> Environment -> Resource maps 1:1 onto tau's project and environments; " +
			"this file is one environment's worth, and the others import the same way"})
}
