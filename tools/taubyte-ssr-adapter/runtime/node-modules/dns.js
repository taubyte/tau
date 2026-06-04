// node:dns stub — no resolver in the sandbox (outbound access goes through fetch
// to declared hosts, not raw DNS). Exports exist so imports resolve; calls report
// failure rather than crashing at load.
function unsupported(name) {
  return function (...args) {
    const cb = args[args.length - 1];
    const err = new Error("node:dns." + name + " is not available in the Taubyte sandbox");
    if (typeof cb === "function") cb(err);
    else throw err;
  };
}
export const lookup = unsupported("lookup");
export const lookupService = unsupported("lookupService");
export const resolve = unsupported("resolve");
export const resolve4 = unsupported("resolve4");
export const resolve6 = unsupported("resolve6");
export const reverse = unsupported("reverse");
export const setServers = () => {};
export const getServers = () => [];
export const promises = {
  lookup: () => Promise.reject(new Error("node:dns is not available in the Taubyte sandbox")),
  resolve: () => Promise.reject(new Error("node:dns is not available in the Taubyte sandbox")),
  resolve4: () => Promise.reject(new Error("node:dns is not available in the Taubyte sandbox")),
};
export default { lookup, lookupService, resolve, resolve4, resolve6, reverse, setServers, getServers, promises };
