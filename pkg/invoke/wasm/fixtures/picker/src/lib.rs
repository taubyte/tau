#[allow(warnings)]
mod bindings;

use bindings::{Decision, Destination, Guest, Header, Request, Response};

// A REAL GATEWAY ROUTER, and the only one in the tree.
//
// It drives all three arms, chosen by the path so a test can reach each:
//
//   /answer  -> answer, the router served it itself
//   /nothing -> none, which is a DECISION and not a failure
//   /shop/*  -> to a destination in another namespace
//   anything -> to a destination in the gateway's own scope
//
// The destination it returns ECHOES the path it was given, which is how a test
// proves the request crossed intact: a router that ignored its argument and
// returned a constant would pass every assertion about the arms.
struct Component;

impl Guest for Component {
    fn route(req: Request) -> Decision {
        match req.path.as_str() {
            "/answer" => Decision::Answer(Response {
                status: 302,
                headers: vec![Header {
                    name: "location".to_string(),
                    value: "/elsewhere".to_string(),
                }],
                body: Vec::new(),
            }),
            "/nothing" => Decision::None("no rule matched".to_string()),
            path if path.starts_with("/shop/") => Decision::To(Destination {
                kind: "function".to_string(),
                namespace: "/shop".to_string(),
                name: "checkout".to_string(),
            }),
            path => Decision::To(Destination {
                kind: "function".to_string(),
                namespace: String::new(),
                // The path, minus its leading slash, so a test can see what the
                // router was handed rather than only which arm it took.
                name: format!("saw{}", path.replace('/', "_")),
            }),
        }
    }
}

bindings::export!(Component with_types_in bindings);
