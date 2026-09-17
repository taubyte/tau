// The guest half of `taubyte:cache/store`: a component that actually uses
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

// ONE PACKAGE PER CAPABILITY, so what a guest reaches is named by what it is
// rather than by which host happens to serve it. The aliases keep the branches
// below reading as verbs.
use bindings::taubyte::bus::publisher as bus;
use bindings::taubyte::cache::store as cache;
use bindings::taubyte::channel::dialer as channel;
use bindings::taubyte::document::store as document;
use bindings::taubyte::kv::store as kv;
use bindings::taubyte::log::records as logrecords;
use bindings::taubyte::queue::producer as queue;
use bindings::taubyte::secret::vault as secret;
use bindings::taubyte::sql::store as database;
use bindings::taubyte::storage::objects as storage;
use bindings::taubyte::stream::publisher as stream;
use bindings::taubyte::vector::store as vector;
use bindings::taubyte::workflow::runs as workflow;
use bindings::taubyte::workload::identity as workload;
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

// why_log names a log error arm.
fn why_log(e: &logrecords::Error) -> String {
    match e {
        logrecords::Error::NoSuchLog(name) => format!("no-such-log:{}", name),
        logrecords::Error::Denied(why) => format!("denied:{}", why),
        logrecords::Error::NotWritable(why) => format!("not-writable:{}", why),
        logrecords::Error::TooLarge(why) => format!("too-large:{}", why),
        logrecords::Error::BadCursor(why) => format!("bad-cursor:{}", why),
        logrecords::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// why_channel names a channel error arm.
fn why_channel(e: &channel::Error) -> String {
    match e {
        channel::Error::NoSuchChannel(name) => format!("no-such-channel:{}", name),
        channel::Error::Denied(why) => format!("denied:{}", why),
        channel::Error::NoAnswer(why) => format!("no-answer:{}", why),
        channel::Error::Failed(why) => format!("failed:{}", why),
        channel::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// render_info renders every field of an object's metadata record, so a test
// asserting one of them is asserting the whole lowering.
fn render_info(i: &storage::Info) -> String {
    let meta = i
        .metadata
        .iter()
        .map(|m| format!("{}={}", m.name, m.value))
        .collect::<Vec<_>>()
        .join(",");
    format!(
        "{}:{}:{}:{}:{}:[{}]",
        i.key, i.size, i.modified, i.etag, i.content_type, meta
    )
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
        storage::Error::BadRequest(why) => format!("bad-request:{}", why),
        storage::Error::Unavailable(why) => format!("unavailable:{}", why),
    }
}

// why_workflow names a workflow error arm.
fn why_workflow(e: &workflow::Error) -> String {
    match e {
        workflow::Error::NoSuchWorkflow(name) => format!("no-such-workflow:{}", name),
        workflow::Error::Denied(why) => format!("denied:{}", why),
        workflow::Error::NotWritable(why) => format!("not-writable:{}", why),
        workflow::Error::Unavailable(why) => format!("unavailable:{}", why),
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

// header reads one request header, case-insensitively — a header name is not
// case-sensitive and a fixture that assumed it was would fail on whatever
// spelling the platform happened to use.
fn header(req: &Request, name: &str) -> Option<String> {
    req.headers
        .iter()
        .find(|h| h.name.eq_ignore_ascii_case(name))
        .map(|h| h.value.clone())
}

impl Guest for Component {
    fn handle(req: Request) -> Response {
        // A VALIDATOR IS A HANDLER, because the world the config names does not
        // exist: `implements: instance-validator` is one of seven host worlds
        // and only `handler` is built. So an instance decision arrives as a
        // request carrying the instance in a header, and this fixture answers
        // it — refusing anything called `denied` so a test can drive both arms
        // of the decision rather than only the yes.
        // A STEP'S FUNCTION IS A HANDLER TOO, and what a step hands on is what
        // its function returned — so this echoes the body. That makes the run's
        // state visible from outside: what came out of the last step is what
        // went into the first.
        if header(&req, "x-taubyte-step").is_some() {
            return reply(200, req.body.clone());
        }

        if let Some(instance) = header(&req, "x-taubyte-instance") {
            return if instance == "denied" {
                reply(403, b"refused".to_vec())
            } else {
                reply(200, instance.into_bytes())
            };
        }

        // The path is "/<op>/<cache>/<key>", so a test drives every branch
        // through one export.
        let parts: Vec<&str> = req.path.trim_start_matches('/').splitn(3, '/').collect();
        let op = parts.first().copied().unwrap_or("");
        let key = parts.get(2).copied().unwrap_or("");

        // `~` IN THE NAME IS A `/`, and it is here because this fixture's path
        // is itself slash-separated. A store that holds many instances is
        // addressed as `label/instance` in the host ABI, so a test needs a way
        // to say one through a path that already uses the separator — writing
        // `sessions~room-42` is that way, and it is the fixture's encoding
        // rather than anything the interface knows about.
        let name = parts.get(1).copied().unwrap_or("").replace('~', "/");
        let name = name.as_str();

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
                    // BOTH HALVES of the outcome record, which a bare row
                    // count could not carry.
                    Ok(o) => reply(
                        200,
                        format!("{}/{}", o.affected, o.last_insert_id).into_bytes(),
                    ),
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
                    Ok(o) => reply(200, format!("{}", o.affected).into_bytes()),
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
            // The key is the run's id and the body is what the run carries.
            "start" => match workflow::start(name, key, &req.body) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_workflow(&e).into_bytes()),
            },
            // Object storage. The key is the object's name and the body is
            // its bytes.
            // PutObject. The content type and the user metadata travel with
            // the bytes, and the answer is the etag they were stored under.
            "obj-put" => {
                let content_type = header(&req, "x-taubyte-content-type").unwrap_or_default();
                let metadata: Vec<storage::Metadata> = req
                    .headers
                    .iter()
                    .filter(|h| h.name.starts_with("x-meta-"))
                    .map(|h| storage::Metadata {
                        name: h.name["x-meta-".len()..].to_string(),
                        value: h.value.clone(),
                    })
                    .collect();
                match storage::put(name, key, &req.body, &content_type, &metadata) {
                    Ok(etag) => reply(200, etag.into_bytes()),
                    Err(e) => reply(403, why_storage(&e).into_bytes()),
                }
            }
            // GetObject: the bytes AND what is known about them, which is what
            // makes a record inside a record observable from the guest.
            "obj-get" => match storage::get(name, key) {
                Ok(object) => reply(200, object.body),
                Err(e) => reply(404, why_storage(&e).into_bytes()),
            },
            // The same call read for its metadata half instead of its bytes.
            "obj-meta" => match storage::get(name, key) {
                Ok(object) => reply(200, render_info(&object.info).into_bytes()),
                Err(e) => reply(404, why_storage(&e).into_bytes()),
            },
            // `head` is the whole reason storage is not kv: what is known about
            // an object without pulling it through the ABI.
            "obj-head" => match storage::head(name, key) {
                Ok(info) => reply(200, render_info(&info).into_bytes()),
                Err(e) => reply(404, why_storage(&e).into_bytes()),
            },
            "obj-delete" => match storage::delete(name, key) {
                Ok(()) => reply(204, Vec::new()),
                Err(e) => reply(403, why_storage(&e).into_bytes()),
            },
            // ListObjectsV2. The key is the prefix; the delimiter, the page
            // size and the continuation token come off headers so one branch
            // exercises the whole request record.
            "obj-list" => {
                let request = storage::ListingRequest {
                    prefix: key.to_string(),
                    delimiter: header(&req, "x-taubyte-delimiter").unwrap_or_default(),
                    max_keys: header(&req, "x-taubyte-max-keys")
                        .and_then(|v| v.parse().ok())
                        .unwrap_or(0),
                    continuation_token: header(&req, "x-taubyte-token").unwrap_or_default(),
                };
                match storage::list(name, &request) {
                    Ok(listing) => {
                        let mut out: Vec<String> = listing
                            .objects
                            .iter()
                            .map(|i| format!("{}:{}", i.key, i.size))
                            .collect();
                        // A COMMON PREFIX IS BRACKETED so a test can tell one
                        // from an object whose name happens to end in the
                        // delimiter — the prefix already carries it.
                        out.extend(listing.common_prefixes.iter().map(|p| format!("[{}]", p)));
                        if listing.truncated {
                            out.push(format!("...{}", listing.next_continuation_token));
                        }
                        reply(200, out.join("\n").into_bytes())
                    }
                    Err(e) => reply(403, why_storage(&e).into_bytes()),
                }
            }
            // An append-only log. `append` answers the cursor the record
            // landed at; `read` walks forward from one.
            "append" => match logrecords::append(name, &req.body) {
                Ok(cursor) => reply(200, cursor.into_bytes()),
                Err(e) => reply(403, why_log(&e).into_bytes()),
            },
            // The key is the cursor to read after; empty reads from the start.
            "read" => {
                let limit = header(&req, "x-taubyte-limit")
                    .and_then(|v| v.parse().ok())
                    .unwrap_or(0);
                match logrecords::read(name, key, limit) {
                    Ok(found) => reply(
                        200,
                        found
                            .iter()
                            .map(|r| {
                                format!(
                                    "{}:{}:{}",
                                    r.cursor,
                                    r.at,
                                    String::from_utf8_lossy(&r.data)
                                )
                            })
                            .collect::<Vec<_>>()
                            .join("\n")
                            .into_bytes(),
                    ),
                    Err(e) => reply(403, why_log(&e).into_bytes()),
                }
            }
            // In-project addressing: what v1 called p2p.
            "dial" => match channel::dial(name, &req.body) {
                Ok(answer) => reply(200, answer),
                Err(e) => reply(403, why_channel(&e).into_bytes()),
            },
            // What this workload IS. No grant, no label — it names nothing.
            "whoami" => {
                let me = workload::get();
                reply(
                    200,
                    format!(
                        "{}/{}/{}/{}/[{}]/{}/{}/{}",
                        me.project,
                        me.environment,
                        me.kind,
                        me.name,
                        me.scope.join(","),
                        me.revision,
                        me.artifact,
                        me.node,
                    )
                    .into_bytes(),
                )
            }
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
