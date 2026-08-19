// The bridge: the explorer is a vanilla TS app and the graph is React. One root,
// created once and reused, so the React tree keeps its state — most importantly the
// viewport — while paint() rebuilds the DOM around it on every keystroke.
import { createElement } from "react";
import { createRoot, type Root } from "react-dom/client";
import { GraphView, type Selection } from "./graph-view";
import type { Graph } from "./graph";
import type { Placed } from "./layout";

const host = document.createElement("div");
host.className = "rfhost";

let root: Root | null = null;

export function graphHost(): HTMLElement {
  return host;
}

export function renderGraph(
  graph: Graph,
  placed: Placed,
  selection: Selection,
  onPick: (id: string | null) => void,
  flippedIds: Set<string> = new Set(),
  focus?: { scope: string; seq: number },
): void {
  root ??= createRoot(host);
  root.render(createElement(GraphView, { graph, placed, selection, onPick, flippedIds, focus }));
}
