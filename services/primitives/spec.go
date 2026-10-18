package primitives

import (
	"strings"

	"github.com/taubyte/tau/pkg/specs"
)

// spec.go is what a sync resource IS, and the one name every layer below
// derives its keys from.

// The `sync` kind and the two types under it, spelled as the vocabulary spells
// them. Nothing is renamed on the way in and there is no second vocabulary to
// map between.
const (
	syncKind  = specs.KindSync
	queueType = specs.SyncTypeQueue
	logType   = specs.SyncTypeLog
)

// The two resolutions the `sync` types may declare, which are the same two
// words on a queue and a log, and the same axis `database.consensus` carries.
const (
	consensusRaft = specs.SyncQueueConsensusRaft
	consensusCRDT = specs.SyncQueueConsensusCrdt
)

// Spec is one sync resource as the store layer needs it.
//
// IT CARRIES NO SETTINGS. A visibility timeout, a retention and a dead letter
// change nothing about WHERE a resource's state lives, so a spec that carried
// them would be a second thing to keep in step with a redeploy — and the one
// place that mattered, node/queue.go's per-call open, says so already: caching a
// queue by its id pinned the config from before the redeploy.
type Spec struct {
	Project     string
	Environment string

	// Type is `queue` or `log`. IT IS IN THE PATH, which is what keeps a queue
	// called `jobs` and a log called `jobs` from sharing a keyspace, and what
	// makes a listing of one type a prefix scan rather than a filter.
	Type string

	// Name is the resource ID, and NEVER the author's label. Two workloads may
	// call one queue different things, and two projects may both call one
	// `jobs`. Nothing else is folded into it — see [Spec.at].
	Name string

	// Consensus is what the author asked for, already defaulted, so nothing
	// below has to decide what silence meant.
	Consensus string
}

// Prefix is where this resource's state lives: four segments, none of which may
// contain a separator — address.valid is what holds that, and it is why it
// holds it for the instance too.
//
// IT IS PARSEABLE BACK, which the retention loop depends on: the loop walks the
// keyspace rather than a list somebody maintains, so every key it finds has to
// say which resource it belongs to.
func (s Spec) Prefix() string {
	return s.Type + "/" + s.Project + "/" + s.Environment + "/" + s.Name
}

// specFrom reads a prefix back. It is [Spec.Prefix] inverted, and the consensus
// is deliberately absent: what a prefix names is a place, and what an author
// asked for is read from the declaration.
func specFrom(prefix string) (Spec, bool) {
	parts := strings.Split(prefix, "/")
	if len(parts) != 4 {
		return Spec{}, false
	}
	for _, part := range parts {
		if part == "" {
			return Spec{}, false
		}
	}
	return Spec{Type: parts[0], Project: parts[1], Environment: parts[2], Name: parts[3]}, true
}

// at is the address a spec was built from.
//
// IT ROUND-TRIPS BECAUSE THE NAME IS THE ID, and that is load-bearing rather
// than incidental: the lifecycle pass reads a PLACE out of the keyspace and
// then has to resolve it back to the declaration that says what to retain, so a
// name carrying anything the project does not index is a resource the pass
// looks up, misses, and silently walks past — retention, the claim sweep and
// the dead letter's bound all stopping for it with nothing said. An earlier
// shape folded an instance in behind a separator and did exactly that.
//
// So the day `sync` carries `instances:`, the instance takes a SEGMENT OF ITS
// OWN in [Spec.Prefix] — parsed back by [specFrom] like every other — and this
// stays the inverse of what built it. Folding it back into the id here is the
// one thing that cannot work. Until then address.valid refuses one.
func (s Spec) at() address {
	return address{project: s.Project, environment: s.Environment, id: s.Name}
}
