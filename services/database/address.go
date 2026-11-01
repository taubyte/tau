package database

import (
	"fmt"
	"strings"

	databasev2 "github.com/taubyte/tau/pkg/proto/gen/tau/database/v2"
)

// An address is parsed once, at the door, so every RPC applies the same check.

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

// valid refuses an address with a hole in it, naming the missing part. A separator
// in a segment is refused: it would be a readable path into another key space.
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

// key is what the resident table holds this database under. The revision is not in
// it, so a newer deployment replaces what it names (a rotated credential takes
// effect).
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
