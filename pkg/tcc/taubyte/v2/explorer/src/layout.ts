// Layout, by Graphviz (dot), compiled to WebAssembly.
//
// Third layout this file has had, and each failed differently enough to be worth
// recording — the failures are the argument for this one.
//
//   A hand-rolled Sugiyama — cycle-break, layering, barycentre ordering, scope bands.
//   ~150 lines, and it still drew crossings you had to trace with a finger.
//
//   ELK with INCLUDE_CHILDREN. Routes everything, including edges crossing a namespace
//   boundary, but it flattens the hierarchy into one global layering: a namespace's
//   members end up smeared across ten layers with its box stretched over all of them.
//   2123x570, mostly whitespace.
//
//   ELK with SEPARATE_CHILDREN. Compact — 1104x815 — because each namespace is laid
//   out as its own cluster. But it declines to route the edges that cross a container
//   boundary, 7 of 30 here, and a straight line between two boxes reads as a wire laid
//   over the picture rather than as a connection.
//
// dot does BOTH, which is the whole reason it exists: `subgraph cluster_*` keeps a
// namespace together, and the spline router avoids the boxes and clusters a line
// passes, including for lines that leave one. It has been the reference implementation
// for a clustered directed graph for thirty years.
//
// Still layout-only: geometry in, our own SVG out. Styling, selection, blast radius
// and every fact on screen are unchanged.

import { Graphviz } from "@hpcc-js/wasm-graphviz";
import { NODE_H, NODE_W, type Arrow, type Node } from "./graph";

export interface Placed {
  nodes: Node[];
  /** A namespace's frame, from dot's cluster bounding box. */
  scopes: { scope: string; x: number; y: number; w: number; h: number }[];
  /** A ready-made SVG path per arrow — dot's spline, not a curve we invented. */
  routes: Map<string, string>;
  w: number;
  h: number;
}

export const routeKey = (a: Arrow) => `${a.from}->${a.to}->${a.verb}`;

let engine: Promise<Graphviz> | null = null;
const graphviz = () => (engine ??= Graphviz.load());

const PT = 72; // dot speaks inches for size and points for position
const q = (s: string) => `"${s.replace(/"/g, '\\"')}"`;
const cluster = (scope: string) => `cluster_${scope.replace(/[^A-Za-z0-9]/g, "_")}`;

// Namespaces nest, so the subgraphs do. The label is declared so dot reserves room at
// the top of the box for it; the text itself is drawn by render.ts, into that space.
function dotFor(nodes: Node[], arrows: Arrow[]): string {
  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();
  const childrenOf = (scope: string) =>
    scopes.filter((s) => s.startsWith(scope + "/") && !s.slice(scope.length + 1).includes("/"));

  const emit = (scope: string, depth: number): string => {
    const pad = "  ".repeat(depth);
    const body = [
      `${pad}  label=${q(`namespace · ${scope}`)};`,
      ...nodes.filter((n) => n.scope === scope).map((n) => `${pad}  ${q(n.id)};`),
      ...childrenOf(scope).map((c) => emit(c, depth + 1)),
    ].join("\n");
    return `${pad}subgraph ${cluster(scope)} {\n${body}\n${pad}}`;
  };

  return [
    "digraph {",
    "  rankdir=LR;",
    "  compound=true;",
    // ORTHO, not spline: right-angle edges read better against rectangular boxes, and
    // dot routes them around obstacles either way — which is the part React Flow's own
    // orthogonal edge type does not do.
    "  splines=ortho;",
    "  nodesep=0.42;",
    "  ranksep=0.95;",
    "  fontsize=11;",
    "  labelloc=t;",
    `  node [shape=box, fixedsize=true, width=${NODE_W / PT}, height=${NODE_H / PT}, label=""];`,
    ...nodes.filter((n) => !n.scope).map((n) => `  ${q(n.id)};`),
    ...scopes.filter((s) => !s.includes("/")).map((s) => emit(s, 1)),
    ...arrows.map((a) => `  ${q(a.from)} -> ${q(a.to)};`),
    "}",
  ].join("\n");
}

interface DotObject {
  name: string;
  pos?: string;
  bb?: string;
}
interface DotEdge {
  tail: number;
  head: number;
  pos?: string;
}

// dot's origin is bottom-left and SVG's is top-left, so every y is mirrored through
// the drawing's height. Doing it once, here, keeps the rest of the app in one system.
const flip = (h: number, y: number) => h - y;

// An edge's `pos` is "e,x,y p0 p1 p2 p3 …": an optional arrow tip, a start point, then
// cubic bezier triples. Emitting them AS beziers keeps dot's routing exactly —
// sampling a spline into a polyline and re-curving it, which the ELK path had to do,
// is where a routed line quietly stops avoiding what it was routed around.
function pathFrom(pos: string, h: number): string {
  let tip: { x: number; y: number } | null = null;
  const pts: { x: number; y: number }[] = [];

  for (const part of pos.trim().split(/\s+/)) {
    const lead = part.slice(0, 2);
    const body = lead === "e," || lead === "s," ? part.slice(2) : part;
    const [x, y] = body.split(",").map(Number);
    if (!Number.isFinite(x) || !Number.isFinite(y)) continue;
    if (lead === "e,") tip = { x, y: flip(h, y) };
    else if (lead !== "s,") pts.push({ x, y: flip(h, y) });
  }
  if (!pts.length) return "";

  let d = `M${pts[0].x},${pts[0].y}`;
  for (let i = 1; i + 2 < pts.length; i += 3) {
    d += ` C${pts[i].x},${pts[i].y} ${pts[i + 1].x},${pts[i + 1].y} ${pts[i + 2].x},${pts[i + 2].y}`;
  }
  if (tip) d += ` L${tip.x},${tip.y}`;
  return d;
}

export async function place(nodes: Node[], arrows: Arrow[]): Promise<Placed> {
  const gv = await graphviz();
  const out = JSON.parse(gv.layout(dotFor(nodes, arrows), "json", "dot")) as {
    bb: string;
    objects?: DotObject[];
    edges?: DotEdge[];
  };

  const [, , W, H] = out.bb.split(",").map(Number);
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const scopeByCluster = new Map(
    [...new Set(nodes.map((n) => n.scope))].filter(Boolean).map((s) => [cluster(s), s] as const),
  );

  const objects = out.objects ?? [];
  const scopes: Placed["scopes"] = [];
  for (const o of objects) {
    if (o.bb) {
      const scope = scopeByCluster.get(o.name);
      if (!scope) continue;
      const [x1, y1, x2, y2] = o.bb.split(",").map(Number);
      scopes.push({ scope, x: x1, y: flip(H, y2), w: x2 - x1, h: y2 - y1 });
      continue;
    }
    const node = byId.get(o.name);
    if (!node || !o.pos) continue;
    const [cx, cy] = o.pos.split(",").map(Number);
    node.x = cx - NODE_W / 2;
    node.y = flip(H, cy) - NODE_H / 2;
  }

  // dot indexes an edge's endpoints into `objects`, so the arrow that produced one is
  // matched by its endpoints. `claimed` keeps two arrows between the same pair from
  // both taking the first spline — which cannot happen while parallel edges are merged
  // upstream, and would be a silent mis-draw if that ever changed.
  const routes = new Map<string, string>();
  const claimed = new Set<number>();
  for (const e of out.edges ?? []) {
    if (!e.pos) continue;
    const from = objects[e.tail]?.name;
    const to = objects[e.head]?.name;
    if (!from || !to) continue;
    const i = arrows.findIndex((a, idx) => a.from === from && a.to === to && !claimed.has(idx));
    if (i < 0) continue;
    claimed.add(i);
    routes.set(routeKey(arrows[i]), pathFrom(e.pos, H));
  }

  return { nodes, scopes, routes, w: W + 24, h: H + 24 };
}
