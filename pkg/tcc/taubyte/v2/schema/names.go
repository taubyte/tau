package schema

import (
	"github.com/taubyte/tau/pkg/tcc/engine"
)

// WHAT EACH RESOURCE IS REACHED AT, computed here.
//
// A name is not one resource's business: it is the label the resource chose, the zone
// that answers for it, and — for a generated one — the project's own label, which is
// derived from its id. Three places, so whoever assembles them decides what the name is.
//
// A drawing was assembling them. It read the naming block, looked the zone up, glued the
// label to the zone's apex, and printed the result: a name the config never states,
// invented by a view, agreeing with anything else that needed it only by coincidence.
// The vocabulary already knew how (GeneratedHost); the view had its own copy of half of
// it.
//
// The cloud's own fqdn is still not here, and never will be: it is not in the config and
// is not the project's to assert. What is returned is the part that IS knowable, with
// the zone that supplies the rest named separately, so a consumer renders the gap rather
// than guessing at it.

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
		if res.str(at+"-kind") == "" {
			continue // it answers on no name, which is allowed
		}

		name := res.str(at + "-name")
		zoneRef := refID(res.fields[at+"-zone"])
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
