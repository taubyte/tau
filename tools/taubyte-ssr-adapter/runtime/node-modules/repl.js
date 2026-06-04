// node:repl stub — there is no interactive REPL in the sandbox. Exists so imports
// resolve (frameworks reference it without using it on the server path).
export function start() { throw new Error("node:repl is not available in the Taubyte sandbox"); }
export function REPLServer() {}
export const REPL_MODE_SLOPPY = Symbol("repl-sloppy");
export const REPL_MODE_STRICT = Symbol("repl-strict");
export default { start, REPLServer, REPL_MODE_SLOPPY, REPL_MODE_STRICT };
