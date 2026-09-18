package project

import (
	"sort"
	"strings"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// hosts.go answers "what names does this project answer on", which is the
// question a gateway asks before it can route anything.
//
// THE NAME ITSELF IS COMPUTED BY THE VOCABULARY, not here. A name is the label
// the resource chose, the zone that answers for it, and — for a generated one —
// the project's own label derived from its id. Three places, and
// schema.ReachableNames is where they are put together. A second assembly here
// would agree with that one by coincidence, which is precisely the bug its own
// doc records: a drawing that read the naming block, glued the label to the
// zone's apex and printed a name the config never states.
//
// THE CLOUD'S OWN FQDN IS STILL NOT HERE. It is not in the config and is not
// the project's to assert, so a generated host carries the part that IS
// knowable and says the rest is missing. Whoever knows the cloud's name appends
// it — see Host.Generated.

// Host is one name a project answers on, and what answers it.
type Host struct {
	// Fqdn is the complete name when the zone is one the project declares, and
	// the project's own portion — "<label>.<project label>" — when the cloud's
	// zone supplies the rest.
	Fqdn string

	// Generated says the cloud's fqdn still has to be appended.
	Generated bool

	// Kind, Scope and Name address the resource that answers, in the form
	// Resource takes — so a gateway that matched a name can look up what it
	// matched without holding an id.
	Kind  string
	Scope []string
	Name  string
}

// Address is where a resource is, in the terms Resource takes. It is here
// rather than in a consumer because a worker and a gateway both name one, and
// two spellings of the same five fields is where a caller starts converting
// between them.
type Address struct {
	Project     string
	Environment string

	// Scope is the chain of namespace names from the project root. It is the
	// ASKER's scope: a name resolves there first and then in each parent.
	Scope []string

	Kind string
	Name string
}

// Handle is how a resolved resource is named to whatever backs its kind: the
// deployment it belongs to, then the resource's own id.
//
// IT IS HERE FOR THE REASON Address IS, and the case that forced it is a bus. A
// worker builds one when it composes a workload's grants, and a gateway builds
// one when it attaches a browser — and the two have to be the SAME STRING or a
// browser and a workload publishing to what the author wrote as one bus never
// meet. Two spellings agree by coincidence and diverge on the first edit to
// either.
//
// The id and not the label, because a label is scoped: three namespaces can
// each hold a `chat`.
func Handle(project, environment, id string) string {
	return strings.Join([]string{project, environment, id}, "/")
}

// Locate is where a resource id lives: its kind, the scope chain it is in, and
// its name.
//
// IT IS THE INDEX READ BACKWARDS, which the package already builds for its own
// hosts walk. A consumer holding an ID — a resolved reference, a lowering's
// produced scope — needs the other direction, and working it out by walking
// every scope is the search the index exists to avoid.
func (p *Project) Locate(id string) (kind string, scope []string, name string, ok bool) {
	where, held := p.located[id]
	if !held {
		return "", nil, "", false
	}
	return where.kind, where.scope, where.name, true
}

// Hosts is every name this project answers on, sorted.
//
// It is a WALK and not a lookup, and it runs once: the result is memoized
// because a compiled project is immutable, so a consumer may call it per
// request without paying for the walk per request.
func (p *Project) Hosts() []Host {
	p.hostsOnce.Do(func() { p.hosts, p.byHost = p.walkHosts() })
	return p.hosts
}

// At is every host entry answering on one name.
//
// IT RETURNS A LIST because a name can serve several paths: an author writes
// `/checkout` and `/api` as two gateways on one host, which is the only way
// this vocabulary spells more than one route at a name. Which of them serves a
// given request is the caller's match, not this lookup's.
func (p *Project) At(name string) []Host {
	p.Hosts()
	return p.byHost[strings.ToLower(name)]
}

func (p *Project) walkHosts() ([]Host, map[string][]Host) {
	reachable := schema.ReachableNames(p.root)

	out := make([]Host, 0, len(reachable))
	for id, r := range reachable {
		if r.Fqdn == "" {
			// A name whose zone the project does not declare. Nothing here can
			// say what it resolves to, and emitting a partial one would put a
			// name in a routing table that no request will ever carry.
			continue
		}
		where, ok := p.located[id]
		if !ok {
			continue
		}
		out = append(out, Host{
			Fqdn:      r.Fqdn,
			Generated: r.Generated,
			Kind:      where.kind,
			Scope:     where.scope,
			Name:      where.name,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Fqdn != out[j].Fqdn {
			return out[i].Fqdn < out[j].Fqdn
		}
		return out[i].Name < out[j].Name
	})

	by := map[string][]Host{}
	for _, h := range out {
		key := strings.ToLower(h.Fqdn)
		by[key] = append(by[key], h)
	}
	return out, by
}

// where is a resource's address in the terms Resource takes.
type where struct {
	kind  string
	scope []string
	name  string
}

// locate builds the id -> address map from the index.
//
// IT IS THE INDEX READ BACKWARDS. The index answers "what id is this name", and
// a consumer holding an id — a resolved reference, a reachable name — needs the
// other direction. Both come out of the same table, so they cannot disagree.
func locate(index map[string][]string, projectID string) map[string]where {
	scopes := map[string]bool{}
	for _, kind := range scopeKinds() {
		scopes[kind] = true
	}

	// scope ids to their names first, so a scope can be reported the way an
	// author writes it rather than as a chain of ids.
	//
	// EVERY KIND THAT IS A SCOPE, not only `namespace`: a workflow is a
	// container of the queues and drivers it lowered to, and reading only
	// namespaces here reported every one of them as sitting at the project
	// root — under the keys the root's own resources use.
	names := map[string]string{}
	for key, links := range index {
		seg := strings.Split(key, "/")
		if len(seg) < 2 || !scopes[seg[len(seg)-2]] {
			continue
		}
		names[lastSegment(links[len(links)-1])] = seg[len(seg)-1]
	}

	prefix := projectsSeg + "/" + projectID + "/"

	out := map[string]where{}
	for key, links := range index {
		rest, ok := strings.CutPrefix(key, prefix)
		if !ok {
			continue
		}
		seg := strings.Split(rest, "/")
		if len(seg) < 2 || len(seg)%2 != 0 {
			continue
		}

		w := where{kind: seg[len(seg)-2], name: seg[len(seg)-1]}
		for i := 0; i+1 < len(seg)-2; i += 2 {
			if !scopes[seg[i]] {
				break
			}
			w.scope = append(w.scope, names[seg[i+1]])
		}
		out[lastSegment(links[len(links)-1])] = w
	}
	return out
}

// Zone is one DNS zone a project claims authority over.
//
// IT IS NOT A Host, and the difference is which way the name points. A Host is
// a name a RESOURCE answers at — a gateway's, a proxy's — assembled from the
// label it chose and the zone that answers for it. A zone's own fqdn is the
// other end of that: the apex this project is authoritative for, under which
// those labels live and under which an author may write records of their own.
//
// So it is a separate walk rather than another arm of the hosts one. Hosts is
// driven by `AnswersAt`, which names the BLOCK a kind declares its name in, and
// a zone has no such block: its name is the resource.
type Zone struct {
	// Fqdn is the apex, e.g. "example.com".
	Fqdn string

	// Records are the zone-file lines the author wrote, relative to Fqdn.
	Records []string

	// Scope and Name address the resource, in the form Resource takes.
	Scope []string
	Name  string
}

// Zones is every zone this project is authoritative for, sorted by apex.
//
// It is memoized for the reason Hosts is: a compiled project is immutable, so a
// consumer answering a query per request should not pay for the walk per
// request.
func (p *Project) Zones() []Zone {
	p.zonesOnce.Do(func() { p.zones = p.walkZones() })
	return p.zones
}

func (p *Project) walkZones() []Zone {
	var out []Zone

	for id, where := range p.located {
		if where.kind != zoneKind {
			continue
		}
		r, _, err := p.Resource(where.scope, zoneKind, where.name)
		if err != nil {
			continue
		}

		fqdn := strings.ToLower(strings.TrimSuffix(r.String("fqdn"), "."))
		if fqdn == "" {
			// A ZONE WITH NO APEX IS AUTHORITATIVE FOR NOTHING. The vocabulary
			// requires the field; a compiled object that lost it would
			// otherwise claim every name at once.
			continue
		}
		_ = id

		out = append(out, Zone{
			Fqdn:    fqdn,
			Records: r.Strings("records"),
			Scope:   append([]string(nil), where.scope...),
			Name:    where.name,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Fqdn < out[j].Fqdn })
	return out
}

const zoneKind = "zone"
