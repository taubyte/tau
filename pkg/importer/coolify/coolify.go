// Package coolify imports a Coolify application.
//
// # Coolify's app definition IS a compose file
//
// Coolify hands the user's compose file to real `docker compose` with no
// filtering, so there is no Coolify-specific application format to parse: this
// importer delegates to pkg/importer/compose and then says what Coolify keeps
// that the file does not.
//
// That delegation is the point. A second parser for the same format would drift
// from the first, and every fidelity finding compose already makes — the
// healthcheck contract, depends_on conditions, published ports — is exactly as
// true here.
//
// # What Coolify keeps outside the file
//
// Environment variables, domains, certificates and per-resource settings live in
// Coolify's database, reachable with a team-scoped token. Those are Elsewhere
// findings: real values, not in this file, and not a gap in tau's vocabulary.
//
// # The one that is a trap
//
// A COOLIFY `read` TOKEN SILENTLY RETURNS REDACTED ENVIRONMENT VALUES. It does
// not error; it answers with blanks. An importer that accepted that would
// produce a project whose every secret is the empty string, and the failure
// would surface later as an application that cannot reach its database.
//
// So `read:sensitive` is required, and this package says so up front rather than
// leaving it to be discovered. Importing blanks is worse than refusing: a
// refusal is a five-minute fix, and a project full of empty secrets is a day of
// debugging something that was never broken.
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
