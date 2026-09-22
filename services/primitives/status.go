package primitives

import (
	"errors"
	"fmt"

	"connectrpc.com/connect"

	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
)

// status.go is the one table, and it is read in BOTH directions.
//
// A refusal leaves this service three ways and arrives back two, and before
// this they were five hand-written switches: the handler encoded a Go error as
// a status, the tier translated the same Go error into the arm a guest sees,
// the client translated a status back into that arm, and the clock translated a
// status back into a sentinel. Nothing checked that any of them agreed.
//
// IT IS NOT A TIDINESS POINT, AND ONE OF THEM ALREADY SHIPPED WRONG.
// node/dispatch.go filtered ordinary refusals on primitives.ErrNotLeading,
// which only the LOCAL arm ever produced — the remote arm dropped the sentinel
// and arrived as a bare connect status — so the filter was silent in-process
// and warned every two seconds on exactly the composition it was written for.
// That is what a mapping written twice costs, and it is invisible until a node
// is composed the other way.
//
// IT MATTERS BECAUSE THE ARM IS NOT THE CALLER'S CHOICE. p2p/rpc's short-circuit
// hands a caller this node's own service or a stream onto another node's, and
// nothing above this package can tell which it got. So the two arms agreeing
// cannot be something each is written to do — it has to be something neither
// can express differently.
//
// # What a status can and cannot carry
//
// The code is all that survives a wire; the sentinel does not. So a status that
// exactly one refusal in the table claims comes back AS that refusal, and a
// status several share comes back as none of them — the sentence the answering
// node wrote says which, and a guess would be worse than silence. Which of the
// two a refusal is, is derived from the table rather than asserted in a
// comment: see [restores] and the property test over every row.

// refusal is one row: a refusal this service names, the status it travels as,
// and the arm the compute tier answers a guest with.
type refusal struct {
	err  error
	code connect.Code

	// arm is the pkg/invoke/wasm sentinel a guest's ABI has a variant for, or
	// nil where it has none. It is on the ROW and not on the translation, so
	// the local arm and the remote arm cannot pick differently.
	arm error
}

// refusals is the table.
//
// THE DEFAULT — everything not named here — IS Unavailable AND NOT Internal,
// for the reason services/database gives: an unnamed failure came out of a
// store, and that is the caller's cue to retry somewhere else. Internal says
// "stop, this request is broken", which for a replicated store is the wrong
// instruction.
var refusals = []refusal{
	// AN ADDRESS WITH A HOLE IN IT is the caller's, and the four are told apart
	// by their sentence rather than by their status.
	{err: ErrNoProject, code: connect.CodeInvalidArgument},
	{err: ErrNoEnvironment, code: connect.CodeInvalidArgument},
	{err: ErrNoID, code: connect.CodeInvalidArgument},
	{err: ErrNoItemID, code: connect.CodeInvalidArgument},

	// A CURSOR THIS LOG CANNOT READ FROM IS THE CALLER'S TO FIX, by starting
	// from the beginning — which is what makes it the caller's mistake rather
	// than a miss. It is a status of its own so the tier can be handed back the
	// arm its ABI actually has.
	{err: ErrBadCursor, code: connect.CodeOutOfRange, arm: wasmtier.ErrBadCursor},

	// A NODE THAT RUNS NO CLOCK IS A FailedPrecondition AND NOT Unimplemented.
	// It serves the call — every other primitives node does the same thing with
	// it — and what is missing is a compute view this node happens not to have,
	// so the leader's answer is to task somebody else rather than to stop.
	{err: ErrNotASync, code: connect.CodeFailedPrecondition},
	{err: ErrWrongType, code: connect.CodeFailedPrecondition},
	{err: ErrNoClock, code: connect.CodeFailedPrecondition},

	// UNIMPLEMENTED AND NOT FailedPrecondition, because nothing the caller can
	// change makes the call succeed: this cloud does not serve that type or
	// that consensus, and the fix is a release rather than a retry.
	{err: ErrTypeUnserved, code: connect.CodeUnimplemented},
	{err: ErrConsensusUnserved, code: connect.CodeUnimplemented},
	{err: ErrInstanceUnserved, code: connect.CodeUnimplemented},

	// ONE STATUS STANDS FOR TWO REFUSALS HERE — a resource nobody declared, and
	// an item this queue does not hold — so neither name survives the wire and
	// the arm is what a caller acts on.
	{err: ErrNotDeclared, code: connect.CodeNotFound, arm: wasmtier.ErrNotFound},
	{err: ErrNoSuchDelivery, code: connect.CodeNotFound, arm: wasmtier.ErrNotFound},

	// A STALE DELIVERY IS NOT A MISS AND NOT A RETRY. The item exists and
	// somebody else has it now; the caller's work was not applied and must not
	// be repeated against this token.
	{err: ErrStaleDelivery, code: connect.CodeAlreadyExists},

	{err: ErrTooLarge, code: connect.CodeResourceExhausted, arm: wasmtier.ErrRecordTooLarge},

	// A NODE THAT MAY NOT WRITE IS ITS OWN STATUS, told apart from Unavailable
	// on purpose: both are retries, and only this one carries a node to retry
	// AT. Folding it into the generic one would tell a guest the cloud was down
	// when one node was simply not the writer.
	{err: ErrNotLeading, code: connect.CodeAborted, arm: wasmtier.ErrNotWritable},

	{err: ErrForming, code: connect.CodeUnavailable},
	{err: ErrClosed, code: connect.CodeUnavailable},
}

// restores is the sentinel a status comes back as: the refusal that status
// names ALONE. A status two rows share is absent from this map, because
// restoring one of the two would assert something the wire did not say.
//
// armByCode is the same lookup for the tier's arm. Two rows sharing a status
// must share an arm, or a caller's variant would depend on which of the two
// refusals the answering node had — which is the disagreement this file exists
// to make impossible, so it is refused where the table is built rather than
// found on a request.
var (
	restores  = map[connect.Code]error{}
	armByCode = map[connect.Code]error{}
)

// init derives both lookups from the table, and REFUSES A TABLE THAT CANNOT BE
// READ BOTH WAYS rather than letting it answer inconsistently at run time. The
// condition is static — it is a literal above — so a table that panics here
// panics on the first build that loads this package, never on a request.
func init() {
	claims := map[connect.Code]int{}
	for _, r := range refusals {
		if claims[r.code] > 0 && armByCode[r.code] != r.arm {
			panic(fmt.Sprintf("primitives: %s carries two arms: %v and %v",
				r.code, armByCode[r.code], r.arm))
		}
		claims[r.code]++
		armByCode[r.code] = r.arm
		restores[r.code] = r.err
	}

	// A STATUS TWO ROWS CLAIM NAMES NEITHER. Restoring one of the two would
	// assert something the wire did not say.
	for code, count := range claims {
		if count > 1 {
			delete(restores, code)
		}
	}
}

// ─────────────────────────────── forwards ────────────────────────────────

// wire is how a failure leaves this service: the table read forwards. Every
// connect.NewError in this package goes through it.
func wire(err error) error {
	if err == nil {
		return nil
	}
	return connect.NewError(codeOf(err), err)
}

// codeOf is the status a refusal travels as.
func codeOf(err error) connect.Code {
	if r, named := rowOf(err); named {
		return r.code
	}
	return connect.CodeUnavailable
}

// rowOf is the table row a refusal matches, walked in declaration order.
func rowOf(err error) (refusal, bool) {
	for _, r := range refusals {
		if errors.Is(err, r.err) {
			return r, true
		}
	}
	return refusal{}, false
}

// tierError puts the tier's arm on a refusal this process produced, so a guest
// sees the same variant whether the namespace was here or on another node.
//
// EVERYTHING ELSE PASSES THROUGH WITH THIS SERVICE'S OWN SENTENCE. Folding a
// refusal the tier has no arm for into one it does — too-large, say — would
// tell a guest its record was oversized when the truth is that this cloud does
// not serve a crdt log.
func tierError(err error) error {
	if err == nil {
		return nil
	}
	r, _ := rowOf(err)
	return armed(r.arm, err)
}

// ─────────────────────────────── backwards ───────────────────────────────

// restored is the table read backwards: a status from another node becomes the
// refusal a local call would have produced, where the status names one.
//
// THE STATUS STAYS IN THE CHAIN BESIDE THE NAME, and so does the sentence the
// answering node wrote. A wrap that kept only the name would leave
// connect.CodeOf answering Unknown about an answer the service was perfectly
// clear about, and an operator reading this node's guess at what happened
// rather than what did.
func restored(err error) error {
	if err == nil {
		return nil
	}
	if sentinel, named := restores[connect.CodeOf(err)]; named {
		return fmt.Errorf("%w: %w", sentinel, err)
	}
	return err
}

// remoteError is what a caller of [Client] gets: the refusal restored, with the
// tier's arm in front of it — the same two things tierError puts there for a
// call that never left this process.
func remoteError(err error) error {
	if err == nil {
		return nil
	}
	return armed(armByCode[connect.CodeOf(err)], restored(err))
}

// taskError is remoteError WITHOUT the tier's arm, and the difference is who is
// asking. A tasking has no guest and no ABI behind it — the caller is this
// service's own leader, and the one thing it acts on is whether the window was
// taken — so an arm meant for a guest's variant would be noise in a chain
// nothing reads it from.
func taskError(err error) error { return restored(err) }

// armed puts an arm in front of a refusal, keeping both in the chain.
//
// BOTH SENTINELS STAY. The tier's arm is what a guest sees and this service's
// is what an operator reads — NotLeading carries the node that may write — so
// wrapping one in the other's SENTENCE would throw away the half nobody above
// the seam can reconstruct.
func armed(arm, err error) error {
	if arm == nil {
		return err
	}
	return fmt.Errorf("%w: %w", arm, err)
}
