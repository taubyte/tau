// Package dokploy imports a Dokploy application. Its app definition is a compose file,
// so this delegates to pkg/importer/compose and reports what Dokploy keeps elsewhere:
// variables inherited from project and environment (`${{project.KEY}}`), and a
// `patch` that writes files into the clone before the build, which cannot be seen in
// the file and is reported unconditionally.
package dokploy

import (
	"regexp"
	"sort"
	"strings"

	"github.com/taubyte/tau/pkg/importer"
	"github.com/taubyte/tau/pkg/importer/compose"
)

// Source is what this importer is called in a finding.
const Source = "dokploy"

// inherited matches Dokploy's own interpolation: ${{project.KEY}} and
// ${{environment.KEY}}. It is deliberately not compose's ${VAR}, which compose
// itself resolves and which this importer already reports through that path.
var inherited = regexp.MustCompile(`\$\{\{\s*(project|environment)\.([A-Za-z0-9_]+)\s*\}\}`)

// Import reads a Dokploy application's compose file and reports what Dokploy
// holds elsewhere.
func Import(composeFile []byte) (importer.Result, error) {
	r, err := compose.Import(composeFile)
	if err != nil {
		return importer.Result{}, err
	}
	r.Source = Source

	addInheritance(&r, composeFile)
	addPlatformFindings(&r)

	r.Sort()
	return r, nil
}

// addInheritance names the references whose values are a level up.
//
// IT READS THE RAW FILE rather than the parsed result, because a reference can
// appear anywhere — an image tag, a command argument, a volume path — and the
// question is not what it means but that it is unresolved.
func addInheritance(r *importer.Result, raw []byte) {
	found := map[string]bool{}
	for _, m := range inherited.FindAllStringSubmatch(string(raw), -1) {
		found[m[1]+"."+m[2]] = true
	}
	if len(found) == 0 {
		return
	}

	names := make([]string, 0, len(found))
	for k := range found {
		names = append(names, k)
	}
	sort.Strings(names)

	r.Add(importer.Finding{Key: "inherited_variables", Level: importer.Elsewhere,
		Code: "SOURCE_IN_API:inherited-variables",
		Detail: "this file references values Dokploy resolves from the project and environment above it (" +
			strings.Join(names, ", ") + "); the file alone carries the references and none of the values"})
}

func addPlatformFindings(r *importer.Result) {
	r.Add(importer.Finding{Key: "env", Level: importer.Elsewhere,
		Code:   "SOURCE_IN_API:env",
		Detail: "environment variables live in Dokploy's database, reachable with an x-api-key against its API"})

	r.Add(importer.Finding{Key: "domains", Level: importer.Elsewhere,
		Code:   "SOURCE_IN_API:domains",
		Detail: "domains and certificates are Dokploy state and are not in the compose file"})

	r.Add(importer.Finding{Key: "registry", Level: importer.Elsewhere,
		Code:   "SOURCE_WRITE_ONLY:registry-password",
		Detail: "a private registry's password is stored encrypted and is not readable from the file"})

	// THE ONE THAT CANNOT BE SEEN. A patch writes files into the cloned repo
	// before the build, so the compose file and the repository can both be
	// complete and the built image still differ from either.
	r.Add(importer.Finding{Key: "patch", Level: importer.Elsewhere,
		Code: "SOURCE_OUT_OF_BAND:file-overlay",
		Detail: "Dokploy can overlay files onto the cloned repository before building; nothing in the compose file " +
			"or the repository reveals one, so an app with a patch builds differently here and the only way to " +
			"know is to ask"})

	r.Add(importer.Finding{Key: "environments", Level: importer.Approximated,
		Code: "STRUCTURE_MAPS_ONE_TO_ONE",
		Detail: "Dokploy's project -> environment -> resource maps 1:1 onto tau's project and environments; " +
			"this file is one environment's worth"})
}
