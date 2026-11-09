package primitives

import (
	"fmt"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	primitivesv2 "github.com/taubyte/tau/pkg/proto/gen/tau/primitives/v2"
)

// An address is parsed once, at the door, so every RPC applies the same check.

// address is one sync resource, in one environment of one project.
type address struct {
	project     string
	environment string
	id          string
	instance    string
}

func addressOf(ref *primitivesv2.Ref) address {
	return address{
		project:     ref.GetProject(),
		environment: ref.GetEnvironment(),
		id:          ref.GetId(),
		instance:    ref.GetInstance(),
	}
}

// addressFrom takes a grant's handle (project, environment, resource id) apart.
func addressFrom(handle string) (address, error) {
	parts := strings.Split(handle, "/")
	if len(parts) != 3 {
		return address{}, fmt.Errorf("%w: %q is not project/environment/id", ErrNoID, handle)
	}
	at := address{project: parts[0], environment: parts[1], id: parts[2]}
	return at, at.valid()
}

// valid refuses an address with a hole in it, naming the missing part. A separator in
// a segment is refused: Spec.Prefix builds raft keys and queue names from these. An
// instance is refused outright: the `sync` kind carries no `instances:` block, so an
// instance would mint undeclared, unbounded keyspaces.
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

// grant is the address as a grant names it, for a write sent on to another node.
func (a address) grant() invoke.Grant {
	return invoke.Grant{Handle: a.project + "/" + a.environment + "/" + a.id}
}

func (a address) ref() *primitivesv2.Ref {
	return &primitivesv2.Ref{
		Project: a.project, Environment: a.environment, Id: a.id, Instance: a.instance,
	}
}
