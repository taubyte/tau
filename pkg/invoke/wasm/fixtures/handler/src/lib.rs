// The guest half of pkg/invoke/wasm's contract, as small as it can be while
// still exercising every field: it reads the method, the path, a header and the
// body, and answers with a status, a header and a body derived from them.
//
// It is a FIXTURE and not an example. What it exists to prove is that the
// host's lowering and lifting agree with a real component built by a real
// toolchain — the one thing no in-process fake can show.
#[allow(warnings)]
mod bindings;

use bindings::{Guest, Header, Request, Response};

struct Component;

impl Guest for Component {
    fn handle(req: Request) -> Response {
        // Echo the path back in a header, so a test can see that a string
        // survived the round trip in both directions.
        let mut headers = vec![Header {
            name: "x-path".to_string(),
            value: req.path.clone(),
        }];

        // And any header the caller sent, prefixed, so the list type is
        // exercised rather than just constructed.
        for h in &req.headers {
            headers.push(Header {
                name: format!("x-echo-{}", h.name),
                value: h.value.clone(),
            });
        }

        // The status says which method arrived, so a test can tell a real
        // dispatch from a constant.
        let status = match req.method.as_str() {
            "GET" => 200,
            "POST" => 201,
            "" => 204,
            _ => 405,
        };

        let mut body = req.method.clone().into_bytes();
        body.push(b' ');
        body.extend_from_slice(&req.body);

        Response {
            status,
            headers,
            body,
        }
    }
}

bindings::export!(Component with_types_in bindings);
