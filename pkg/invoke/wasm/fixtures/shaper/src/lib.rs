#[allow(warnings)]
mod bindings;

use bindings::{Guest, Header, Outcome, Request, Response};

// A REAL GATEWAY TRANSFORMER, and the only one in the tree.
//
// It drives both arms of `outcome`, which is the whole shape of this world:
//
//   header `x-shape: reject`  -> answer, and the chain stops here
//   anything else             -> forward, with the request CHANGED
//
// The change is deliberate rather than a pass-through. A transformer that
// returned what it was given would pass whether or not the host read the
// forwarded request back out of the guest at all — and reading a `request` out
// of a component is something nothing else in this tree does, so it is the half
// most likely to be wrong.
//
// It adds a header carrying what it saw, and prefixes the body. Both survive
// into whatever runs next, which is how a test proves the modified request and
// not the original one is what carried on.
struct Component;

impl Guest for Component {
    fn transform(req: Request) -> Outcome {
        let shape = req
            .headers
            .iter()
            .find(|h| h.name.eq_ignore_ascii_case("x-shape"))
            .map(|h| h.value.clone())
            .unwrap_or_default();

        if shape == "reject" {
            return Outcome::Answer(Response {
                status: 418,
                headers: vec![Header {
                    name: "x-shaped-by".to_string(),
                    value: "shaper".to_string(),
                }],
                body: b"the chain stopped here".to_vec(),
            });
        }

        let mut headers = req.headers.clone();
        headers.push(Header {
            name: "x-shaped".to_string(),
            value: format!("{}:{}", req.method, req.path),
        });

        let mut body = b"shaped:".to_vec();
        body.extend_from_slice(&req.body);

        Outcome::Forward(Request {
            method: req.method,
            path: req.path,
            headers,
            body,
        })
    }
}

bindings::export!(Component with_types_in bindings);
