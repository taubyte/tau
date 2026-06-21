// node:net stub. No raw TCP in the sandbox (the wasi:http host owns the
// socket). The IP-classification helpers are real (some libs validate addresses
// with them); the Socket/Server surface exists so imports resolve but connecting
// fails loudly.

export function isIP(input) {
  if (isIPv4(input)) return 4;
  if (isIPv6(input)) return 6;
  return 0;
}
export function isIPv4(s) {
  return typeof s === "string" && /^(25[0-5]|2[0-4]\d|[01]?\d?\d)(\.(25[0-5]|2[0-4]\d|[01]?\d?\d)){3}$/.test(s);
}
export function isIPv6(s) {
  return typeof s === "string" && /^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$/.test(s);
}

export class Socket {
  connect() { throw new Error("node:net Socket.connect is not available in the Taubyte sandbox"); }
  on() { return this; }
  once() { return this; }
  write() { return false; }
  end() { return this; }
  destroy() { return this; }
  setTimeout() { return this; }
  setNoDelay() { return this; }
  setKeepAlive() { return this; }
}

export class Server {
  listen() { throw new Error("node:net Server.listen is not available in the Taubyte sandbox (use the HTTP server bridge)"); }
  on() { return this; }
  close() { return this; }
}

export function createServer() { return new Server(); }
export function connect() { throw new Error("node:net.connect is not available in the Taubyte sandbox"); }
export const createConnection = connect;

export default { isIP, isIPv4, isIPv6, Socket, Server, createServer, connect, createConnection };
