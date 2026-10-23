// A function in Rust: it answers the language, the method, the path and the
// body, so a caller can tell which component served it.
#[allow(warnings)]
mod bindings;

use bindings::{Guest, Request, Response};

struct Component;

impl Guest for Component {
    fn handle(req: Request) -> Response {
        let mut body = format!("rust {} {} ", req.method, req.path).into_bytes();
        body.extend_from_slice(&req.body);
        Response { status: 200, headers: vec![], body }
    }
}

bindings::export!(Component with_types_in bindings);
