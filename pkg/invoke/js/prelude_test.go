package js_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	js "github.com/taubyte/tau/pkg/invoke/js"
)

// checks runs in a realm and answers with every check that failed, one per
// line, or "ok".
const checks = `
const failed = [];
const eq = (name, got, want) => {
  const g = JSON.stringify(got), w = JSON.stringify(want);
  if (g !== w) failed.push(name + ": got " + g + ", want " + w);
};
const throws = async (name, kind, f) => {
  try { await f(); failed.push(name + ": did not throw"); }
  catch (e) { if (!(e instanceof kind)) failed.push(name + ": threw " + e); }
};
const bytes = (u8) => Array.from(u8);

export default {
  async fetch() {
    // TextEncoder
    const enc = new TextEncoder();
    eq("encoding", enc.encoding, "utf-8");
    eq("encode ascii", bytes(enc.encode("hi")), [104, 105]);
    eq("encode empty", bytes(enc.encode()), []);
    eq("encode multibyte", bytes(enc.encode("é€😀")), [0xc3, 0xa9, 0xe2, 0x82, 0xac, 0xf0, 0x9f, 0x98, 0x80]);
    eq("encode lone surrogate", bytes(enc.encode("a\uD800b")), [0x61, 0xef, 0xbf, 0xbd, 0x62]);
    eq("encode non-string", bytes(enc.encode(12)), [0x31, 0x32]);
    const into = new Uint8Array(4);
    eq("encodeInto stops at a whole character", enc.encodeInto("ab€c", into), { read: 2, written: 2 });
    eq("encodeInto bytes", bytes(into), [0x61, 0x62, 0, 0]);
    eq("encodeInto exactly full", enc.encodeInto("ab€c", new Uint8Array(5)), { read: 3, written: 5 });
    eq("encodeInto all", enc.encodeInto("😀", new Uint8Array(4)), { read: 2, written: 4 });
    await throws("encodeInto needs a Uint8Array", TypeError, () => enc.encodeInto("a", []));

    // TextDecoder
    const dec = new TextDecoder();
    eq("decoder", [dec.encoding, dec.fatal, dec.ignoreBOM], ["utf-8", false, false]);
    eq("round trip", dec.decode(enc.encode("héllo € 😀")), "héllo € 😀");
    eq("decode nothing", dec.decode(), "");
    eq("decode an ArrayBuffer", dec.decode(enc.encode("ab").buffer), "ab");
    eq("decode a DataView", dec.decode(new DataView(enc.encode("xyz").buffer, 1, 1)), "y");
    eq("replacement", dec.decode(new Uint8Array([0x61, 0xff, 0x62])), "a�b");
    eq("maximal subpart", dec.decode(new Uint8Array([0xe2, 0x82, 0x41])), "�A");
    eq("truncated at the end", dec.decode(new Uint8Array([0x61, 0xf0, 0x9f])), "a�");
    eq("BOM stripped", dec.decode(new Uint8Array([0xef, 0xbb, 0xbf, 0x41])), "A");
    eq("BOM kept", new TextDecoder("utf-8", { ignoreBOM: true }).decode(new Uint8Array([0xef, 0xbb, 0xbf, 0x41])), "\uFEFFA");
    eq("labels", new TextDecoder(" UTF8 ").encoding, "utf-8");
    await throws("an unknown label", RangeError, () => new TextDecoder("latin1"));
    await throws("decode a string", TypeError, () => dec.decode("abc"));
    const fatal = new TextDecoder("utf-8", { fatal: true });
    eq("fatal flag", fatal.fatal, true);
    await throws("fatal refuses 0xff", TypeError, () => fatal.decode(new Uint8Array([0xff])));
    eq("fatal decodes after a refusal", fatal.decode(new Uint8Array([0x41])), "A");
    const stream = new TextDecoder();
    const euro = enc.encode("€");
    eq("stream holds a split character", stream.decode(euro.slice(0, 1), { stream: true }), "");
    eq("stream finishes it", stream.decode(euro.slice(1), { stream: true }), "€");
    eq("stream flushes", stream.decode(), "");
    const bomStream = new TextDecoder();
    eq("a BOM split across chunks", bomStream.decode(new Uint8Array([0xef, 0xbb]), { stream: true }), "");
    eq("is still stripped", bomStream.decode(new Uint8Array([0xbf, 0x41]), { stream: true }), "A");
    eq("and only once", bomStream.decode(new Uint8Array([0xef, 0xbb, 0xbf])), "\uFEFF");
    const fatalStream = new TextDecoder("utf-8", { fatal: true });
    eq("fatal stream waits", fatalStream.decode(new Uint8Array([0xe2]), { stream: true }), "");
    await throws("fatal stream refuses at the flush", TypeError, () => fatalStream.decode());

    // Headers
    const h = new Headers({ "Content-Type": "text/plain", "X-A": " padded \t" });
    eq("case-insensitive get", h.get("content-type"), "text/plain");
    eq("trimmed", h.get("x-a"), "padded");
    eq("missing", h.get("nope"), null);
    h.append("x-a", "second");
    eq("combined", h.get("X-A"), "padded, second");
    h.set("X-A", "only");
    eq("set replaces every value", h.get("x-a"), "only");
    h.set("x-new", "v");
    eq("set adds", h.has("X-NEW"), true);
    h.delete("x-new");
    eq("delete", h.has("x-new"), false);
    h.append("set-cookie", "a=1");
    h.append("set-cookie", "b=2");
    eq("cookies kept apart", h.getSetCookie(), ["a=1", "b=2"]);
    eq("entries sorted and combined", [...h], [["content-type", "text/plain"], ["set-cookie", "a=1"], ["set-cookie", "b=2"], ["x-a", "only"]]);
    eq("keys", [...h.keys()], ["content-type", "set-cookie", "set-cookie", "x-a"]);
    eq("values", [...h.values()], ["text/plain", "a=1", "b=2", "only"]);
    const seen = [];
    h.forEach(function (v, k, self) { seen.push([k, v, self === h, this.tag]); }, { tag: 1 });
    eq("forEach", seen[0], ["content-type", "text/plain", true, 1]);
    eq("from pairs", new Headers([["a", "1"], ["b", "2"]]).get("b"), "2");
    eq("from Headers", new Headers(h).get("x-a"), "only");
    eq("from nothing", [...new Headers(null)], []);
    eq("tag", Object.prototype.toString.call(h), "[object Headers]");
    await throws("a bad name", TypeError, () => h.get("bad name"));
    await throws("a bad value", TypeError, () => h.set("x", "a\nb"));
    await throws("a bad pair", TypeError, () => new Headers([["only-one"]]));
    await throws("a bad init", TypeError, () => new Headers("str"));

    // Request
    const r = new Request("https://example.test/a", { method: "post", headers: { "X-K": "v" }, body: "payload" });
    eq("request", [r.method, r.url, r.headers.get("x-k"), r.redirect, r.bodyUsed], ["POST", "https://example.test/a", "v", "follow", false]);
    eq("a string body sets its type", r.headers.get("content-type"), "text/plain;charset=UTF-8");
    const copy = r.clone();
    eq("text", await r.text(), "payload");
    eq("used", r.bodyUsed, true);
    await throws("read twice", TypeError, () => r.text());
    await throws("clone a used body", TypeError, () => r.clone());
    eq("clone reads alone", await copy.json().catch((e) => e.name), "SyntaxError");
    eq("custom methods are kept", new Request("u", { method: "Patch" }).method, "Patch");
    eq("from a Request", new Request(new Request("u", { method: "PUT", body: "x" })).method, "PUT");
    eq("a Request's body carries over", await new Request(new Request("u", { method: "PUT", body: "x" })).text(), "x");
    eq("tag", Object.prototype.toString.call(r), "[object Request]");
    await throws("GET with a body", TypeError, () => new Request("u", { body: "x" }));
    await throws("a bad method", TypeError, () => new Request("u", { method: "a b" }));
    await throws("a forbidden method", TypeError, () => new Request("u", { method: "trace" }));
    eq("null body reads empty", await new Request("u").text(), "");
    eq("null body reads twice", [await new Request("u").arrayBuffer().then((b) => b.byteLength)], [0]);

    // Response
    const res = new Response("hi");
    eq("defaults", [res.status, res.ok, res.statusText, res.type, res.url, res.redirected], [200, true, "", "default", "", false]);
    eq("bytes", bytes(await res.clone().bytes()), [104, 105]);
    eq("arrayBuffer", (await res.clone().arrayBuffer()).byteLength, 2);
    eq("text", await res.text(), "hi");
    eq("not ok", new Response(null, { status: 503 }).ok, false);
    eq("json", await Response.json([1, { a: 2 }]).json(), [1, { a: 2 }]);
    eq("json keeps a type", Response.json(1, { headers: { "content-type": "application/x" } }).headers.get("content-type"), "application/x");
    eq("json types itself", Response.json(1).headers.get("content-type"), "application/json");
    eq("a string body types itself", new Response("x").headers.get("content-type"), "text/plain;charset=UTF-8");
    eq("a typed body keeps a given type", new Response("x", { headers: { "content-type": "text/x" } }).headers.get("content-type"), "text/x");
    eq("no body, no headers", [...new Response().headers], []);
    eq("a clone keeps the type", new Response("x").clone().headers.get("content-type"), "text/plain;charset=UTF-8");
    const untyped = new Response("x");
    untyped.headers.delete("content-type");
    eq("a type deleted stays deleted", untyped.headers.has("content-type"), false);
    eq("redirect", [Response.redirect("/x").status, Response.redirect("/x").headers.get("location")], [302, "/x"]);
    eq("binary bodies are copied", bytes(await new Response(new Uint8Array([1, 2, 3]).subarray(1)).bytes()), [2, 3]);
    eq("an ArrayBuffer body", bytes(await new Response(new Uint8Array([9]).buffer).bytes()), [9]);
    eq("anything else is its string", await new Response(42).text(), "42");
    eq("tag", Object.prototype.toString.call(res), "[object Response]");
    const own = (o, k) => Object.prototype.hasOwnProperty.call(o, k);
    eq("body methods are the class's own", [own(Request.prototype, "text"), own(Response.prototype, "json"), own(Response.prototype, "bodyUsed")], [true, true, true]);
    eq("as on the web platform", [Object.getPrototypeOf(Request.prototype), Object.getPrototypeOf(Response.prototype)].every((p) => p === Object.prototype), true);
    await throws("a status out of range", RangeError, () => new Response(null, { status: 600 }));
    await throws("a fractional status", RangeError, () => new Response(null, { status: 200.5 }));
    await throws("a body on a 204", TypeError, () => new Response("x", { status: 204 }));
    await throws("a bad redirect", RangeError, () => Response.redirect("/x", 200));
    await throws("json of undefined", TypeError, () => Response.json(undefined));
    await throws("a body read as another type", TypeError, () => Response.prototype.text.call({}));

    // The globals are the platform's, and replacing them changes nothing it does.
    eq("globals", [typeof Headers, typeof Request, typeof Response, typeof TextEncoder, typeof TextDecoder, typeof console], Array(6).fill("function").slice(0, 5).concat(["object"]));
    eq("not enumerable", Object.keys(globalThis).includes("Response"), false);
    return new Response(failed.length ? failed.join("\n") : "ok");
  },
};
`

func TestThePreludeBehavesAsTheWebPlatformDoes(t *testing.T) {
	modes := []js.Mode{js.ModeBytecode, js.DefaultMode()}
	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			f := newFixture(t, checks, js.WithMode(mode))
			resp := serve(t, f.start(), get("/"))
			require.Equal(t, 200, resp.Status, f.logs.err())
			require.Equal(t, "ok", string(resp.Body))
		})
	}
}

func TestAUsedOrForeignResponseIsA500(t *testing.T) {
	f := newFixture(t, `
export default {
  async fetch(request) {
    if (request.url.endsWith("/used")) { const r = new Response("x"); await r.text(); return r; }
    if (request.url.endsWith("/fake")) return { status: 200, headers: new Headers(), body: "x" };
    if (request.url.endsWith("/replaced")) { globalThis.Response = class { constructor() { this.status = 201; } }; return new Response("x"); }
    return new Response("fine");
  },
};
`)
	inst := f.start()
	for _, path := range []string{"/used", "/fake", "/replaced"} {
		require.Equal(t, 500, serve(t, inst, get(path)).Status, path)
	}
	require.Contains(t, f.logs.err(), "Body has already been used")
	require.Equal(t, 2, strings.Count(f.logs.err(), "did not answer with a Response"))
}
