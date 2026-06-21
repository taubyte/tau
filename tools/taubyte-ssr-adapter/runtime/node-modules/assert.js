// node:assert shim — the common assertion surface used as invariants across the
// npm ecosystem (Koa asserts status codes, etc.). Authored as CommonJS so
// `require("assert")` returns the callable assert function with methods attached
// (the shape Node exposes: `assert(cond)` and `assert.equal(...)`).

class AssertionError extends Error {
  constructor(message) {
    super(message || "Assertion failed");
    this.name = "AssertionError";
    this.code = "ERR_ASSERTION";
  }
}

function assert(value, message) {
  if (!value) throw new AssertionError(message || "The expression evaluated to a falsy value");
}

function deepEqualish(a, b) {
  if (a === b) return true;
  if (typeof a !== "object" || typeof b !== "object" || a == null || b == null) return a == b;
  const ka = Object.keys(a), kb = Object.keys(b);
  if (ka.length !== kb.length) return false;
  return ka.every((k) => deepEqualish(a[k], b[k]));
}

assert.ok = assert;
assert.AssertionError = AssertionError;
assert.equal = (a, b, m) => { if (a != b) throw new AssertionError(m || a + " == " + b); };
assert.notEqual = (a, b, m) => { if (a == b) throw new AssertionError(m || a + " != " + b); };
assert.strictEqual = (a, b, m) => { if (a !== b) throw new AssertionError(m || a + " === " + b); };
assert.notStrictEqual = (a, b, m) => { if (a === b) throw new AssertionError(m || a + " !== " + b); };
assert.deepEqual = (a, b, m) => { if (!deepEqualish(a, b)) throw new AssertionError(m || "deepEqual"); };
assert.deepStrictEqual = (a, b, m) => { if (!deepEqualish(a, b)) throw new AssertionError(m || "deepStrictEqual"); };
assert.notDeepEqual = (a, b, m) => { if (deepEqualish(a, b)) throw new AssertionError(m || "notDeepEqual"); };
assert.fail = (m) => { throw new AssertionError(m || "Failed"); };
assert.ifError = (err) => { if (err) throw err; };
assert.throws = (fn, m) => {
  try { fn(); } catch (e) { return; }
  throw new AssertionError(m || "Missing expected exception");
};
assert.doesNotThrow = (fn) => { fn(); };
assert.strict = assert;
assert.default = assert;

module.exports = assert;
