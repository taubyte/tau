// A function in JavaScript: it answers the language, the method, the path and
// the body, so a caller can tell which component served it.
export function handle(req) {
  const head = new TextEncoder().encode(`js ${req.method} ${req.path} `);
  const body = new Uint8Array(head.length + req.body.length);
  body.set(head);
  body.set(req.body, head.length);
  return { status: 200, headers: [], body };
}
