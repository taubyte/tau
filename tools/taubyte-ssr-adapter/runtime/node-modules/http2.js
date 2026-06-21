// node:http2 stub. The wasi:http host serves HTTP/1; http2 servers/clients aren't
// available. Constructors exist so imports resolve; using them fails loudly.
function unavailable() { throw new Error("node:http2 is not available in the Taubyte sandbox (the host serves HTTP/1; use the standard request handler)"); }
export const createServer = unavailable;
export const createSecureServer = unavailable;
export const connect = unavailable;
export const constants = {};
export const getDefaultSettings = () => ({});
export default { createServer, createSecureServer, connect, constants, getDefaultSettings };
