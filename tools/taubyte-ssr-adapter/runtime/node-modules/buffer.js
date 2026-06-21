// node:buffer shim. The Node-compat layer (node.js) installs a minimal Buffer on
// globalThis; this module re-exports it so `import { Buffer } from "node:buffer"`
// (used by next-on-pages' worker to hoist Buffer onto globalThis) resolves.
export const Buffer = globalThis.Buffer;
export const constants = { MAX_LENGTH: 0x7fffffff, MAX_STRING_LENGTH: 0x1fffffe8 };
export default { Buffer: globalThis.Buffer, constants };
