package project

import (
	"fmt"
	"sort"
	"strings"
	"time"

	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// access.go reads a resource's access block: what it declared it may reach, and
// what it asked to do to it.
//
// # It is the grant, and there is no second one
//
// A workload reaches a cache, a database or a storage because its own document
// says so. Nothing at run time widens that — no node setting, no policy file —
// so "what can this function touch" is answerable by reading the repository,
// which is the property the whole access block exists to have.
//
// # Telling a grant from a lowered child
//
// The compiled resource keys these blocks by KIND, and the same map also holds
// resources LOWERED into this one — an agent's own memory database, a worker
// pool's function — keyed by their id rather than by a label. The two are told
// apart by `ops`, which the vocabulary makes Required on an access entry and
// which no resource kind declares. See the schema's accessGroups.

// Reach is one entry: a target this resource may touch, and the verbs it asked
// for.
type Reach struct {
	// Kind is the vocabulary's kind name — "cache", "database", "storage".
	Kind string

	// Label is the key the author wrote. It is the target's name unless Name
	// says otherwise, and it is what a guest passes at run time.
	Label string

	// Name is the target's real name when it differs from the label — needed
	// only to reach two resources that share a name in different namespaces.
	Name string

	// Namespace is where it lives: "/billing" from the project root, "eu"
	// relative to the asker, or empty for the asker's own scope and then its
	// parents.
	Namespace string

	// Ops are the verbs asked for, sorted.
	Ops []string

	// Timeout bounds one operation on it. Zero means the caller's own.
	Timeout time.Duration

	// Path is where a mounted filesystem appears inside the workload. It is on
	// the ENTRY and not on the target because it is per use: two workloads
	// mounting one volume can put it in different places, which is what
	// mountAttributes exists for.
	Path string

	// DeliverPort and DeliverPath are where the platform POSTS an item for a
	// verb the platform delivers — a queue's `receive`, a stream's `subscribe`.
	//
	// ON THE ENTRY FOR THE SAME REASON Path IS: one container can consume two
	// queues on two paths, and the entry is where that is already written. A
	// WASM consumer needs neither — it is dispatched and has no port — so both
	// are ordinarily empty and the platform calls the workload's entrypoint.
	DeliverPort string
	DeliverPath string
}

// Target is the target's own name, which is the label unless the entry said
// otherwise — the only way to reach two resources that share a name in
// different namespaces.
func (r Reach) Target() string { return r.target() }

// target is the name to look the reach up under.
func (r Reach) target() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Label
}

// Reaches are the access entries of one kind, sorted by label.
//
// A kind with no block is an empty slice and not an error: most resources
// reach nothing.
func (r Resource) Reaches(kind string) []Reach {
	entries := r.Sub(kind)
	if len(entries) == 0 {
		return nil
	}

	// THE THREE FIELDS COME OFF THE VOCABULARY, not off this file's memory of
	// them. schema.AccessEntryShape exists for this consumer and says so — "so a
	// consumer recognises an access block without knowing that the word is
	// ops" — and until now the only callers were inside the schema package.
	//
	// IT IS THE HIGHEST-CONSEQUENCE STRING IN THIS PACKAGE. Rename the ops field
	// in the vocabulary and every entry below reads as empty, Reaches answers nil
	// for every kind, and every workload in the cloud resolves with no grants at
	// all — and starts, because a resource that reaches nothing is a legal
	// resource. There is no error anywhere in that.
	shape := schema.AccessEntryShape()

	out := make([]Reach, 0, len(entries))
	for _, label := range r.Keys(kind) {
		entry := entries.Sub(label)

		// AN ENTRY WITHOUT `ops` IS NOT A GRANT. It is a resource lowered into
		// this one, which the compiler files in the same map under its id.
		ops := entry.Strings(shape.Ops)
		if len(ops) == 0 {
			continue
		}
		sort.Strings(ops)

		reach := Reach{
			Kind:      kind,
			Label:     label,
			Name:      entry.String(shape.Name),
			Namespace: entry.String(shape.Namespace),
			Ops:       ops,
		}
		// THE PER-USE FIELDS COME OFF THE VOCABULARY TOO. They were the three
		// this file still spelled after the entry's own three stopped being
		// spelled, which is the same rot one layer in: a renamed `deliver/path`
		// reads as empty, a container consumer is posted at "/" instead of where
		// its author said, and nothing anywhere reports it.
		reach.Timeout, _ = entry.Duration(shape.Timeout)
		reach.Path = entry.String(shape.MountPath)
		reach.DeliverPort = entry.String(strings.Split(shape.DeliverPort, "/")...)
		reach.DeliverPath = entry.String(strings.Split(shape.DeliverPath, "/")...)
		out = append(out, reach)
	}

	if len(out) == 0 {
		return nil
	}

	// SORTED, because Resource.Keys walks a map and does not sort — so two
	// reads of one resource returned its grants in different orders, which is
	// a spec that differs between two resolves of the same revision.
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

// Reached resolves one entry to the resource it names.
//
// asker is the scope of the resource that declared it, which is what makes an
// omitted namespace mean "mine, then my parents" — Resource already walks
// outward, so an empty namespace is simply the asker's own scope handed
// through.
func (p *Project) Reached(asker []string, reach Reach) (Resource, string, error) {
	resource, id, err := p.Resource(scopeOf(asker, reach.Namespace), reach.Kind, reach.target())
	if err != nil {
		return nil, "", fmt.Errorf("%s %q: %w", reach.Kind, reach.Label, err)
	}
	return resource, id, nil
}

// ScopeOf turns a declared namespace into the chain Resource wants, from the
// scope of whoever declared it.
//
// EXPORTED BECAUSE THE ACCESS BLOCK IS NOT THE ONLY PLACE A NAMESPACE IS
// WRITTEN. A gateway route's handler names one, and a cron's target names one,
// and each of them was resolving it with a copy of half this rule — an absolute
// path handled and a relative one silently read as absolute. Direction is the
// whole claim, so it is stated once.
func ScopeOf(asker []string, namespace string) []string { return scopeOf(asker, namespace) }

// scopeOf turns a declared namespace into the chain Resource wants.
//
// AN ABSOLUTE NAMESPACE IGNORES THE ASKER ENTIRELY, which is the whole reason
// the leading "/" is in the grammar: a resource in /shop naming
// "/billing/ledger" means that one and not "/shop/billing/ledger".
func scopeOf(asker []string, namespace string) []string {
	if namespace == "" {
		return asker
	}

	if strings.HasPrefix(namespace, "/") {
		return segments(strings.TrimPrefix(namespace, "/"))
	}
	return append(append([]string{}, asker...), segments(namespace)...)
}

func segments(path string) []string {
	var out []string
	for _, segment := range strings.Split(path, "/") {
		if segment != "" {
			out = append(out, segment)
		}
	}
	return out
}
