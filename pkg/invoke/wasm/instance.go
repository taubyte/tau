package wasm

import (
	"context"
	"errors"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
)

// instance.go is how a guest names ONE OF MANY.
//
// # The vocabulary's `instances:` block, finally meaning something
//
// Four kinds may declare that they hold many instances rather than one — cache,
// database, storage and bus — each admitted by a function the target names and
// bounded by a ceiling it declares. Until now nothing honoured it: a workload
// reaching such a store got the store, once, with no admission and no count, so
// the config's own security control silently did not exist. The composer
// refused the grant rather than serving that, which was right and made the
// block unusable.
//
// # The instance travels in the LABEL, and that is not a hack
//
// A label is `^[a-zA-Z_][a-zA-Z0-9_]*$` in the vocabulary — it CANNOT contain a
// slash — so `sessions/room-42` splits at the first one with no ambiguity ever.
// The alternative is an `instance: string` parameter on all twenty-three host
// functions, which every caller of a single-instance store passes empty: a
// required argument that is almost always nothing, on every call, forever.
//
// What the label parameter already means is "which one", and this is the whole
// of that question rather than half of it.
//
// # An instance is a handle, so no backing changes
//
// Every backing here already scopes its keys by `Grant.Handle`. An admitted
// instance is that handle with the instance appended, so a cache, a kv
// database, a document store and a bucket each isolate one instance from
// another by the mechanism they already use for two resources. Nothing below
// this file knows instances exist.
//
// # Admission is a seam because it invokes a function
//
// Deciding whether an instance may exist means running the workload the target
// named, which this tier cannot do: it runs one workload and has no way to
// reach another. So it asks whoever composed it — see Admitter, and
// node/admission.go for the implementation that invokes through the worker and
// counts against the ceiling.

// Admitter decides whether a caller may open one instance of a store.
//
// IT IS ASKED BEFORE ANYTHING IS TOUCHED, and it is asked per open rather than
// per call: an implementation that has already admitted an instance should say
// so cheaply, because this is on the request path.
type Admitter interface {
	Admit(ctx context.Context, grant invoke.Grant, instance string) error
}

// WithAdmission is what decides whether an instance may exist. A tier without
// one refuses every instance of every store that declares `instances:`, which
// is the same answer the composer used to give for the whole grant.
func WithAdmission(a Admitter) Option {
	return func(r *Runtime) { r.admission = a }
}

var (
	// ErrNotAdmitted is a validator that said no. It is the ordinary answer
	// rather than a failure: the target declared who decides, and this is that
	// decision.
	ErrNotAdmitted = errors.New("wasm: this instance was not admitted")

	// ErrNoAdmission is a tier with nothing to ask. It is a composition fault
	// and not the caller's, which is why it reads differently.
	ErrNoAdmission = errors.New("wasm: this store admits instances through a function and this tier cannot call one")
)

// The setting keys the composer writes for the `instances:` block, which are
// the vocabulary's own attribute paths.
const (
	settingAdmission = "instances/admission"
	settingLimit     = "instances/limit"
	settingValidator = "instances/validator/name"
	settingValidated = "instances/validator/namespace"
)

// reason is why an open did not happen, in the two shapes every interface here
// has an arm for.
type reason struct {
	// missing says the label names nothing this workload was granted, which
	// every interface reports as its own `no-such-…` arm because it reads as a
	// typo rather than as an outage.
	missing bool
	detail  string
}

func missingLabel(label string) *reason { return &reason{missing: true, detail: label} }
func denied(detail string) *reason      { return &reason{detail: detail} }

// addressed resolves what a guest named: which grant, whether it is legal to
// address that resource this way, and whether this instance is admitted.
//
// It is the whole of the check every interface's `open` does, in one place,
// because seven copies of "is this the resource or one of its instances" is
// seven places for the answer to differ.
func (r *Runtime) addressed(ctx context.Context, grants map[string]invoke.Grant, name, op string) (invoke.Grant, *reason) {
	label, instance, _ := strings.Cut(name, "/")

	grant, ok := grants[label]
	if !ok {
		return invoke.Grant{}, missingLabel(label)
	}
	if grant.Refused != "" {
		return invoke.Grant{}, denied(grant.Refused)
	}
	if !grant.Allows(op) {
		return invoke.Grant{}, denied(label + " cannot " + op)
	}

	many := grant.Settings[settingAdmission] != ""
	switch {
	case many && instance == "":
		// THE STORE HOLDS MANY AND THE CALLER NAMED NONE. Serving the resource
		// itself would be the defect this whole file exists to remove: one
		// store where the config says many, admitted by nobody.
		return invoke.Grant{}, denied(label + " holds many instances; name one as " + label + "/<instance>")

	case !many && instance != "":
		// AND THE OTHER DIRECTION. A store that holds one thing has no
		// instances to address, and inventing one would give a caller a
		// private corner of a resource its author declared to be single.
		return invoke.Grant{}, denied(label + " holds one instance and cannot be addressed as " + name)

	case !many:
		return grant, nil
	}

	if instance == "" || strings.Contains(instance, "/") {
		// An instance name is a KEY, not a path: it is looked up exactly, and
		// a separator in it would make one instance reachable by two names.
		return invoke.Grant{}, denied("an instance name may not be empty or contain /")
	}

	if r.admission == nil {
		return invoke.Grant{}, denied(ErrNoAdmission.Error())
	}
	if err := r.admission.Admit(ctx, grant, instance); err != nil {
		return invoke.Grant{}, denied(err.Error())
	}

	// THE INSTANCE IS PART OF THE HANDLE, which is what makes every backing
	// isolate one instance from another with the mechanism it already uses for
	// two resources.
	grant.Handle += "/" + instance
	return grant, nil
}
