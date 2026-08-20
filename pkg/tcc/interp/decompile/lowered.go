package decompile

import (
	"github.com/taubyte/tau/pkg/tcc/interp"
	"github.com/taubyte/tau/pkg/tcc/object"
	"github.com/taubyte/tau/pkg/tcc/transform"
)

// HOW DEEP A DECOMPILE GOES.
//
// Compiling lowers, so the object holds resources nobody wrote and access entries nobody
// granted. Written back as they stand they land in somebody's repository as though they
// had been asked for, and the next compile lowers them again — the round trip stops being
// one the first time it runs.
//
// So the default is HALF WAY: what somebody wrote, and nothing else. The object says what
// the compile lowered (interp.Lowered, recorded beside the project) and this takes it back
// out — the produced resources go, and a resource that was handed an entry gives back the
// op that was handed to it. Just that op: the entry may name a target the author also
// reaches for reasons of their own, and dropping the whole entry would delete config
// nobody generated.
//
// ALL THE WAY is the other depth, reached with WithAsLowered, for a reader that wants what
// the compiler produced rather than what an author would recognise — a console showing
// what one workflow became, a diff against what is deployed.
//
// Either way the record itself is not config: no vocabulary declares a `lowered` group, so
// writing it out would produce a file that cannot be read back.

// authoredOnly is the half-way depth as a transform: it runs over the project, after the
// chroot unwrap so the ids are still ids — the driver that follows renames every resource
// from its id to its name, and the record speaks in ids.
type authoredOnly struct {
	lowered interp.Lowered
	// keep is WithAsLowered: the record still comes off, nothing else does.
	keep bool
}

func (a *authoredOnly) Process(ct transform.Context[object.Refrence], o object.Object[object.Refrence]) (object.Object[object.Refrence], error) {
	// Normally gone with the envelope the unwrap discarded; this covers the object that
	// carries a record without one.
	o.Delete(interp.LoweredKey)
	if a.keep {
		return o, nil
	}

	// An entry is addressed by the id of the resource that declares it, so the lookup is
	// by accessor and the walk asks it once per resource it passes.
	granted := map[string][]interp.Granted{}
	for _, g := range a.lowered.Entries {
		granted[g.On] = append(granted[g.On], g)
	}

	a.strip(o, granted)
	return o, nil
}

// strip walks the compiled tree looking for keys the record names. It reads no schema on
// purpose: a resource is keyed by its id at whatever depth its scope sits, and an id is
// the one key nothing else in the tree is keyed by — an access entry is keyed by the
// target's name, a nested group by the author's. So "a child whose key is an id the record
// knows" needs no vocabulary to be sure of.
func (a *authoredOnly) strip(o object.Object[object.Refrence], granted map[string][]interp.Granted) {
	// Children() is a snapshot, so removing while walking it is safe.
	for _, key := range o.Children() {
		if a.lowered.Produced(key) {
			o.Delete(key)
			continue
		}
		child, err := o.Child(key).Object()
		if err != nil {
			continue
		}
		for _, g := range granted[key] {
			revoke(child, g)
		}
		a.strip(child, granted)
	}
}

// revoke takes one value back off the path a lowering wrote it to, and takes the empty
// containers with it: a list with nothing in it is not an empty list, it is a field the
// author never typed, and the same goes for the maps that held it.
//
// It reads no schema and no vocabulary. A path is a path — walk it, remove the value,
// and prune upward while nothing is left. That is the whole of undoing a lowering.
func revoke(res object.Object[object.Refrence], g interp.Granted) {
	if len(g.Path) == 0 {
		return
	}

	// walk in, keeping every container on the way so an emptied one can be pruned
	holders := []object.Object[object.Refrence]{res}
	at := res
	for _, seg := range g.Path[:len(g.Path)-1] {
		next, err := at.Child(seg).Object()
		if err != nil {
			return
		}
		holders = append(holders, next)
		at = next
	}

	field := g.Path[len(g.Path)-1]
	var left []string
	listed := false
	for _, v := range values(at, field) {
		listed = true
		if v != g.Value {
			left = append(left, v)
		}
	}
	switch {
	case listed && len(left) > 0:
		at.Set(field, left)
		return
	case listed:
		at.Delete(field)
	default:
		if s, ok := any(at.Get(field)).(string); ok && s == g.Value {
			at.Delete(field)
		} else {
			return // somebody changed it; it is theirs now
		}
	}

	// prune the containers that are now empty, innermost first
	for i := len(holders) - 1; i > 0; i-- {
		if len(holders[i].Children()) > 0 || len(holders[i].Flat()) > 0 {
			return
		}
		holders[i-1].Delete(g.Path[i-1])
	}
}

// values reads a field that holds a list. It compiles as []string; an object that has
// been through a wire format hands the same list back as []any, and both are the field.
func values(o object.Object[object.Refrence], field string) []string {
	switch v := any(o.Get(field)).(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// loweredOf reads the record off the compiled object while it is still there — the chroot
// unwrap keeps the project and drops everything beside it, this one included. Only the
// record is flattened: Flat() over the whole object would copy the project to read the few
// hundred bytes sitting next to it.
func loweredOf(obj Object) interp.Lowered {
	rec, err := obj.Child(interp.LoweredKey).Object()
	if err != nil {
		return interp.LoweredOf(nil)
	}
	return interp.LoweredOf(map[string]any{interp.LoweredKey: rec.Flat()})
}
