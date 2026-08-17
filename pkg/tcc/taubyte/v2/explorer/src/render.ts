// SVG drawing and the JSON tree. Presentation only — every fact on screen comes
// from the compiled object.

import {
  blastRadius,
  categoryOf,
  NODE_H,
  NODE_W,
  reachesFrom,
  type Arrow,
  type Graph,
  type Node,
} from "./graph";
import { routeKey, type Placed } from "./layout";

const NS = "http://www.w3.org/2000/svg";
const el = <K extends keyof SVGElementTagNameMap>(t: K, attrs: Record<string, string | number> = {}) => {
  const n = document.createElementNS(NS, t);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
  return n;
};

export type Selection = { id: string; mode: "blast" | "reach" } | null;

export function drawGraph(
  g: Graph,
  placed: Placed,
  sel: Selection,
  onPick: (id: string | null) => void,
): SVGSVGElement {
  const { w, h } = placed;
  const svg = el("svg", {
    viewBox: `0 0 ${w} ${h}`,
    // Intrinsic size in CSS pixels; the stylesheet caps width at 100% so a wide
    // graph scales down rather than forcing a horizontal scrollbar.
    width: w,
    height: h,
    preserveAspectRatio: "xMinYMin meet",
    class: "graph",
  });

  const defs = el("defs");
  for (const [id, cls] of [
    ["arrow-read", "read"],
    ["arrow-write", "write"],
    ["arrow-ref", "ref"],
    ["arrow-policy", "policy"],
    ["arrow-dim", "dim"],
  ]) {
    const m = el("marker", {
      id,
      viewBox: "0 0 8 8",
      refX: 7,
      refY: 4,
      markerWidth: 6,
      markerHeight: 6,
      orient: "auto-start-reverse",
    });
    m.appendChild(el("path", { d: "M0,0 L8,4 L0,8 z", class: `mk ${cls}` }));
    defs.appendChild(m);
  }
  svg.appendChild(defs);

  const highlight = pickHighlight(g, sel);

  // Namespaces are drawn as what they are in the DSL: a container group holding a
  // nested copy of every resource group. A resource inside one resolves names in
  // its own scope first and falls back to the project root — so the box is not
  // decoration, it is the boundary the compiler applied.
  // The frames come from the layout, which placed the children inside them. They are
  // no longer an extent measured around a set of nodes and hoped to agree with the
  // compiler's idea of the boundary — shop/eu is inside shop because ELK put it
  // there.
  const scopeLayer = el("g", { class: "scopes" });
  for (const s of placed.scopes) {
    const box = el("g", { class: "scope" });
    box.appendChild(el("rect", { x: s.x, y: s.y, width: s.w, height: s.h, rx: 8, class: "scopebox" }));
    const label = el("text", { x: s.x + 10, y: s.y + 18, class: "scopelabel" });
    label.textContent = `namespace · ${s.scope}`;
    box.appendChild(label);
    scopeLayer.appendChild(box);
  }
  svg.appendChild(scopeLayer);

  const arrowLayer = el("g");
  for (const a of g.arrows) {
    const from = g.byId.get(a.from)!;
    const to = g.byId.get(a.to)!;
    const dim = highlight !== null && !(highlight.has(a.from) && highlight.has(a.to));
    arrowLayer.appendChild(edgePath(from, to, a, dim, placed.routes.get(routeKey(a))));
  }
  svg.appendChild(arrowLayer);

  for (const n of g.nodes) {
    svg.appendChild(nodeBox(n, highlight, sel?.id === n.id, onPick));
  }

  svg.addEventListener("click", (e) => {
    if (e.target === svg) onPick(null);
  });
  return svg;
}

function pickHighlight(g: Graph, sel: Selection): Set<string> | null {
  if (!sel) return null;
  const reached = sel.mode === "blast" ? blastRadius(sel.id, g.arrows) : reachesFrom(sel.id, g.arrows);
  reached.add(sel.id);
  return reached;
}

function edgePath(
  from: Node,
  to: Node,
  a: Arrow,
  dim: boolean,
  route?: string,
): SVGElement {
  const d = route || drawn(from, to);
  const cls = dim ? "dim" : a.policy ? "policy" : a.ref ? "ref" : a.effect;
  const g = el("g", { class: `arrow ${cls}` });
  const p = el("path", {
    d,
    class: `edge ${cls}${a.ref ? " isref" : ""}`,
    "marker-end": `url(#arrow-${dim ? "dim" : a.policy ? "policy" : a.ref ? "ref" : a.effect})`,
  });
  const title = el("title");
  title.textContent = a.policy
    ? `${from.kind}/${from.name} — ${a.verb} — ${to.kind}/${to.name}`
    : a.ref
    ? `${from.kind}/${from.name} → ${to.kind}/${to.name} · ${a.verb} (reference, no verb)`
    : `${from.kind}/${from.name} → ${to.kind}/${to.name} · ${a.verb} (${a.effect})`;
  p.appendChild(title);
  g.appendChild(p);
  return g;
}

// Fallback for an edge dot returns no spline for. It should not happen — dot routes
// every edge including the ones that leave a cluster, which is why it replaced ELK —
// so this exists so a missing route degrades to a readable line instead of nothing.
// Drawn, not routed: it does not know what it passes over.
function drawn(from: Node, to: Node): string {
  const x1 = from.x + NODE_W;
  const y1 = from.y + NODE_H / 2;
  const x2 = to.x;
  const y2 = to.y + NODE_H / 2;
  if (x2 < x1) {
    // backwards: bow above both boxes so it reads as going the other way instead of
    // disappearing behind everything between them
    const lift = Math.min(y1, y2) - 34;
    return `M${x1},${y1} C${x1 + 70},${lift} ${x2 - 70},${lift} ${x2},${y2}`;
  }
  const mid = x1 + (x2 - x1) / 2;
  return `M${x1},${y1} C${mid},${y1} ${mid},${y2} ${x2},${y2}`;
}

function nodeBox(n: Node, highlight: Set<string> | null, selected: boolean, onPick: (id: string) => void): SVGElement {
  const dim = highlight !== null && !highlight.has(n.id);
  const g = el("g", {
    class: `node ${categoryOf(n.kind)}${dim ? " dim" : ""}${selected ? " selected" : ""}`,
    transform: `translate(${n.x},${n.y})`,
  });
  g.appendChild(el("rect", { width: NODE_W, height: NODE_H, rx: 6, class: "box" }));
  const kind = el("text", { x: 10, y: 16, class: "kind" });
  kind.textContent = n.kind + (n.scope ? ` · ${n.scope}` : "");
  const name = el("text", { x: 10, y: 31, class: "name" });
  name.textContent = n.name;
  g.appendChild(kind);
  g.appendChild(name);
  g.addEventListener("click", (e) => {
    e.stopPropagation();
    onPick(n.id);
  });
  return g;
}

// ---- JSON tree -------------------------------------------------------------

// Children go in their own container so BOTH scalars and sub-objects are indented
// by it. Indenting <details> alone (the obvious way) leaves every scalar leaf flush
// with its parent's key, and the whole thing reads as one flat list.
export function jsonTree(value: unknown, label: string, openDepth = 2, depth = 0): HTMLElement {
  if (value === null || typeof value !== "object") {
    const leaf = document.createElement("div");
    leaf.className = "row leaf";
    leaf.innerHTML =
      `<span class="k">${esc(label)}</span><span class="p">:</span> ` +
      `<span class="${scalarClass(value)}">${esc(fmtScalar(value))}</span>`;
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
// number is the difference between a page you can read and a wall of digits.
function fmtScalar(v: unknown): string {
  if (typeof v === "number" && Number.isInteger(v) && v >= 1_000_000) {
    if (v % 1_000_000 === 0 && v <= 1_000_000_000_000) {
      const ns = humanDuration(v);
      if (ns) return `${v}  (${ns})`;
    }
    return `${v}  (${humanBytes(v)})`;
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
