// Layout, by ELK.
//
// This replaces a hand-rolled Sugiyama — cycle-break, longest-path layering,
// barycentre ordering, scope-banded placement — that was ~150 lines and still drew
// crossings a person had to trace with a finger. Layered graph drawing is a solved
// problem with a good implementation, and none of what it solves is what this tool is
// about.
//
// Two things made ELK the pick over dagre or d3-dag:
//
//   1. COMPOUND NODES. A namespace holds namespaces, and the layout has to place
//      shop/eu's resources inside shop's box, not merely draw a rectangle around them
//      afterwards. ELK lays out a hierarchy natively and hands back the container's
//      own frame, so the boxes stop being a computed extent that could disagree with
//      the compiler and become the thing the layout produced.
//   2. EDGE ROUTING. Bend points come back per edge, routed around nodes rather than
//      through them, which is the actual fix for the spaghetti.
//
// It stays layout-only: positions in, our own SVG out. Styling, selection, blast
// radius and every fact on screen are unchanged.

import ELK, { type ElkNode, type ElkExtendedEdge } from "elkjs/lib/elk.bundled.js";
import { NODE_H, NODE_W, type Arrow, type Node } from "./graph";

const elk = new ELK();

export interface Placed {
  nodes: Node[];
  /** A namespace's frame, produced by the layout rather than measured after it. */
  scopes: { scope: string; x: number; y: number; w: number; h: number }[];
  /** Absolute polyline per arrow, keyed by `${from}->${to}->${verb}`. */
  routes: Map<string, { x: number; y: number }[]>;
  w: number;
  h: number;
}

const SCOPE_ID = (scope: string) => `ns:${scope}`;
export const routeKey = (a: Arrow) => `${a.from}->${a.to}->${a.verb}`;

// Namespaces nest, so the ELK tree does too: every scope becomes a container holding
// its own resources and its child scopes.
function tree(nodes: Node[]): ElkNode[] {
  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();

  const containers = new Map<string, ElkNode>();
  for (const scope of scopes) {
    containers.set(scope, {
      id: SCOPE_ID(scope),
      children: [],
      layoutOptions: {
        // room for the namespace label, which is drawn inside the frame
        "elk.padding": "[top=30,left=14,bottom=14,right=14]",
      },
    });
  }

  const parentOf = (scope: string): ElkNode | null => {
    if (!scope) return null;
    const cut = scope.lastIndexOf("/");
    return cut < 0 ? null : containers.get(scope.slice(0, cut)) ?? null;
  };

  const roots: ElkNode[] = [];
  for (const scope of scopes) {
    const box = containers.get(scope)!;
    const parent = parentOf(scope);
    if (parent) parent.children!.push(box);
    else roots.push(box);
  }

  for (const n of nodes) {
    const leaf: ElkNode = { id: n.id, width: NODE_W, height: NODE_H };
    if (!n.scope) roots.push(leaf);
    else containers.get(n.scope)!.children!.push(leaf);
  }
  return roots;
}

export async function place(nodes: Node[], arrows: Arrow[]): Promise<Placed> {
  const edges: ElkExtendedEdge[] = arrows.map((a, i) => ({
    id: `e${i}`,
    sources: [a.from],
    targets: [a.to],
  }));

  const laid: ElkNode = await elk.layout({
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": "RIGHT",
      // an edge between two namespaces is the whole point of drawing namespaces;
      // without this ELK refuses to route across a container boundary
      "elk.hierarchyHandling": "INCLUDE_CHILDREN",
      "elk.layered.spacing.nodeNodeBetweenLayers": "64",
      "elk.spacing.nodeNode": "22",
      "elk.layered.spacing.edgeNodeBetweenLayers": "24",
      // the crossing minimiser is what this whole change is for
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.nodePlacement.strategy": "BRANDES_KOEPF",
      "elk.edgeRouting": "SPLINES",
    },
    children: tree(nodes),
    edges,
  });

  // ELK reports each child relative to its parent; everything downstream wants
  // absolute, so the offsets are accumulated on the way down.
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const scopes: Placed["scopes"] = [];

  const walk = (parent: ElkNode, dx: number, dy: number) => {
    for (const c of parent.children ?? []) {
      const x = dx + (c.x ?? 0);
      const y = dy + (c.y ?? 0);
      const node = byId.get(c.id);
      if (node) {
        node.x = x;
        node.y = y;
        continue;
      }
      if (c.id.startsWith("ns:")) {
        scopes.push({
          scope: c.id.slice(3),
          x,
          y,
          w: c.width ?? 0,
          h: c.height ?? 0,
        });
        walk(c, x, y);
      }
    }
  };
  walk(laid, 0, 0);

  // Edge sections are relative to the edge's own container, which for a hierarchical
  // edge is the nearest common ancestor — so they carry the same offset walk.
  const routes = new Map<string, { x: number; y: number }[]>();
  const offsetOf = new Map<string, { x: number; y: number }>([["root", { x: 0, y: 0 }]]);
  const noteOffsets = (parent: ElkNode, dx: number, dy: number) => {
    offsetOf.set(parent.id, { x: dx, y: dy });
    for (const c of parent.children ?? []) {
      if (!c.id.startsWith("ns:")) continue;
      noteOffsets(c, dx + (c.x ?? 0), dy + (c.y ?? 0));
    }
  };
  noteOffsets(laid, 0, 0);

  const collectEdges = (parent: ElkNode) => {
    const off = offsetOf.get(parent.id) ?? { x: 0, y: 0 };
    for (const e of (parent.edges ?? []) as ElkExtendedEdge[]) {
      const i = Number(e.id.slice(1));
      const arrow = arrows[i];
      if (!arrow) continue;
      const section = e.sections?.[0];
      if (!section) continue;
      const pts = [section.startPoint, ...(section.bendPoints ?? []), section.endPoint];
      routes.set(
        routeKey(arrow),
        pts.map((p) => ({ x: p.x + off.x, y: p.y + off.y })),
      );
    }
    for (const c of parent.children ?? []) if (c.id.startsWith("ns:")) collectEdges(c);
  };
  collectEdges(laid);

  return {
    nodes,
    scopes,
    routes,
    w: Math.max(laid.width ?? 0, 400) + 24,
    h: Math.max(laid.height ?? 0, 200) + 24,
  };
}
