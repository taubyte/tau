package database

import (
	"fmt"
	"strings"

	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
)

// address.go is the one place a Ref becomes something this package uses.
//
// IT IS PARSED ONCE, AT THE DOOR. Every handler takes the same four fields off
// the wire, and a check repeated per call is a second opinion — the day the two
// disagreed, the answer would depend on which RPC the caller used.

// address is one database, in one environment of one project.
type address struct {
	project     string
	environment string
	id          string
	instance    string
}

func addressOf(ref *databasev2.Ref) address {
	return address{
		project:     ref.GetProject(),
		environment: ref.GetEnvironment(),
		id:          ref.GetId(),
		instance:    ref.GetInstance(),
	}
}

// valid refuses an address with a hole in it, naming which part is missing so a
// caller is told rather than left with a miss.
//
// A SEPARATOR IN A SEGMENT IS REFUSED for the reason the secrets service gives:
// the separator between the parts is this service's, and a segment carrying one
// is a readable path into somebody else's key space.
func (a address) valid() error {
	switch {
	case a.project == "":
		return ErrNoProject
	case a.environment == "":
		return ErrNoEnvironment
	case a.id == "":
		return ErrNoID
	}

	for what, segment := range map[string]string{
		"project": a.project, "environment": a.environment,
		"id": a.id, "instance": a.instance,
	} {
		if strings.ContainsAny(segment, "/") {
			return fmt.Errorf("%w: %s %q carries a separator", ErrNoID, what, segment)
		}
	}
	return nil
}

// key is what the resident table holds this database under. The revision is NOT
// in it: a newer deployment replaces what it names rather than sitting beside
// it, which is what makes a rotated credential take effect.
func (a address) key() string {
	return a.project + "/" + a.environment + "/" + a.storeName()
}

// storeName is what ADDRESSES the store: the resource id, plus the instance for
// a store whose config says it holds many.
func (a address) storeName() string {
	if a.instance == "" {
		return a.id
	}
	return a.id + "-" + a.instance
}
