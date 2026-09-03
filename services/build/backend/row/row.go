// Package row is ONE ROW OF A REPLICATED ENTRY, and it is a package rather than
// a type in its caller for one reason: a row that names a key MUST NOT BE
// SPELLABLE anywhere but here.
//
// # What it is for
//
// Its caller (services/build/backend) spent nine rounds making a write
// impossible to make outside the transition that decided it, about the job that
// transition read. Every one of those rounds moved a DOOR — a token, a key, an
// id — and the ninth found that the doors no longer mattered, because the thing
// they guarded could be assembled from scratch beside them:
//
//	s.ops = append(s.ops, raft.BatchOp{Set: &raft.SetCommand{
//	        Key: refLastKey("github.com/evil/repo", "refs/heads/main"), …}})
//
// One line, inside the very method that owns that row, appended a second row
// naming ANOTHER REPOSITORY'S ref anchor — the commit the next build of that
// ref diffs from. It passed the whole suite. raft.BatchOp is an ordinary
// exported struct with exported fields, so composing one is not a call, and a
// scan keyed on WHICH FUNCTION WAS CALLED has nothing to see: there is no call.
//
// So the element type is this one. Op's only field is unexported, and its
// caller is another package, so:
//
//	append(e.ops, raft.BatchOp{…})    does not compile — the element type is Op
//	append(e.ops, Op{op: …})          does not compile — op is not exported
//	Op{}                              compiles, and names nothing at all
//
// THE ZERO VALUE IS THE ONE A CALLER CAN STILL WRITE, and it is empty by
// construction: there is no field it can fill, so it carries no key, no value
// and no verb. raft's own contract is that exactly one of Set and Delete is
// non-nil (pkg/raft/interfaces.go), and a cluster refuses a batch containing
// anything else with ErrInvalidCommand rather than applying it. So the worst a
// zero Op can do is fail an entry that a section had already decided — which is
// a refusal, not a write, and never a row about somebody else.
//
// # What it deliberately is not
//
// It is not a key builder and it does not validate one. WHICH key a row may
// name is its caller's question — the key must be composed from the subject the
// section was entered on, which is a fact about that package's handle and means
// nothing here. This package answers only "can a row carrying a key be made
// anywhere but by calling Set or Delete", and the answer is no.
//
// It is also not a batch. A []Op is an ordinary slice its caller owns, appends
// to and counts; what it cannot do is put anything in one that did not come
// from here.
package row

import "github.com/taubyte/tau/pkg/raft"

// Op is one row of an entry: a key, and either the value to write there or the
// fact that it is a delete.
//
// THE FIELD IS UNEXPORTED AND THAT IS THE WHOLE TYPE. Everything else about it
// — that it is comparable, copyable, an ordinary value — is deliberate: its
// caller keeps a slice of them and hands that slice around inside one entry,
// and none of that is a way to change what a row says.
type Op struct{ op raft.BatchOp }

// Set is a row with a value at a key.
func Set(key string, value []byte) Op {
	return Op{op: raft.BatchOp{Set: &raft.SetCommand{Key: key, Value: value}}}
}

// Delete is a row removed.
func Delete(key string) Op {
	return Op{op: raft.BatchOp{Delete: &raft.DeleteCommand{Key: key}}}
}

// Ops is the ONE WAY OUT, and it exists so that the boundary at which rows
// become a raft batch is a call somebody can enumerate.
//
// Its callers are counted in the package that uses this one: they are the two
// places that reach raft.Cluster.Batch at all, and a third would be a write
// that no section decided.
func Ops(rows []Op) []raft.BatchOp {
	out := make([]raft.BatchOp, len(rows))
	for i, r := range rows {
		out[i] = r.op
	}
	return out
}
