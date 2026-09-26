#[allow(warnings)]
mod bindings;

use bindings::{Decision, Guest, Request};

// A REAL HTTP AUTHORIZER, and the only one in the tree.
//
// It drives both arms of `decision`, which is the whole shape of this world:
//
//   header `x-deny: <why>`  -> denied, and the reason is the OPERATOR's
//   anything else           -> granted, with the ops the caller gets
//
// THE GRANT IS DELIBERATELY PARTIAL. It answers `subscribe` and never
// `publish`, so a browser this admits may listen and may not send — which is
// the thing a yes/no cannot say, and the reason this world answers with a list
// rather than a bool.
struct Component;

impl Guest for Component {
    fn authorize(req: Request) -> Decision {
        if let Some(h) = req
            .headers
            .iter()
            .find(|h| h.name.eq_ignore_ascii_case("x-deny"))
        {
            return Decision::Denied(h.value.clone());
        }
        Decision::Granted(vec!["subscribe".to_string()])
    }
}

bindings::export!(Component with_types_in bindings);
