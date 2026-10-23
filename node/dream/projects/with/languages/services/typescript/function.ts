// A function in TypeScript: it answers the language, the method, the path and
// the body, so a caller can tell which component served it.
interface Header {
  name: string;
  value: string;
}

interface Request {
  method: string;
  path: string;
  headers: Header[];
  body: Uint8Array;
}

interface Response {
  status: number;
  headers: Header[];
  body: Uint8Array;
}

export function handle(req: Request): Response {
  const head = new TextEncoder().encode(`typescript ${req.method} ${req.path} `);
  const body = new Uint8Array(head.length + req.body.length);
  body.set(head);
  body.set(req.body, head.length);
  return { status: 200, headers: [], body };
}
