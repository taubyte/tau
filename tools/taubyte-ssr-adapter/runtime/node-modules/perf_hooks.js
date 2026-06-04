// node:perf_hooks shim — performance timing over the platform clock.
const _perf = (typeof globalThis !== "undefined" && globalThis.performance)
  ? globalThis.performance
  : { now: () => Date.now(), timeOrigin: 0 };
export const performance = _perf.now ? _perf : {
  now: () => Date.now(), timeOrigin: 0,
  mark() {}, measure() {}, clearMarks() {}, clearMeasures() {},
  getEntriesByName() { return []; }, getEntriesByType() { return []; }, getEntries() { return []; },
};
export class PerformanceObserver {
  constructor(cb) { this._cb = cb; }
  observe() {} disconnect() {} takeRecords() { return []; }
}
export function monitorEventLoopDelay() {
  return { enable() {}, disable() {}, reset() {}, percentile() { return 0; }, mean: 0, max: 0, min: 0, stddev: 0, exceeds: 0 };
}
export function createHistogram() { return { record() {}, recordDelta() {}, percentile() { return 0; }, reset() {} }; }
export const constants = {};
export default { performance, PerformanceObserver, monitorEventLoopDelay, createHistogram, constants };
