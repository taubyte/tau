#[allow(warnings)]
mod bindings;

use bindings::{Connection, Decision, Destination, Guest};

// A REAL PROXY ROUTER, and the only one in the tree.
//
// It drives all three arms, chosen by what it has been shown, so a test can
// reach each of them:
//
//   fewer than 4 bytes   -> more(4), which is how a router asks to see enough
//   "nope"               -> refuse, a decision and not a failure
//   "pg" + anything      -> to a destination in another namespace, on a port
//   anything else        -> to a destination in this proxy's own scope
//
// WHAT IT RETURNS ECHOES WHAT IT WAS GIVEN — the source address and the first
// bytes — because a router that ignored its arguments and returned a constant
// would satisfy every assertion about which arm was taken.
struct Component;

impl Guest for Component {
    fn route(conn: Connection, peeked: Vec<u8>) -> Decision {
        // A protocol identifies itself in its first bytes, so a router that has
        // not seen enough of them says so rather than guessing.
        if peeked.len() < 4 {
            return Decision::More(4);
        }

        if peeked.starts_with(b"nope") {
            return Decision::Refuse(format!("{} is not welcome", conn.source));
        }

        if peeked.starts_with(b"pg") {
            return Decision::To(Destination {
                kind: "application".to_string(),
                namespace: "/shop".to_string(),
                name: "db".to_string(),
                port: "pg".to_string(),
            });
        }

        Decision::To(Destination {
            kind: "application".to_string(),
            namespace: String::new(),
            // What it was handed, so a test can see the argument rather than
            // only which arm was taken: the name it answers on, and the four
            // bytes it decided from.
            name: format!(
                "saw-{}-{}",
                conn.host.replace('.', "_"),
                String::from_utf8_lossy(&peeked[..4])
            ),
            port: String::new(),
        })
    }
}

bindings::export!(Component with_types_in bindings);
