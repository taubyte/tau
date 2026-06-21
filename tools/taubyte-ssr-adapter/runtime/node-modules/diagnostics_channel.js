// node:diagnostics_channel shim — a working in-process channel registry (Fastify
// and others publish request lifecycle events through it). No tracing host, but
// publish/subscribe round-trips so instrumented code runs.
class Channel {
  constructor(name) { this.name = name; this._subs = []; }
  get hasSubscribers() { return this._subs.length > 0; }
  publish(message) { for (const s of this._subs.slice()) { try { s(message, this.name); } catch (e) {} } }
  subscribe(fn) { this._subs.push(fn); }
  unsubscribe(fn) { const n = this._subs.length; this._subs = this._subs.filter((s) => s !== fn); return this._subs.length < n; }
}
const channels = {};
export function channel(name) { return channels[name] || (channels[name] = new Channel(name)); }
export function hasSubscribers(name) { return !!channels[name] && channels[name].hasSubscribers; }
export function subscribe(name, fn) { channel(name).subscribe(fn); }
export function unsubscribe(name, fn) { return channel(name).unsubscribe(fn); }
export function tracingChannel(nameOrChannels) {
  const base = typeof nameOrChannels === "string" ? nameOrChannels : "";
  const ch = (suffix) => channel(base ? "tracing:" + base + ":" + suffix : suffix);
  return {
    start: ch("start"), end: ch("end"), asyncStart: ch("asyncStart"),
    asyncEnd: ch("asyncEnd"), error: ch("error"),
    subscribe() {}, unsubscribe() { return true; },
    traceSync(fn, ctx, thisArg, ...args) { return fn.apply(thisArg, args); },
    tracePromise(fn, ctx, thisArg, ...args) { return Promise.resolve(fn.apply(thisArg, args)); },
    traceCallback(fn, pos, ctx, thisArg, ...args) { return fn.apply(thisArg, args); },
  };
}
export { Channel };
export default { channel, hasSubscribers, subscribe, unsubscribe, tracingChannel, Channel };
