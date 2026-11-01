package primitives

import (
	"strings"

	"github.com/taubyte/tau/pkg/specs"
)

// What a sync resource is, and the one name every layer derives its keys from.

// The `sync` kind and its two types, as the vocabulary spells them.
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

// Spec is one sync resource as the store layer needs it. It carries no settings:
// they do not change where state lives, and carrying them would pin a pre-redeploy
// config.
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

// at is the address a spec was built from. It round-trips because the name is the id:
// the lifecycle pass resolves a place back to its declaration, and anything unindexed
// in the name would make it silently skip the resource. When `sync` carries
// `instances:`, the instance needs a segment of its own in Spec.Prefix.
func (s Spec) at() address {
	return address{project: s.Project, environment: s.Environment, id: s.Name}
}
