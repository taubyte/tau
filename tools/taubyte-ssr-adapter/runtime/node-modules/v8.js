// node:v8 stub. The real V8 serialization format and heap introspection aren't
// available off-V8; serialize/deserialize fall back to a JSON-based encoding
// (sufficient for the structured-config/clone uses libraries reach for), and
// the heap/flag helpers are inert. Not wire-compatible with Node's v8 format.

export function serialize(value) {
  const json = JSON.stringify(value);
  return typeof Buffer !== "undefined" ? Buffer.from(json, "utf8") : new TextEncoder().encode(json);
}

export function deserialize(buf) {
  const text = typeof Buffer !== "undefined" && Buffer.isBuffer(buf)
    ? buf.toString("utf8")
    : new TextDecoder().decode(buf);
  return JSON.parse(text);
}

export function getHeapStatistics() {
  return {
    total_heap_size: 0, total_heap_size_executable: 0, total_physical_size: 0,
    total_available_size: 0, used_heap_size: 0, heap_size_limit: 0,
    malloced_memory: 0, peak_malloced_memory: 0, does_zap_garbage: 0,
    number_of_native_contexts: 0, number_of_detached_contexts: 0,
  };
}
export function getHeapSpaceStatistics() { return []; }
export function setFlagsFromString() {}
export function getHeapSnapshot() { throw new Error("node:v8.getHeapSnapshot is not available in the Taubyte sandbox"); }
export function takeCoverage() {}
export function stopCoverage() {}

export default {
  serialize, deserialize, getHeapStatistics, getHeapSpaceStatistics,
  setFlagsFromString, getHeapSnapshot, takeCoverage, stopCoverage,
};
