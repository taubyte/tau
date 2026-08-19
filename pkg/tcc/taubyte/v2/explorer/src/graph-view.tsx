// The graph, in React Flow — the same library the web console draws its build graph
// with (`web-console.v2/src/components/editor/BuildGraphPanel.tsx`).
//
// This is the fourth attempt and the first that is not a picture. The three before it
// were layout libraries with hand-written SVG around them: a hand-rolled Sugiyama,
// then ELK, then Graphviz. Each produced a static drawing that had to FIT, so the
// ceiling was "how much can be shown at once", and every round of tuning traded
// legibility against how much fell off the edge.
//
// React Flow owns the viewport instead — pan, zoom, fit, selection, hit-testing — so
// the drawing no longer has to fit, and it is the framework the rest of the stack
// already uses, which matters more than any of my opinions about layout quality.
//
// Positions still come from dot (layout.ts). React Flow deliberately ships no layout;
// its own docs tell you to bring one, and dot is the one that already got clusters and
// routing right here.

import { useMemo } from "react";
import {
  Background,
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  getBezierPath,
  type Edge,
  type EdgeProps,
  type Node as FlowNode,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { blastRadius, categoryOf, iconOf, reachesFrom, NODE_H, NODE_W, type Graph } from "./graph";
import { KindIcon } from "./icons";
import { IconWorld, IconWorldWww } from "@tabler/icons-react";
import { CLOUD_ZONE, INTERNET, hopKey, routeKey, type Placed } from "./layout";

export type Selection = { id: string; mode: "blast" | "reach" } | null;

interface ResourceData extends Record<string, unknown> {
  kind: string;
  icon: string;
  name: string;
  scope: string;
  category: string;
  faded: boolean;
  picked: boolean;
  /** Nothing authored this: it is what a transpiled resource becomes. */
  derived: boolean;
}

// A resource. Kind above name, bordered by category — the same reading the SVG had,
// because that part was never the problem.
function ResourceNode({ data }: NodeProps) {
  const d = data as ResourceData;
  return (
    <div
      className={`rf-node ${d.category}${d.faded ? " faded" : ""}${d.picked ? " picked" : ""}${d.derived ? " derived" : ""}`}
      title={d.derived ? "not authored — what a workflow becomes" : undefined}
    >
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <span className="rf-glyph">
        <KindIcon icon={d.icon} size={15} />
      </span>
      <span className="rf-text">
        <span className="rf-kind">
          {d.kind}
          {d.scope ? ` · ${d.scope}` : ""}
        </span>
        <span className="rf-name">{d.name}</span>
      </span>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
}

// A namespace. React Flow gives grouping for free through parentId, so a namespace is
// a node that CONTAINS nodes rather than a rectangle drawn around a set of positions —
// it cannot disagree with the compiler about what is inside it.
function ScopeNode({ data }: NodeProps) {
  const d = data as { label: string; faded: boolean };
  return (
    <div className={`rf-scope${d.faded ? " faded" : ""}`}>
      <span className="rf-scopelabel">namespace · {d.label}</span>
    </div>
  );
}

// The internet. Not a resource and never part of the model — it is drawn so that
// ingress arrives from somewhere visible instead of from the edge of the canvas.
function InternetNode() {
  return (
    <div className="rf-internet">
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <IconWorld size={16} stroke={1.7} />
      <span>the internet</span>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
}

// The cloud's own zone: what answers a generated name. Not a resource in any project,
// so like the internet it is drawn rather than compiled — but it IS in the path, and a
// diagram that skips it makes a generated name look like it resolves by magic.
function CloudZoneNode() {
  return (
    <div className="rf-internet rf-cloudzone">
      <Handle type="target" position={Position.Left} isConnectable={false} />
      <IconWorldWww size={16} stroke={1.7} />
      <span>the cloud's zone</span>
      <Handle type="source" position={Position.Right} isConnectable={false} />
    </div>
  );
}

const NODE_TYPES = { resource: ResourceNode, scope: ScopeNode, internet: InternetNode, cloudzone: CloudZoneNode };

// The edge dot routed.
//
// React Flow's built-in edge types draw between two handles and know nothing about
// what is in the way, so `smoothstep` ran lines straight through boxes — while
// layout.ts had already computed a spline that avoids them and was throwing it away.
// This renders that spline.
//
// The fallback is a plain bezier for an edge dot returned no route for, which should
// not happen: dot routes every edge, including the ones leaving a cluster, and that is
// most of why it is the layout here.
function RoutedEdge({ id, data, markerEnd, style, label, sourceX, sourceY, targetX, targetY }: EdgeProps) {
  const route = data?.route as { d: string; mid: { x: number; y: number } } | undefined;
  const [fallback, bezierX, bezierY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
  });
  const midX = route ? route.mid.x : bezierX;
  const midY = route ? route.mid.y : bezierY;
  return (
    <>
      <BaseEdge id={id} path={route?.d || fallback} markerEnd={markerEnd} style={style} />
      {label ? (
        <EdgeLabelRenderer>
          <div
            className="rf-edgelabel"
            style={{ transform: `translate(-50%, -50%) translate(${midX}px, ${midY}px)` }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  );
}

const EDGE_TYPES = { routed: RoutedEdge };

// Resolved, not `var(--x)`. React Flow builds a marker's id out of its config and
// references it as url(#id) — a CSS var carries parentheses, which terminate that
// reference early, and every arrowhead on the canvas silently fails to resolve. The
// stroke can stay a var because it is a plain CSS property; the marker cannot.
const cssVar = (name: string) =>
  getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "#888";

const CATEGORY_COLOR: Record<string, string> = {
  internet: "var(--internet)",
  trigger: "var(--trigger)",
  compute: "var(--compute)",
  data: "var(--data)",
  code: "var(--code-cat)",
};

const EDGE_COLOR: Record<string, string> = {
  read: "var(--read)",
  write: "var(--write)",
  ref: "var(--mut)",
  policy: "var(--acc)",
};

export function GraphView({
  graph,
  placed,
  selection,
  onPick,
}: {
  graph: Graph;
  placed: Placed;
  selection: Selection;
  onPick: (id: string | null) => void;
}) {
  // Read once per render rather than per edge; a theme flip re-renders the page.
  const ink = useMemo(
    () => ({
      read: cssVar("--read"),
      write: cssVar("--write"),
      ref: cssVar("--mut"),
      policy: cssVar("--acc"),
      internet: cssVar("--internet"),
    }),
    [],
  );

  const lit = useMemo(() => {
    if (!selection) return null;
    const reached =
      selection.mode === "blast"
        ? blastRadius(selection.id, graph.arrows)
        : reachesFrom(selection.id, graph.arrows);
    reached.add(selection.id);

    // The radius follows USES only — a build-time reference grants no runtime access,
    // which is why a zone is not in a gateway's blast radius. But it IS something the
    // gateway declares, and hiding it makes the resource that answers the name look
    // unrelated to the name. So the selection's immediate neighbours are always lit,
    // references included; only what lies BEYOND them follows uses.
    for (const a of graph.arrows) {
      if (a.from === selection.id) reached.add(a.to);
      if (a.to === selection.id) reached.add(a.from);
    }

    // The ingress path is drawn, not compiled, so it is not in arrows — and a gateway
    // selected without the zone that answers for it is missing the step that makes its
    // name work. Both directions: pick the zone, see what it answers for.
    for (const n of graph.nodes) {
      if (!n.answeredById) continue;
      if (n.id === selection.id) reached.add(n.answeredById);
      if (n.answeredById === selection.id) reached.add(n.id);
    }
    return reached;
  }, [selection, graph]);

  const nodes: FlowNode[] = useMemo(() => {
    const out: FlowNode[] = [];
    const frame = new Map(placed.scopes.map((s) => [s.scope, s]));

    // Parents first: React Flow requires a parent to appear before its children, and
    // a child's position is relative to it — which is also what makes dragging a
    // namespace move everything in it.
    const byDepth = [...placed.scopes].sort(
      (a, b) => a.scope.split("/").length - b.scope.split("/").length,
    );
    for (const s of byDepth) {
      const cut = s.scope.lastIndexOf("/");
      const parent = cut < 0 ? null : frame.get(s.scope.slice(0, cut));
      out.push({
        id: `ns:${s.scope}`,
        type: "scope",
        parentId: parent ? `ns:${s.scope.slice(0, cut)}` : undefined,
        position: parent ? { x: s.x - parent.x, y: s.y - parent.y } : { x: s.x, y: s.y },
        style: { width: s.w, height: s.h },
        selectable: false,
        draggable: false,
        data: { label: s.scope, faded: false },
      });
    }

    if (placed.cloudZone) {
      out.push({
        id: CLOUD_ZONE,
        type: "cloudzone",
        position: { x: placed.cloudZone.x, y: placed.cloudZone.y },
        width: placed.cloudZone.w,
        height: placed.cloudZone.h,
        draggable: false,
        selectable: false,
        data: {},
      });
    }

    if (placed.internet) {
      out.push({
        id: INTERNET,
        type: "internet",
        position: { x: placed.internet.x, y: placed.internet.y },
        width: placed.internet.w,
        height: placed.internet.h,
        draggable: false,
        selectable: false,
        data: {},
      });
    }

    for (const n of graph.nodes) {
      const parent = n.scope ? frame.get(n.scope) : undefined;
      out.push({
        id: n.id,
        type: "resource",
        parentId: parent ? `ns:${n.scope}` : undefined,
        position: parent ? { x: n.x - parent.x, y: n.y - parent.y } : { x: n.x, y: n.y },
        // Declared, not measured: the minimap draws from node dimensions, and a
        // custom node React has not laid out yet has none — which is why the minimap
        // came up blank the first time.
        width: NODE_W,
        height: NODE_H,
        draggable: false,
        data: {
          kind: n.kind,
          icon: iconOf(n.kind),
          name: n.name,
          scope: n.scope,
          category: categoryOf(n.kind),
          faded: lit !== null && !lit.has(n.id),
          picked: selection?.id === n.id,
          derived: !!n.derived,
        } satisfies ResourceData,
      });
    }
    return out;
  }, [graph, placed, lit, selection, ink]);

  const edges: Edge[] = useMemo(() => {
    // The path in: the internet asks DNS, the zone answers with a name, the request
    // reaches what that name points at. Two hops, because that is two different things
    // happening — and drawing one arrow straight to the gateway made the zone look
    // like something off to the side rather than the step that makes the name work.
    const ingressEdge = (from: string, to: string, label?: string): Edge | null => {
      const route = placed.routes.get(hopKey(from, to));
      if (!route) return null;
      const fits = label ? route.len > label.length * 5.5 : false;
      return {
        id: hopKey(from, to),
        source: from,
        target: to,
        type: "routed",
        data: { route },
        label: fits ? label : undefined,
        className: "rf-edge exposed",
        markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: ink.internet },
        style: { stroke: "var(--internet)", strokeWidth: 1.6, opacity: lit ? 0.15 : 1 },
      };
    };

    const out: Edge[] = [];
    const zones = new Set<string>();
    for (const n of graph.nodes) {
      if (!n.fqdn || n.kind === "zone") continue;
      const zone = n.answeredById || CLOUD_ZONE;
      zones.add(zone);
      const hop = ingressEdge(zone, n.id, n.fqdn);
      if (hop) out.push(hop);
    }
    for (const zone of zones) {
      const hop = ingressEdge(INTERNET, zone);
      if (hop) out.push(hop);
    }

    return out.concat(
      graph.arrows.map((a) => {
        const cls = a.policy ? "policy" : a.ref ? "ref" : a.effect;
        const faded = lit !== null && (!lit.has(a.from) || !lit.has(a.to));
        // The verb is written only on the edges of whatever is selected. Labelling
        // all thirty at once buries the boxes under text that is unreadable at any
        // zoom where the graph is legible — which is what a tooltip was doing in the
        // SVG version, less usefully.
        const touched = selection !== null && (a.from === selection.id || a.to === selection.id);
        return {
          id: `${a.from}->${a.to}->${a.verb}`,
          source: a.from,
          target: a.to,
          type: "routed",
          data: { route: placed.routes.get(routeKey(a)) },
          label: touched ? a.verb : undefined,
          className: `rf-edge ${cls}${faded ? " faded" : ""}`,
          // An arrowhead, which the first cut of this forgot: direction is the entire
          // claim an edge makes here — who acts on whom — and a line without one says
          // only that two things are related.
          markerEnd: {
            type: MarkerType.ArrowClosed,
            width: 14,
            height: 14,
            color: ink[cls as keyof typeof ink],
          },
          style: {
            stroke: EDGE_COLOR[cls],
            strokeWidth: touched ? 2 : 1.4,
            strokeDasharray: a.ref ? "4 4" : a.policy ? "2 4" : undefined,
            opacity: faded ? 0.1 : 1,
          },
          zIndex: touched ? 10 : 0,
        } satisfies Edge;
      }),
    );
  }, [graph, placed, lit, selection, ink]);

  return (
    <ReactFlow
      nodes={nodes}
      edges={edges}
      nodeTypes={NODE_TYPES}
      edgeTypes={EDGE_TYPES}
      onNodeClick={(_e, node) => onPick(node.id.startsWith("ns:") ? null : node.id)}
      onPaneClick={() => onPick(null)}
      fitView
      fitViewOptions={{ padding: 0.15, maxZoom: 1 }}
      minZoom={0.15}
      maxZoom={2}
      nodesConnectable={false}
      nodesDraggable={false}
      elementsSelectable
      proOptions={{ hideAttribution: true }}
    >
      <Background gap={16} color="var(--line)" />
      <Controls showInteractive={false} />
      {/* A project outgrows one screen long before it outgrows the model, and the
          minimap is what keeps "where am I" answerable once it has. */}
      <MiniMap
        pannable
        zoomable
        maskColor="var(--code)"
        nodeColor={(n) => (n.type === "scope" ? "transparent" : CATEGORY_COLOR[String(n.data.category)] ?? "var(--mut)")}
        nodeStrokeWidth={2}
        style={{ background: "var(--card)", border: "1px solid var(--line)" }}
      />
    </ReactFlow>
  );
}

export { NODE_H, NODE_W };
