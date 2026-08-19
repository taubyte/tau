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
  /**
   * Per arrow: the path dot routed, and the point halfway ALONG it. The midpoint has
   * to come from the route — a label placed between the two endpoints instead sits
   * wherever the straight line would have been, which is exactly the space the router
   * moved the line out of.
   */
  routes: Map<string, Route>;
  /** Where the internet sits, when anything is exposed at all. */
  internet: { x: number; y: number; w: number; h: number } | null;
  /** Where the cloud's own zone sits, when a generated name needs it drawn. */
  cloudZone: { x: number; y: number; w: number; h: number } | null;
  w: number;
  h: number;
}

export interface Route {
  d: string;
  mid: { x: number; y: number };
  /** How long the line is, so a caller can decide whether a word fits on it. */
  len: number;
}

export const routeKey = (a: Arrow) => `${a.from}->${a.to}->${a.verb}`;

// The internet is not a resource, so it is not in the compiled object — but a diagram
// that omits it makes ingress look like it comes from nowhere, and "what is exposed"
// is the first question anyone asks of an architecture. It is laid out as a real node
// so dot keeps the exposed resources next to it and routes the arrows properly, and it
// is excluded from everything that reasons about the model: no blast radius, no
// inventory, no findings.
export const INTERNET = "internet";

// The cloud's own zone. It answers every generated name and it is not a resource in
// any project, so — like the internet — it is drawn rather than compiled.
export const CLOUD_ZONE = "cloud-zone";

// The size of both drawn-not-compiled nodes, in ONE place. dot reserves a box of
// exactly this, React Flow positions a box of exactly this, and the CSS fills it —
// when those disagreed by 10px the arrow into the cloud zone stopped short of a border
// that was not where the router thought it was.
export const INGRESS_W = 150;
export const INGRESS_H = 44;

/** One hop of the path a request takes to get in: internet -> zone -> the resource. */
export interface Ingress {
  zone: string; // a zone resource's id, or CLOUD_ZONE
  to: string;
  name: string; // the resolved name, as far as the config can know it
}

export const hopKey = (from: string, to: string) => `${from}=>${to}`;

let engine: Promise<Graphviz> | null = null;
const graphviz = () => (engine ??= Graphviz.load());

const PT = 72; // dot speaks inches for size and points for position
const q = (s: string) => `"${s.replace(/"/g, '\\"')}"`;
const cluster = (scope: string) => `cluster_${scope.replace(/[^A-Za-z0-9]/g, "_")}`;

// Namespaces nest, so the subgraphs do. The label is declared so dot reserves room at
// the top of the box for it; the text itself is drawn by render.ts, into that space.
function dotFor(nodes: Node[], arrows: Arrow[], ingress: Ingress[], frames: Map<string, string>): string {
  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();
  const childrenOf = (scope: string) =>
    scopes.filter((s) => s.startsWith(scope + "/") && !s.slice(scope.length + 1).includes("/"));

  const emit = (scope: string, depth: number): string => {
    const pad = "  ".repeat(depth);
    const body = [
      `${pad}  label=${q(frames.get(scope) ?? `namespace · ${scope}`)};`,
      // room between a namespace's border and the resources inside it, so an edge
      // crossing the boundary has somewhere to be that is not on top of the dashes
      `${pad}  margin=16;`,
      // An anchor is the box's own resource, drawn as nothing: it is only here so the
      // edges that point at the resource have somewhere inside the box to land.
      ...nodes
        .filter((n) => n.scope === scope)
        .map((n) => (n.anchor ? `${pad}  ${q(n.id)} [width=0.06, height=0.06];` : `${pad}  ${q(n.id)};`)),
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
    // Keep routed lines OFF the boxes. sep pads a node for the router's purposes and
    // esep is the margin a line keeps from that padding; without them ortho is happy
    // to run a wire flush along a border, which reads as the line belonging to the box
    // rather than passing it.
    '  sep="+14,10";',
    '  esep="+7,5";',
    "  fontsize=11;",
    "  labelloc=t;",
    `  node [shape=box, fixedsize=true, width=${NODE_W / PT}, height=${NODE_H / PT}, label=""];`,
    // internet -> zone -> resource, which is the path a request actually takes: DNS
    // answers first, and what it answers with is what the request then reaches.
    ...(ingress.length ? [`  ${q(INTERNET)} [width=${INGRESS_W / PT}, height=${INGRESS_H / PT}];`] : []),
    ...(ingress.some((i) => i.zone === CLOUD_ZONE)
      ? [`  ${q(CLOUD_ZONE)} [width=${INGRESS_W / PT}, height=${INGRESS_H / PT}];`]
      : []),
    ...nodes.filter((n) => !n.scope).map((n) => `  ${q(n.id)};`),
    ...scopes.filter((s) => !s.includes("/")).map((s) => emit(s, 1)),
    ...[...new Set(ingress.map((i) => i.zone))].map((z) => `  ${q(INTERNET)} -> ${q(z)};`),
    ...ingress.map((i) => `  ${q(i.zone)} -> ${q(i.to)};`),
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
// control points. Under splines=ortho every corner is a right angle and the control
// points are colinear with the anchors, so the points ARE the polyline — reading them
// that way loses nothing and, unlike re-curving a sampled spline, cannot bend the line
// into something dot did not route.
//
// Corners are then rounded, which is cosmetic and bounded: the radius is capped at
// half the shorter adjacent segment, so a short leg gets a small arc instead of a
// bulge that leaves the corridor dot reserved.
const CORNER = 9;

function pathFrom(pos: string, h: number): Route | null {
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
  if (tip) pts.push(tip);

  // A repeated point has no direction, and a zero-length final segment leaves the
  // arrowhead with no angle to orient by.
  const line = pts.filter((p, i) => i === 0 || Math.hypot(p.x - pts[i - 1].x, p.y - pts[i - 1].y) > 0.5);
  if (line.length < 2) return null;

  let d = `M${line[0].x},${line[0].y}`;
  for (let i = 1; i < line.length - 1; i++) {
    const [a, p, b] = [line[i - 1], line[i], line[i + 1]];
    const r = Math.min(CORNER, Math.hypot(p.x - a.x, p.y - a.y) / 2, Math.hypot(b.x - p.x, b.y - p.y) / 2);
    if (r < 1) {
      d += ` L${p.x},${p.y}`;
      continue;
    }
    const towards = (from: { x: number; y: number }, to: { x: number; y: number }) => {
      const len = Math.hypot(to.x - from.x, to.y - from.y) || 1;
      return { x: from.x + ((to.x - from.x) / len) * r, y: from.y + ((to.y - from.y) / len) * r };
    };
    const enter = towards(p, a);
    const exit = towards(p, b);
    d += ` L${enter.x.toFixed(2)},${enter.y.toFixed(2)} Q${p.x},${p.y} ${exit.x.toFixed(2)},${exit.y.toFixed(2)}`;
  }
  const end = line[line.length - 1];
  const seg = line.slice(1).map((p, i) => Math.hypot(p.x - line[i].x, p.y - line[i].y));
  return {
    d: `${d} L${end.x},${end.y}`,
    mid: midpointOf(line),
    len: seg.reduce((a, b) => a + b, 0),
  };
}

// Halfway by LENGTH, not the middle vertex: an L-shaped route with one long leg and
// one short one has its middle vertex at the corner, which is the least useful place
// on the line to put a word.
function midpointOf(line: { x: number; y: number }[]): { x: number; y: number } {
  const seg = line.slice(1).map((p, i) => Math.hypot(p.x - line[i].x, p.y - line[i].y));
  const half = seg.reduce((a, b) => a + b, 0) / 2;
  let walked = 0;
  for (let i = 0; i < seg.length; i++) {
    if (walked + seg[i] >= half) {
      const t = seg[i] === 0 ? 0 : (half - walked) / seg[i];
      return {
        x: line[i].x + (line[i + 1].x - line[i].x) * t,
        y: line[i].y + (line[i + 1].y - line[i].y) * t,
      };
    }
    walked += seg[i];
  }
  return line[line.length - 1];
}

export async function place(
  nodes: Node[],
  arrows: Arrow[],
  ingress: Ingress[] = [],
  frames: Map<string, string> = new Map(),
): Promise<Placed> {
  const gv = await graphviz();
  const out = JSON.parse(gv.layout(dotFor(nodes, arrows, ingress, frames), "json", "dot")) as {
    bb: string;
    objects?: DotObject[];
    edges?: DotEdge[];
  };

  const [, , W, H] = out.bb.split(",").map(Number);
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const scopeByCluster = new Map(
    [...new Set(nodes.map((n) => n.scope))].filter(Boolean).map((s) => [cluster(s), s] as const),
  );

  // Which endpoint pairs are ingress hops. Inferring it from the endpoints does not
  // work: internet->zone and cloud-zone->proxy have a synthetic end, but zone->gateway
  // has two real ones and is indistinguishable from an arrow the model produced.
  const hops = new Set<string>();
  for (const i of ingress) {
    hops.add(hopKey(INTERNET, i.zone));
    hops.add(hopKey(i.zone, i.to));
  }

  const objects = out.objects ?? [];
  const scopes: Placed["scopes"] = [];
  let internet: Placed["internet"] = null;
  let cloudZone: Placed["cloudZone"] = null;
  for (const o of objects) {
    if (o.bb) {
      const scope = scopeByCluster.get(o.name);
      if (!scope) continue;
      const [x1, y1, x2, y2] = o.bb.split(",").map(Number);
      scopes.push({ scope, x: x1, y: flip(H, y2), w: x2 - x1, h: y2 - y1 });
      continue;
    }
    if (!o.pos) continue;
    const [cx, cy] = o.pos.split(",").map(Number);
    if (o.name === INTERNET) {
      internet = { x: cx - INGRESS_W / 2, y: flip(H, cy) - INGRESS_H / 2, w: INGRESS_W, h: INGRESS_H };
      continue;
    }
    if (o.name === CLOUD_ZONE) {
      cloudZone = { x: cx - INGRESS_W / 2, y: flip(H, cy) - INGRESS_H / 2, w: INGRESS_W, h: INGRESS_H };
      continue;
    }
    const node = byId.get(o.name);
    if (!node) continue;
    // An anchor has no size to centre: it is a point, and the arrows land on it.
    const w = node.anchor ? 0 : NODE_W;
    const h = node.anchor ? 0 : NODE_H;
    node.x = cx - w / 2;
    node.y = flip(H, cy) - h / 2;
  }

  // dot indexes an edge's endpoints into `objects`, so the arrow that produced one is
  // matched by its endpoints. `claimed` keeps two arrows between the same pair from
  // both taking the first spline — which cannot happen while parallel edges are merged
  // upstream, and would be a silent mis-draw if that ever changed.
  const routes = new Map<string, Route>();
  const claimed = new Set<number>();
  for (const e of out.edges ?? []) {
    if (!e.pos) continue;
    const from = objects[e.tail]?.name;
    const to = objects[e.head]?.name;
    if (!from || !to) continue;
    if (hops.has(hopKey(from, to))) {
      const r = pathFrom(e.pos, H);
      if (r) routes.set(hopKey(from, to), r);
      continue;
    }
    const i = arrows.findIndex((a, idx) => a.from === from && a.to === to && !claimed.has(idx));
    if (i < 0) continue;
    claimed.add(i);
    const r = pathFrom(e.pos, H);
    if (r) routes.set(routeKey(arrows[i]), r);
  }

  return { nodes, scopes, routes, internet, cloudZone, w: W + 24, h: H + 24 };
}
