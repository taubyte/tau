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

use bindings::taubyte::handler::{cache, database, kv, stream};
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

// why_db names a database error arm.
fn why_db(e: &database::Error) -> String {
    match e {
        database::Error::NoSuchDatabase(name) => format!("no-such-database:{}", name),
        database::Error::Denied(why) => format!("denied:{}", why),
        database::Error::Unavailable(why) => format!("unavailable:{}", why),
        database::Error::Failed(why) => format!("failed:{}", why),
    }
}

// render turns one value into text a test can read, keeping the CASE visible —
// which is what proves an integer crossed as an integer and not as its digits.
fn render(v: &database::Value) -> String {
    match v {
        database::Value::Null => "null".to_string(),
        database::Value::Text(s) => format!("text:{}", s),
        database::Value::Integer(i) => format!("integer:{}", i),
        database::Value::Real(f) => format!("real:{}", f),
        database::Value::Boolean(b) => format!("boolean:{}", b),
        database::Value::Blob(b) => format!("blob:{}", b.len()),
    }
}

// why_kv names a kv error arm.
fn why_kv(e: &kv::Error) -> String {
    match e {
        kv::Error::NoSuchDatabase(name) => format!("no-such-database:{}", name),
        kv::Error::Denied(why) => format!("denied:{}", why),
        kv::Error::NotFound => "not-found".to_string(),
        kv::Error::Unavailable(why) => format!("unavailable:{}", why),
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
            // The statement is the body, and the answer is the result set
            // rendered so every value's CASE is visible.
            "query" => {
                let sql = String::from_utf8_lossy(&req.body).to_string();
                match database::query(name, &sql, &[]) {
                    Ok(rows) => {
                        let mut out = rows.columns.join(",");
                        for v in &rows.values {
                            out.push('|');
                            out.push_str(&render(v));
                        }
                        reply(200, out.into_bytes())
                    }
                    Err(e) => reply(500, why_db(&e).into_bytes()),
                }
            }
            "exec" => {
                let sql = String::from_utf8_lossy(&req.body).to_string();
                match database::exec(name, &sql, &[]) {
                    Ok(n) => reply(200, format!("{}", n).into_bytes()),
                    Err(e) => reply(500, why_db(&e).into_bytes()),
                }
            }
            // Every argument case, bound in one call, so the lowering of each
            // is exercised rather than only the string one.
            "bind" => {
                let args = vec![
                    database::Value::Null,
                    database::Value::Text("hello".to_string()),
                    database::Value::Integer(-42),
                    database::Value::Real(1.5),
                    database::Value::Boolean(true),
                    database::Value::Blob(vec![1, 2, 3]),
                ];
                match database::exec(name, "insert", &args) {
                    Ok(n) => reply(200, format!("{}", n).into_bytes()),
                    Err(e) => reply(500, why_db(&e).into_bytes()),
                }
            }
            // The kv data model, driven the same way as the cache so a test
            // can see the two are different doors onto different kinds.
            "kv-get" => match kv::get(name, key) {
                Ok(value) => reply(200, value),
                Err(e) => reply(404, why_kv(&e).into_bytes()),
            },
            "kv-put" => match kv::put(name, key, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_kv(&e).into_bytes()),
            },
            "kv-delete" => match kv::delete(name, key) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_kv(&e).into_bytes()),
            },
            "kv-keys" => match kv::keys(name, key) {
                Ok(names) => reply(200, names.join(",").into_bytes()),
                Err(e) => reply(403, why_kv(&e).into_bytes()),
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
