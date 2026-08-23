// The JSON tree. Presentation only — every fact on screen comes from the compiled
// object. The graph itself moved to cy.ts, which is a graph library rather than a
// coordinate generator plus a thousand lines of SVG.

export function jsonTree(value: unknown, label: string, openDepth = 2, depth = 0): HTMLElement {
  if (value === null || typeof value !== "object") {
    const leaf = document.createElement("div");
    leaf.className = "row leaf";
    leaf.innerHTML =
      `<span class="k">${esc(label)}</span><span class="p">:</span> ` +
      `<span class="${scalarClass(value)}">${esc(fmtScalar(value, label))}</span>`;
    return leaf;
  }

  const entries = Array.isArray(value)
    ? (value as unknown[]).map((v, i) => [String(i), v] as const)
    : Object.entries(value as Record<string, unknown>).sort(([a], [b]) => a.localeCompare(b));

  const det = document.createElement("details");
  det.className = "row";
  if (depth < openDepth) det.open = true;

  const sum = document.createElement("summary");
  const count = Array.isArray(value) ? `[${entries.length}]` : `{${entries.length}}`;
  sum.innerHTML = `<span class="k">${esc(label)}</span> <span class="c">${count}</span>`;
  det.appendChild(sum);

  const kids = document.createElement("div");
  kids.className = "kids";
  for (const [k, v] of entries) kids.appendChild(jsonTree(v, k, openDepth, depth + 1));
  det.appendChild(kids);
  return det;
}

// jsonRoot wraps a tree so the top level carries no rail of its own.
export function jsonRoot(value: unknown, label: string, openDepth = 2): HTMLElement {
  const wrap = document.createElement("div");
  wrap.className = "jt";
  wrap.appendChild(jsonTree(value, label, openDepth));
  return wrap;
}

function scalarClass(v: unknown) {
  if (typeof v === "number") return "n";
  if (typeof v === "boolean") return "b";
  if (v === null) return "z";
  return "s";
}

// Durations and sizes compile to raw integers. Showing the human form beside the
// number is the difference between a page you can read and a wall of digits. WHICH
// field is which is not this file's to guess — Duration()/Bytes() already tag it
// on the schema (x-scalar) — so a field is read once, off tcc's own schema(), not
// inferred from how big a number happens to be, which would as happily call a
// price or a count a duration.
let scalarsByField: Map<string, string> | null = null;

function scalarOf(field: string): string | undefined {
  if (!scalarsByField && window.tccV2) {
    scalarsByField = new Map();
    collectScalars(window.tccV2.schema(), scalarsByField);
  }
  return scalarsByField?.get(field);
}

// Walks the whole JSON Schema tree looking for `properties` maps, wherever they
// sit ($defs, oneOf branches, array items) — schema-shape-agnostic, so a new
// section or nesting level needs no change here.
function collectScalars(node: unknown, out: Map<string, string>): void {
  if (!node || typeof node !== "object") return;
  if (Array.isArray(node)) {
    for (const v of node) collectScalars(v, out);
    return;
  }
  const obj = node as Record<string, unknown>;
  const props = obj.properties;
  if (props && typeof props === "object") {
    for (const [key, sub] of Object.entries(props as Record<string, unknown>)) {
      const scalar = (sub as Record<string, unknown> | null)?.["x-scalar"];
      if (typeof scalar === "string") out.set(key, scalar);
    }
  }
  for (const v of Object.values(obj)) collectScalars(v, out);
}

function fmtScalar(v: unknown, field: string): string {
  if (typeof v === "number" && Number.isInteger(v)) {
    const scalar = scalarOf(field);
    if (scalar === "duration") {
      const human = humanDuration(v);
      return human ? `${v}  (${human})` : String(v);
    }
    if (scalar === "bytes") return `${v}  (${humanBytes(v)})`;
  }
  return typeof v === "string" ? `"${v}"` : String(v);
}

function humanDuration(ns: number): string | null {
  const s = ns / 1e9;
  if (s < 1 || s > 86400 * 400) return null;
  if (s < 60) return `${trim(s)}s`;
  if (s < 3600) return `${trim(s / 60)}m`;
  if (s < 86400) return `${trim(s / 3600)}h`;
  return `${trim(s / 86400)}d`;
}
const trim = (n: number) => String(Math.round(n * 100) / 100);
const humanBytes = (b: number) =>
  b >= 1e9 ? `${trim(b / 1e9)}GB` : b >= 1e6 ? `${trim(b / 1e6)}MB` : `${trim(b / 1e3)}kB`;

export function esc(s: string): string {
  return s.replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!);
}
