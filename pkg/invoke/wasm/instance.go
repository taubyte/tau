package wasm

import (
	"context"
	"errors"
	"strings"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/taubyte/tau/pkg/invoke"
)

// instance.go is how a guest names one of many: the `instances:` block on cache,
// database, storage and bus. The instance travels in the label, `sessions/room-42`,
// unambiguous since a label cannot contain a slash. An admitted instance is the
// grant's handle with the instance appended, so every backing isolates instances
// with the mechanism it already uses. Admission runs a function this tier cannot
// reach, so it asks an Admitter (services/worker/admit).

// Admitter decides whether a caller may open one instance of a store, asked per
// open on the request path, so repeat admissions should be cheap.
type Admitter interface {
	Admit(ctx context.Context, spec invoke.Spec, grant invoke.Grant, instance string) error
}

// WithAdmission decides whether an instance may exist; without it every instance of
// an `instances:` store is refused.
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
	settingAdmission = specs.InstancesKeyAdmission
	settingLimit     = specs.InstancesKeyLimit
	settingValidator = specs.InstancesKeyValidatorName
	settingValidated = specs.InstancesKeyValidatorNamespace
)

// reason is why an open did not happen, in the three shapes every interface
// here has an arm for.
type reason struct {
	// missing says the label names nothing granted: a `no-such-…` arm.
	missing bool

	// unavailable says the platform could not answer; a validator that could not run is
	// not a refusal.
	unavailable bool

	detail string
}

func missingLabel(label string) *reason { return &reason{missing: true, detail: label} }
func denied(detail string) *reason      { return &reason{detail: detail} }
func unreachable(detail string) *reason { return &reason{unavailable: true, detail: detail} }

// addressed resolves what a guest named: which grant, whether it may be addressed
// this way, and whether the instance is admitted. Every interface's open uses it.
func (r *Runtime) addressed(ctx context.Context, spec invoke.Spec, grants map[string]invoke.Grant, name, op string) (invoke.Grant, *reason) {
	label, instance, addressed := strings.Cut(name, "/")

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

	// An authorizer narrows and cannot widen: it runs after Allows, so the access block
	// stays the ceiling. It fails closed, unlike the instance validator: the target
	// said somebody else decides, so an unreachable authorizer grants nothing.
	if grant.Settings[settingAuthorizer] != "" {
		if r.authorizers == nil {
			return invoke.Grant{}, denied(label + " is guarded and this tier has nothing to ask")
		}
		ops, err := r.authorizers.Ops(ctx, spec, grant, op)
		switch {
		case errors.Is(err, ErrUndecided):
			return invoke.Grant{}, denied(err.Error())
		case err != nil:
			return invoke.Grant{}, denied(label + " is guarded and the guard could not be run: " + err.Error())
		}
		if !contains(ops, op) {
			return invoke.Grant{}, denied(label + "'s authorizer did not grant " + op)
		}
	}

	// A SEPARATOR WITH NOTHING AFTER IT IS A NAMED INSTANCE THAT IS EMPTY, not
	// an unnamed one. Reporting it as "name one" would tell a caller to do the
	// thing it just tried.
	if addressed && instance == "" {
		return invoke.Grant{}, denied("an instance name may not be empty or contain /")
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

	if strings.Contains(instance, "/") {
		// An instance name is a key: no separator, and no `..` that a store's path cleaning
		// would resolve into another store's rows.
		return invoke.Grant{}, denied("an instance name may not be empty or contain /")
	}

	if r.admission == nil {
		return invoke.Grant{}, unreachable(ErrNoAdmission.Error())
	}
	if err := r.admission.Admit(ctx, spec, grant, instance); err != nil {
		if errors.Is(err, ErrNotAdmitted) {
			return invoke.Grant{}, denied(err.Error())
		}
		return invoke.Grant{}, unreachable(err.Error())
	}

	// The instance is part of the handle.
	grant.Handle += "/" + instance
	return grant, nil
}
