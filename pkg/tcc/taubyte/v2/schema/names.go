package schema

import (
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// What each resource is reached at, assembled once here from the label it chose,
// the zone answering for it, and (for a generated name) the project label. The
// cloud's fqdn is never known; the knowable part is returned and the gap named.

// Reachable is what one resource answers at.
type Reachable struct {
	Name string // the label it chose, "" for the apex of its zone
	// Zone is the id of the zone resource that answers for it, empty when the cloud's
	// own zone does — which no config contains.
	Zone string
	// Fqdn is the name as far as it is knowable: complete when the zone is one the
	// project declares, and the project's own portion when it is the cloud's.
	Fqdn string
	// Generated says the missing piece is the cloud's fqdn, so a consumer shows that
	// rather than presenting a partial name as a whole one.
	Generated bool
}

// ReachableNames is every resource that answers on a name, keyed by id.
func ReachableNames(obj map[string]any) map[string]Reachable {
	root := obj
	if inner, ok := obj["object"].(map[string]any); ok {
		root = inner
	}

	all := collect(root, "")

	// the apex of every zone the project declares, by id
	apex := map[string]string{}
	for _, res := range all {
		if fqdn := res.str("fqdn"); fqdn != "" {
			apex[res.id] = fqdn
		}
	}

	projectID, _ := root["id"].(string)
	out := map[string]Reachable{}
	for _, res := range all {
		at := answersAtOf(res.kind)
		if at == "" {
			continue
		}
		if res.at(at, "kind") == "" {
			continue // it answers on no name, which is allowed
		}

		name := res.at(at, "name")
		zoneRef := res.at(at, "zone", "in", "name")
		if zoneRef == "" {
			// the cloud's own zone: the project's label is a pure function of its id,
			// so the knowable part is computed rather than left to a caller
			r := Reachable{Name: name, Generated: true}
			if projectID != "" {
				r.Fqdn = GeneratedHost(name, projectID)
			}
			out[res.id] = r
			continue
		}
		zoneID := zoneRef
		r := Reachable{Name: name, Zone: zoneID}
		if a := apex[zoneID]; a != "" {
			r.Fqdn = a
			if name != "" {
				r.Fqdn = name + "." + a
			}
		}
		out[res.id] = r
	}
	return out
}

// answersAtOf is where a kind declares the name it answers on, read off the kind.
func answersAtOf(kind string) string {
	for _, g := range V2Resources {
		if name, _ := g.Match.(string); name == kind && len(g.Children) > 0 {
			return engine.AnswersAtOf(g.Children[0])
		}
	}
	return ""
}
