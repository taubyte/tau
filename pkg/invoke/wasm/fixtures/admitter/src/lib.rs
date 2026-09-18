#[allow(warnings)]
mod bindings;

use bindings::{Guest, Opening, Verdict};

// A REAL INSTANCE VALIDATOR, and the only one in the tree.
//
// It exists for the reason fixtures/guard does: to prove the
// `taubyte:instance-validator` ABI against generated bindings nobody in this
// repository wrote. Before this world was built a validator was driven as an
// ordinary handler with the instance in a header, so what this fixture replaces
// is a stopgap rather than nothing.
//
// It decides from the INSTANCE NAME so a test can drive every arm:
//
//   "denied"    -> refused, which is a decision the caller may act on
//   "unsure"    -> undecided, which is a refusal to decide, for an operator
//   "echo"      -> refused, carrying the whole opening back as its reason
//   anything    -> admitted
//
// The echo arm is the round-trip proof. `admitted` carries nothing, so there is
// no room in a yes to show that the record arrived intact — and a transposition
// of two strings in `opening` is invisible to an assertion that only checks
// whether the instance was let in. Borrowing the refusal's string is what makes
// the caller, its scope and the store visible to a test.
struct Component;

impl Guest for Component {
    fn admit(o: Opening) -> Verdict {
        match o.instance.as_str() {
            "denied" => Verdict::Refused("that tenant is over its plan".to_string()),
            "unsure" => Verdict::Undecided("the plan service did not answer".to_string()),
            "echo" => Verdict::Refused(format!(
                "saw:{}/{}/{}/{}/[{}]:{}/{}",
                o.who.project,
                o.who.environment,
                o.who.kind,
                o.who.name,
                o.who.scope.join(","),
                o.what.kind,
                o.what.label,
            )),
            _ => Verdict::Admitted,
        }
    }
}

bindings::export!(Component with_types_in bindings);
