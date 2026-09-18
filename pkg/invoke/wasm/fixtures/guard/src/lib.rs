#[allow(warnings)]
mod bindings;

use bindings::{Decision, Guest, Question};

// A REAL AUTHORIZER, and the only one in the tree.
//
// It exists to prove the `taubyte:authorizer` ABI against generated bindings
// nobody in this repository wrote — the same argument fixtures/README.md makes
// for the handler and the cacher. A hand-rolled stand-in would be this
// repository's idea of the world on both sides of the call.
//
// It decides from what it was HANDED so that a test can drive every arm:
//
//   label "undecidable"  -> undecided, which is a refusal to decide
//   label "forbidden"    -> granted(nothing), which is a decided refusal
//   anything else        -> granted a FIXED SET, plus one synthetic entry
//                           encoding the whole question
//
// THE SET IS FIXED AND DOES NOT ECHO `q.op`, which is the contract rather than a
// simplification: an authorizer is asked about one operation and answers with
// every operation this caller gets, so a host is entitled to keep the answer.
// A guard that returned only the op it was handed would be broken in a way
// nothing can detect — and would appear to work until something kept its answer,
// at which point the first question a workload happened to ask would decide
// every later one. Granting get and put but never delete is what lets a test see
// a denial come out of a KEPT set rather than a fresh call.
//
// The synthetic entry is the other half. An op the host never asked about cannot
// be confused for one it did, and the encoded question is how a test proves the
// caller, the target and the instance survived being lowered into a record and
// lifted back — a transposition of two strings in that record is invisible to
// every other kind of assertion.
struct Component;

impl Guest for Component {
    fn authorize(q: Question) -> Decision {
        if q.what.label == "undecidable" {
            return Decision::Undecided("this guard has no opinion about that one".to_string());
        }
        if q.what.label == "forbidden" {
            return Decision::Granted(vec![]);
        }

        Decision::Granted(vec![
            "get".to_string(),
            "put".to_string(),
            format!(
                "saw:{}/{}/{}/{}/[{}]:{}/{}/{}",
                q.who.project,
                q.who.environment,
                q.who.kind,
                q.who.name,
                q.who.scope.join(","),
                q.what.kind,
                q.what.label,
                q.what.instance,
            ),
        ])
    }
}

bindings::export!(Component with_types_in bindings);
