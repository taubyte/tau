#[allow(warnings)]
mod bindings;

use bindings::{Answer, Guest, Question};

// A REAL DNS RESOLVER, and the only one in the tree.
//
// It drives all three arms of `answer`, which is the whole shape of this world
// and the reason it is not a bare list:
//
//   "geo"    -> records, in zone-file notation relative to the apex
//   "quiet"  -> empty,  the name is here and holds nothing of that type
//   anything -> absent, no such name (NXDOMAIN, and cacheable as one)
struct Component;

impl Guest for Component {
    fn resolve(q: Question) -> Answer {
        let label = q.name.trim_end_matches('.').split('.').next().unwrap_or("");
        match label {
            "geo" => Answer::Records(vec![format!("geo 60 IN {} 203.0.113.9", q.type_)]),
            "quiet" => Answer::Empty,
            _ => Answer::Absent,
        }
    }
}

bindings::export!(Component with_types_in bindings);
