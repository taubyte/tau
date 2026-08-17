package schema

import (
	"strings"

	"github.com/taubyte/tau/pkg/tcc/engine"
)

// The label a project occupies in a cloud's generated zone.
//
//	<name>.<ProjectLabel(id)>.<cloud fqdn>
//	 db     tdw4ygm23gwgn      tau.somecloud.io
//
// A pure function of the project id, which is a CID and therefore already unique — so
// the middle label is DERIVED, not assigned, and the same project has the same label
// on every cloud that hosts it. Only the cloud's own fqdn is resolved at deploy.
//
// The derivation itself lives in the engine's registry (engine.DeriveDNSLabel), not
// here, so tau-cli, the web console and seer compute this name the same way instead of
// each carrying their own. Why base32 and why 13 characters are documented there.

// ProjectLabelLength is the label's width; see engine.DeriveDNSLabel.
const ProjectLabelLength = 13

// ProjectLabel derives a project's label in a cloud's generated zone from its id.
// Panics only if the engine registry loses the derivation this package declares,
// which is a build-time contradiction rather than a runtime condition.
func ProjectLabel(projectID string) string {
	label, err := engine.Derive(engine.DeriveDNSLabel, projectID)
	if err != nil {
		panic("v2 schema declares " + engine.DeriveDNSLabel + " but the engine does not provide it: " + err.Error())
	}
	return label
}

// GeneratedHost spells out what a resource is reachable at, as far as the config can
// know it. The cloud's own fqdn is not in the config and is not guessed here — the
// caller that knows it appends the last segment.
//
//	GeneratedHost("db", "Qm…") == "db.tdw4ygm23gwgn"
//	GeneratedHost("",   "Qm…") == "tdw4ygm23gwgn"
func GeneratedHost(name, projectID string) string {
	label := ProjectLabel(projectID)
	if name == "" {
		return label
	}
	return strings.Join([]string{name, label}, ".")
}
