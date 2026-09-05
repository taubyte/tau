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

use bindings::taubyte::handler::{
    bus, cache, database, document, kv, queue, secret, storage, stream, vector,
};
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

// why_doc names a document error arm.
fn why_doc(e: &document::Error) -> String {
    match e {
        document::Error::NoSuchDatabase(name) => format!("no-such-database:{}", name),
        document::Error::Denied(why) => format!("denied:{}", why),
        document::Error::NotFound => "not-found".to_string(),
        document::Error::Malformed(why) => format!("malformed:{}", why),
        document::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// why_vec names a vector error arm.
fn why_vec(e: &vector::Error) -> String {
    match e {
        vector::Error::NoSuchDatabase(name) => format!("no-such-database:{}", name),
        vector::Error::Denied(why) => format!("denied:{}", why),
        vector::Error::WrongWidth(why) => format!("wrong-width:{}", why),
        vector::Error::Malformed(why) => format!("malformed:{}", why),
        vector::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// floats reads a comma-separated body as an embedding, so a test can send one
// without a JSON parser in the guest.
fn floats(body: &[u8]) -> Vec<f32> {
    String::from_utf8_lossy(body)
        .split(',')
        .filter(|s| !s.trim().is_empty())
        .filter_map(|s| s.trim().parse::<f32>().ok())
        .collect()
}

// why_queue names a queue error arm.
fn why_queue(e: &queue::Error) -> String {
    match e {
        queue::Error::NoSuchQueue(name) => format!("no-such-queue:{}", name),
        queue::Error::Denied(why) => format!("denied:{}", why),
        queue::Error::NotWritable(why) => format!("not-writable:{}", why),
        queue::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// why_storage names a storage error arm. `full` is its own arm because a
// caller acts on it, and a test asserting on the body proves the host produced
// that one rather than folding it into an outage.
fn why_storage(e: &storage::Error) -> String {
    match e {
        storage::Error::NoSuchStorage(name) => format!("no-such-storage:{}", name),
        storage::Error::Denied(why) => format!("denied:{}", why),
        storage::Error::NotFound => "not-found".to_string(),
        storage::Error::Full(why) => format!("full:{}", why),
        storage::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// why_secret names a secret error arm.
fn why_secret(e: &secret::Error) -> String {
    match e {
        secret::Error::NoSuchSecret(name) => format!("no-such-secret:{}", name),
        secret::Error::Denied(why) => format!("denied:{}", why),
        secret::Error::NotSet => "not-set".to_string(),
        secret::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// why_bus names a bus error arm.
fn why_bus(e: &bus::Error) -> String {
    match e {
        bus::Error::NoSuchBus(name) => format!("no-such-bus:{}", name),
        bus::Error::Denied(why) => format!("denied:{}", why),
        bus::Error::Unavailable(why) => format!("unavailable:{}", why),
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
            // The document data model. The body is the document, or the
            // filter, and the answer is the JSON that came back — so a test
            // reads exactly what crossed.
            "doc-get" => match document::get(name, key) {
                Ok(raw) => reply(200, raw),
                Err(e) => reply(404, why_doc(&e).into_bytes()),
            },
            "doc-put" => match document::put(name, key, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_doc(&e).into_bytes()),
            },
            "doc-delete" => match document::delete(name, key) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_doc(&e).into_bytes()),
            },
            "doc-find" => {
                let filter = String::from_utf8_lossy(&req.body).to_string();
                match document::find(name, &filter) {
                    Ok(raw) => reply(200, raw),
                    Err(e) => reply(403, why_doc(&e).into_bytes()),
                }
            }
            // The vector data model. The body is the embedding as
            // comma-separated floats; the key doubles as the id, or as k.
            "vec-upsert" => match vector::upsert(name, key, &floats(&req.body), "") {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_vec(&e).into_bytes()),
            },
            "vec-search" => {
                let k: i32 = key.parse().unwrap_or(0);
                match vector::search(name, &floats(&req.body), k) {
                    Ok(found) => {
                        let rendered: Vec<String> = found
                            .iter()
                            .map(|m| format!("{}:{:.3}:{}", m.id, m.score, m.meta))
                            .collect();
                        reply(200, rendered.join(",").into_bytes())
                    }
                    Err(e) => reply(403, why_vec(&e).into_bytes()),
                }
            }
            "vec-delete" => match vector::delete(name, key) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_vec(&e).into_bytes()),
            },
            // The key is the item's id and the body is its payload.
            "send" => match queue::send(name, key, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_queue(&e).into_bytes()),
            },
            // Read at the moment of the call, which is what makes a rotation
            // take effect without a redeploy.
            "secret" => match secret::get(name) {
                Ok(value) => reply(200, value),
                Err(e) => reply(403, why_secret(&e).into_bytes()),
            },
            // Object storage. The key is the object's name and the body is
            // its bytes.
            "obj-put" => match storage::put(name, key, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_storage(&e).into_bytes()),
            },
            "obj-get" => match storage::get(name, key) {
                Ok(value) => reply(200, value),
                Err(e) => reply(404, why_storage(&e).into_bytes()),
            },
            // `head` is the whole reason storage is not kv: what is known about
            // an object without pulling it through the ABI.
            "obj-head" => match storage::head(name, key) {
                Ok(entry) => reply(
                    200,
                    format!("{}:{}:{}", entry.name, entry.size, entry.modified).into_bytes(),
                ),
                Err(e) => reply(404, why_storage(&e).into_bytes()),
            },
            "obj-delete" => match storage::delete(name, key) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_storage(&e).into_bytes()),
            },
            // The key is the prefix. The answer is one name per line, so a
            // record list survives both directions of the ABI observably.
            "obj-list" => match storage::entries(name, key) {
                Ok(found) => reply(
                    200,
                    found
                        .iter()
                        .map(|e| format!("{}:{}", e.name, e.size))
                        .collect::<Vec<_>>()
                        .join("\n")
                        .into_bytes(),
                ),
                Err(e) => reply(403, why_storage(&e).into_bytes()),
            },
            "broadcast" => match bus::publish(name, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_bus(&e).into_bytes()),
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
