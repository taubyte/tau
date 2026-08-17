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
import { routeKey, type Placed } from "./layout";

export type Selection = { id: string; mode: "blast" | "reach" } | null;

interface ResourceData extends Record<string, unknown> {
  kind: string;
  icon: string;
  name: string;
  scope: string;
  category: string;
  faded: boolean;
  picked: boolean;
}

// A resource. Kind above name, bordered by category — the same reading the SVG had,
// because that part was never the problem.
function ResourceNode({ data }: NodeProps) {
  const d = data as ResourceData;
  return (
    <div className={`rf-node ${d.category}${d.faded ? " faded" : ""}${d.picked ? " picked" : ""}`}>
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

const NODE_TYPES = { resource: ResourceNode, scope: ScopeNode };

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
  const routed = typeof data?.path === "string" ? (data.path as string) : "";
  const [fallback, midX, midY] = getBezierPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition: Position.Right,
    targetPosition: Position.Left,
  });
  return (
    <>
      <BaseEdge id={id} path={routed || fallback} markerEnd={markerEnd} style={style} />
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
  const lit = useMemo(() => {
    if (!selection) return null;
    const reached =
      selection.mode === "blast"
        ? blastRadius(selection.id, graph.arrows)
        : reachesFrom(selection.id, graph.arrows);
    reached.add(selection.id);
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
        } satisfies ResourceData,
      });
    }
    return out;
  }, [graph, placed, lit, selection]);

  const edges: Edge[] = useMemo(
    () =>
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
          data: { path: placed.routes.get(routeKey(a)) ?? "" },
          label: touched ? a.verb : undefined,
          className: `rf-edge ${cls}${faded ? " faded" : ""}`,
          // An arrowhead, which the first cut of this forgot: direction is the entire
          // claim an edge makes here — who acts on whom — and a line without one says
          // only that two things are related.
          markerEnd: {
            type: MarkerType.ArrowClosed,
            width: 14,
            height: 14,
            color: EDGE_COLOR[cls],
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
    [graph, placed, lit, selection],
  );

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
