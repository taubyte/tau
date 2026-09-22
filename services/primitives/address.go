package primitives

import (
	"fmt"
	"strings"

	primitivesv1 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v1"
)

// address.go is the one place a Ref becomes something this package uses.
//
// IT IS PARSED ONCE, AT THE DOOR. Every handler takes the same four fields off
// the wire, and a check repeated per call is a second opinion — the day the two
// disagreed, the answer would depend on which RPC the caller used.

// address is one sync resource, in one environment of one project.
type address struct {
	project     string
	environment string
	id          string
	instance    string
}

func addressOf(ref *primitivesv1.Ref) address {
	return address{
		project:     ref.GetProject(),
		environment: ref.GetEnvironment(),
		id:          ref.GetId(),
		instance:    ref.GetInstance(),
	}
}

// addressFrom takes a grant's handle apart. It is the same three segments
// node/queue.go and node/log.go read, spelled once here because this package is
// the other end of that move.
func addressFrom(handle string) (address, error) {
	parts := strings.Split(handle, "/")
	if len(parts) != 3 {
		return address{}, fmt.Errorf("%w: %q is not project/environment/id", ErrNoID, handle)
	}
	at := address{project: parts[0], environment: parts[1], id: parts[2]}
	return at, at.valid()
}

// valid refuses an address with a hole in it, naming which part is missing so a
// caller is told rather than left with a miss.
//
// A SEPARATOR IN A SEGMENT IS REFUSED for the reason the secrets service gives:
// the separator between the parts is this service's, and a segment carrying one
// is a readable path into somebody else's key space. Here it is load-bearing
// twice over, because [Spec.Prefix] is what a raft key and a queue name are
// built out of.
//
// AN INSTANCE IS REFUSED OUTRIGHT, AND THE VOCABULARY IS WHY. `instances:` is a
// real block — its presence is what says a resource holds many, and it carries
// both the validator that admits one and the limit on how many there may be —
// and the `sync` kind does not carry it. So no project can say that a queue or
// a log holds more than one, which leaves nothing to read: a service that took
// the field anyway would let any caller mint keyspaces nothing declared, nobody
// admitted and no limit bounds, one raft key apiece, none of which the
// lifecycle pass could resolve back to a declaration to retain. Refused by name
// is the honest answer until `sync` carries instanceAttributes(); [Spec.at] is
// the other half of what that day changes.
func (a address) valid() error {
	switch {
	case a.project == "":
		return ErrNoProject
	case a.environment == "":
		return ErrNoEnvironment
	case a.id == "":
		return ErrNoID
	case a.instance != "":
		return fmt.Errorf("%w: %s asks for instance %q", ErrInstanceUnserved, a.id, a.instance)
	}

	for what, segment := range map[string]string{
		"project": a.project, "environment": a.environment, "id": a.id,
	} {
		if strings.ContainsAny(segment, "/") {
			return fmt.Errorf("%w: %s %q carries a separator", ErrNoID, what, segment)
		}
	}
	return nil
}

func (a address) ref() *primitivesv1.Ref {
	return &primitivesv1.Ref{
		Project: a.project, Environment: a.environment, Id: a.id, Instance: a.instance,
	}
}
