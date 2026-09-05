// The guest half of `taubyte:handler/cache`: a component that actually uses
// the capability, built by a real toolchain against the same wit the host
// implements.
//
// It is a FIXTURE and not an example. Every branch below exists to make one
// host-side claim observable from inside a guest — that a grant is enforced
// before the store is touched, that a miss is its own arm of the variant, and
// that bytes and lists survive both directions.
//
// The request's path selects the operation and its body carries the value, so
// one component exercises the whole interface without needing four.
#[allow(warnings)]
mod bindings;

use bindings::taubyte::handler::{cache, stream};
use bindings::{Guest, Header, Request, Response};

struct Component;

// how names an error arm, so a test can assert WHICH refusal came back rather
// than only that something did.
fn how(e: &cache::Error) -> String {
    match e {
        cache::Error::NoSuchCache(name) => format!("no-such-cache:{}", name),
        cache::Error::Denied(why) => format!("denied:{}", why),
        cache::Error::NotFound => "not-found".to_string(),
        cache::Error::TooLarge => "too-large".to_string(),
        cache::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// whyStream names a stream error arm, so a test asserts WHICH refusal came back.
fn why_stream(e: &stream::Error) -> String {
    match e {
        stream::Error::NoSuchStream(name) => format!("no-such-stream:{}", name),
        stream::Error::Denied(why) => format!("denied:{}", why),
        stream::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

fn reply(status: u16, body: Vec<u8>) -> Response {
    Response {
        status,
        headers: vec![Header {
            name: "x-cache".to_string(),
            value: "yes".to_string(),
        }],
        body,
    }
}

impl Guest for Component {
    fn handle(req: Request) -> Response {
        // The path is "/<op>/<cache>/<key>", so a test drives every branch
        // through one export.
        let parts: Vec<&str> = req.path.trim_start_matches('/').splitn(3, '/').collect();
        let op = parts.first().copied().unwrap_or("");
        let name = parts.get(1).copied().unwrap_or("");
        let key = parts.get(2).copied().unwrap_or("");

        match op {
            "get" => match cache::get(name, key) {
                Ok(value) => reply(200, value),
                Err(e) => reply(404, how(&e).into_bytes()),
            },
            "put" => match cache::put(name, key, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, how(&e).into_bytes()),
            },
            "delete" => match cache::delete(name, key) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, how(&e).into_bytes()),
            },
            "list" => match cache::list(name, key) {
                Ok(names) => reply(200, names.join(",").into_bytes()),
                Err(e) => reply(403, how(&e).into_bytes()),
            },
            // The other capability, driven the same way: the path names the
            // stream and the body is the message.
            "publish" => match stream::publish(name, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_stream(&e).into_bytes()),
            },
            // A round trip through the cache in one call: write the body, read
            // it back, and answer with what came back. It is the only branch
            // that proves the bytes a guest wrote are the bytes it gets.
            "roundtrip" => {
                if let Err(e) = cache::put(name, key, &req.body) {
                    return reply(403, how(&e).into_bytes());
                }
                match cache::get(name, key) {
                    Ok(value) => reply(200, value),
                    Err(e) => reply(404, how(&e).into_bytes()),
                }
            }
            _ => reply(400, b"unknown op".to_vec()),
        }
    }
}

bindings::export!(Component with_types_in bindings);
