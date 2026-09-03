// Package attempt is WHICH JOB AND WHICH RUN OF IT a set of rows belongs to,
// and it is a package for the same reason backend/row and backend/section are:
// the two things that say WHOSE rows these are must not be assignable by the
// code that composes them.
//
// # What it is for
//
// Its caller (services/build/backend) writes a build's logs and assets from
// OUTSIDE every section — they are content a run produces as it goes, keyed by
// (job, attempt), decided by no read of a job record — so the mechanism that
// holds every other write, a handle that exists only inside the transition that
// decided it, does not reach them. What holds these instead is that the writer
// is CONSTRUCTED for one attempt and cannot be re-pointed at another:
//
//	Of{job: "…", n: 3}          does not compile — both fields are unexported
//	other := *sink              copies the same attempt; it names no other job
//	other.of.job = neighbour    does not compile — the field is another
//	                            package's
//	other.of = Nth(x, 0)        compiles, and is A CALL: its callers are
//	                            enumerated in the caller, and so is every
//	                            assignment to that field
//
// The one that is still spellable is the one round ten could not remove, and
// its caller says so where it enumerates it rather than leaving a reader to
// find it: a constructor is a name, and a name is defeated by respelling.
//
// # What it deliberately is not
//
// It is not a key and it builds none. The grammar of a key is store.go's, in
// one file, so that there is exactly one place that has to agree with itself
// about the shape of one. What this gives that file is an ARGUMENT it cannot be
// handed a bare string for: attemptLogKey(of, at) takes a job and a run of it
// that something constructed, where attemptLogKey(id, n, at) took two values any
// expression could spell.
package attempt

// Of is one job's one attempt.
//
// It is comparable, copyable and an ordinary value: passing one around is how
// the row builders are told whose rows they are composing. What no caller can
// do is make one out of nothing, or change what one says.
type Of struct {
	job string
	n   uint32
}

// Nth is the only way to make one.
func Nth(job string, n uint32) Of { return Of{job: job, n: n} }

// Job is the job these rows belong to.
func (o Of) Job() string { return o.job }

// Number is which run of it. It counts the deliveries the job's record has
// seen, so that two runners either side of a leadership flap cannot interleave
// into one another's rows and a retry cannot overwrite the previous run's.
func (o Of) Number() uint32 { return o.n }
