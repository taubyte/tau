// The graph the compiled config already is.
//
// Nothing here infers anything: a node is a compiled resource, an arrow is a
// `use-<kind>-<verb>` key on it, and the arrow's colour is the verb's effect class.
// That is the whole claim of the model — the diagram is a read of the config, not a
// drawing kept beside it — so this file is deliberately dumb.

import type { Compiled, Effect, KindVerbs } from "./wasm";

export interface Node {
  id: string;
  kind: string;
  name: string;
  scope: string; // "" for the project root, else the namespace name
  // Any resource that compiled an `fqdn` is a name the outside world can reach.
  // Read off the field rather than off a list of kinds, so a new kind that carries
  // a name is answered without editing this file.
  fqdn?: string;
  layer: number;
  x: number;
  y: number;
}
export interface Arrow {
  from: string;
  to: string;
  kind: string;
  verb: string;
  effect: Effect;
  // The target's own authorizer: policy, not access. Drawn apart from both because
  // it runs in the other direction from everything else — it is what decides what
  // the arrows INTO this resource are actually worth.
  policy?: boolean;
  // A plain reference (source, credentials, dead-letter) rather than a use. It is
  // still a dependency — X cannot build or run without Y — but it grants no verb,
  // so it is drawn differently and never counts toward a blast radius.
  ref?: boolean;
}
export interface Graph {
  nodes: Node[];
  arrows: Arrow[];
  byId: Map<string, Node>;
  scopes: string[]; // namespace names, in the order they should be drawn
}

// Which category a kind belongs to is DECLARED on the kind, in the DSL
// (Category(...) in definition.go), and read back through kinds(). This file used
// to keep its own table; that table drifted the moment a kind was added, which is
// exactly the failure the whole "the config is the architecture" claim is about.
let CATEGORY: Record<string, string> = {};

export function setCategories(kinds: { group: string; category: string }[]) {
  CATEGORY = Object.fromEntries(kinds.map((k) => [k.group, k.category || "other"]));
}

export function categoryOf(kind: string): string {
  return CATEGORY[kind] ?? "other";
}

const isScopeContainer = (k: string) => k === "namespaces";

export function buildGraph(compiled: Compiled, verbs: KindVerbs[]): Graph {
  const nodes: Node[] = [];
  const arrows: Arrow[] = [];
  const verbEffect = new Map<string, Effect>();
  for (const kv of verbs) for (const v of kv.verbs) verbEffect.set(`${kv.kind}/${v.name}`, v.effect);

  const root = (compiled.object as Compiled) ?? compiled;
  const pendingHandlers: { from: string; kind: string; name: string; scope: string }[] = [];
  // an access entry names its target; it is matched once every node is known
  const pendingPolicy: { from: string; name: string; scope: string | null }[] = [];
  const pendingAccess: {
    from: string; kind: string; name: string; scope: string | null; verb: string; effect: Effect;
  }[] = [];

  // an access block is a map of target-name -> { ops, namespace? }
  const looksLikeAccess = (v: unknown): v is Record<string, Compiled> =>
    !!v && typeof v === "object" && !Array.isArray(v) &&
    Object.values(v as Record<string, unknown>).every(
      (e) => !!e && typeof e === "object" && !Array.isArray(e) && "ops" in (e as object),
    );

  // A compiled resource may itself hold a nested group — a gateway holds its
  // routes — and a namespace may hold namespaces, at any depth. Both are just
  // maps of maps in the compiled object, so the walk is written once and applies
  // to whatever the schema nests next.
  const looksLikeResources = (v: unknown): v is Record<string, Compiled> =>
    !!v &&
    typeof v === "object" &&
    !Array.isArray(v) &&
    Object.values(v as Record<string, unknown>).every(
      (x) => !!x && typeof x === "object" && !Array.isArray(x) && "name" in (x as object),
    ) &&
    Object.keys(v as object).length > 0;

  // Every compiled key is a PATH now, so the arrow rules read the path rather than a
  // dashed prefix:
  //
  //   <kind>/<verb>            an access declaration; the values are resolved ids
  //   route/handler/{kind,name,namespace}   a gateway's handler, named not resolved
  //   <group>/<id>             a plain reference (source, credentials, dead-letter)
  const kindNames = new Set(Object.keys(CATEGORY));

  const addResource = (group: string, id: string, res: Compiled, scope: string) => {
    nodes.push({
      id,
      kind: group,
      name: String(res.name ?? id.slice(0, 8)),
      scope,
      fqdn: typeof res.fqdn === "string" ? res.fqdn : undefined,
      layer: 0,
      x: 0,
      y: 0,
    });

    for (const [key, val] of Object.entries(res)) {
      // an access block: kind -> { targetName: { namespace?, ops } }. The target is
      // NAMED, not resolved, so it is matched against compiled nodes by (kind,
      // name, namespace) the same way a gateway handler is.
      if (kindNames.has(key) && looksLikeAccess(val)) {
        for (const [target, entry] of Object.entries(val as Record<string, Compiled>)) {
          const ops = Array.isArray(entry.ops) ? (entry.ops as string[]) : [];
          // "/x" is absolute from the project root, "x" is relative to this
          // resource's own namespace, "/" is the root itself, absent means the
          // resource's own scope then its parents.
          const ns = typeof entry.namespace === "string" ? (entry.namespace as string) : null;
          for (const op of ops) {
            pendingAccess.push({
              from: id,
              kind: key,
              name: target,
              scope: ns,
              verb: op,
              effect: verbEffect.get(`${key}/${op}`) ?? "write",
            });
          }
        }
        continue;
      }

      // a plain reference: "<group>/<id>"
      if (typeof val === "string" && val.includes("/")) {
        const cut = val.indexOf("/");
        const [kind, to] = [val.slice(0, cut), val.slice(cut + 1)];
        if (kindNames.has(kind) && to) {
          arrows.push({ from: id, to, kind, verb: key, effect: "read", ref: true });
        }
        continue;
      }

      // a nested group under this resource
      if (looksLikeResources(val)) {
        for (const [childID, child] of Object.entries(val)) {
          addResource(key, childID, child, scope);
          arrows.push({ from: id, to: childID, kind: key, verb: key, effect: "read", ref: true });
        }
      }
    }

    // A reachable resource names the function that decides what callers get. It is
    // the target's declaration, so the arrow starts at the target.
    const az = res["authorizer/name"];
    if (typeof az === "string") {
      const ns = typeof res["authorizer/namespace"] === "string" ? (res["authorizer/namespace"] as string) : null;
      pendingPolicy.push({ from: id, name: az, scope: ns });
    }

    // A gateway's handler names its target by (kind, namespace, name) rather than
    // resolving it to an id — a name in another namespace has no spelling the ref
    // resolver accepts. Resolve it here, against the nodes actually compiled.
    const hk = res["route/handler/kind"];
    const hn = res["route/handler/name"];
    if (typeof hk === "string" && typeof hn === "string") {
      const ns = typeof res["route/handler/namespace"] === "string" ? (res["route/handler/namespace"] as string) : "";
      pendingHandlers.push({ from: id, kind: hk + "s", name: hn, scope: ns.replace(/^\/+/, "") });
    }
  };

  const collect = (scopeObj: Compiled, scope: string) => {
    for (const [group, val] of Object.entries(scopeObj)) {
      if (!val || typeof val !== "object" || Array.isArray(val)) continue;
      const instances = val as Record<string, Compiled>;
      if (isScopeContainer(group)) {
        for (const inst of Object.values(instances)) {
          // nested namespaces read as a path, so two levels down is visible as such
          const name = String(inst.name ?? "?");
          collect(inst, scope ? `${scope}/${name}` : name);
        }
        continue;
      }
      for (const [id, res] of Object.entries(instances)) {
        if (!res || typeof res !== "object") continue;
        addResource(group, id, res, scope);
      }
    }
  };
  collect(root, "");

  // Resolve each access entry the way the config says to: absolute from the root
  // with a leading slash, relative to the resource's own namespace without one, and
  // own-scope-then-parents when it names none at all.
  const scopeOf = (own: string, ns: string | null): string[] => {
    if (ns === null) {
      // no path: own scope, then each parent, then the root
      const out = [own];
      let cur = own;
      while (cur.includes("/")) {
        cur = cur.slice(0, cur.lastIndexOf("/"));
        out.push(cur);
      }
      if (own !== "") out.push("");
      return out;
    }
    if (ns === "/") return [""];
    if (ns.startsWith("/")) return [ns.slice(1)];
    return [own ? `${own}/${ns}` : ns];
  };

  for (const a of pendingAccess) {
    const own = nodes.find((n) => n.id === a.from)?.scope ?? "";
    for (const scope of scopeOf(own, a.scope)) {
      const target = nodes.find((n) => n.kind === a.kind && n.name === a.name && n.scope === scope);
      if (target) {
        arrows.push({ from: a.from, to: target.id, kind: a.kind, verb: a.verb, effect: a.effect });
        break;
      }
    }
  }

  // an authorizer is a function, addressed like any other target
  for (const pol of pendingPolicy) {
    const own = nodes.find((n) => n.id === pol.from)?.scope ?? "";
    for (const scope of scopeOf(own, pol.scope)) {
      const target = nodes.find((n) => n.kind === "functions" && n.name === pol.name && n.scope === scope);
      if (target) {
        arrows.push({
          from: pol.from, to: target.id, kind: "functions",
          verb: "authorized by", effect: "read", policy: true,
        });
        break;
      }
    }
  }

  // handlers resolve by name once every node is known
  for (const h of pendingHandlers) {
    const target = nodes.find((n) => n.kind === h.kind && n.name === h.name && (!h.scope || n.scope === h.scope));
    if (target) arrows.push({ from: h.from, to: target.id, kind: h.kind, verb: "serves", effect: "write" });
  }

  const byId = new Map(nodes.map((n) => [n.id, n]));
  // An arrow to something outside this project compiles to an id we do not hold.
  const live = arrows.filter((a) => byId.has(a.to) && byId.has(a.from));

  layer(nodes, live, byId);
  place(order(nodes, live, byId));
  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();
  return { nodes, arrows: live, byId, scopes };
}

// Layering: a node sits one column right of everything that points at it.
//
// A config graph is not acyclic — a channel invokes a function that publishes back
// to it, which is legal and common — so the back edges are found first and left out
// of the layering. They are still drawn; they just do not get a vote on where a
// column is. Without this, one publish/invoke loop walks every downstream node to
// the right until the picture is unreadable.
function layer(nodes: Node[], arrows: Arrow[], byId: Map<string, Node>) {
  const out = new Map<string, Arrow[]>();
  for (const n of nodes) out.set(n.id, []);
  for (const a of arrows) if (a.from !== a.to) out.get(a.from)!.push(a);

  // DFS: an arrow into a node still on the stack closes a cycle.
  const back = new Set<Arrow>();
  const state = new Map<string, 0 | 1 | 2>(); // unseen | on-stack | done
  const visit = (id: string) => {
    state.set(id, 1);
    for (const a of out.get(id)!) {
      const st = state.get(a.to) ?? 0;
      if (st === 1) back.add(a);
      else if (st === 0) visit(a.to);
    }
    state.set(id, 2);
  };
  for (const n of nodes) if ((state.get(n.id) ?? 0) === 0) visit(n.id);

  const dag = arrows.filter((a) => a.from !== a.to && !back.has(a));

  // Longest path over the DAG, by Kahn order so each node is settled once.
  const dagOut = new Map<string, string[]>();
  const indeg = new Map<string, number>();
  for (const n of nodes) {
    dagOut.set(n.id, []);
    indeg.set(n.id, 0);
  }
  for (const a of dag) {
    dagOut.get(a.from)!.push(a.to);
    indeg.set(a.to, indeg.get(a.to)! + 1);
  }
  const queue = nodes.filter((n) => indeg.get(n.id) === 0).map((n) => n.id);
  while (queue.length) {
    const id = queue.shift()!;
    const here = byId.get(id)!.layer;
    for (const next of dagOut.get(id)!) {
      const to = byId.get(next)!;
      if (to.layer < here + 1) to.layer = here + 1;
      indeg.set(next, indeg.get(next)! - 1);
      if (indeg.get(next) === 0) queue.push(next);
    }
  }
}

// order reduces crossings the cheap way: repeatedly put each node at the average
// height of what it connects to. Four passes is enough at this size and it is
// deterministic, which matters more here than optimality.
function order(nodes: Node[], arrows: Arrow[], byId: Map<string, Node>) {
  const cols = new Map<number, Node[]>();
  for (const n of nodes) {
    if (!cols.has(n.layer)) cols.set(n.layer, []);
    cols.get(n.layer)!.push(n);
  }
  const rank = new Map<string, number>();
  for (const [, col] of cols) {
    col.sort(
      (a, b) =>
        a.scope.localeCompare(b.scope) ||
        categoryOf(a.kind).localeCompare(categoryOf(b.kind)) ||
        a.kind.localeCompare(b.kind) ||
        a.name.localeCompare(b.name),
    );
    col.forEach((n, i) => rank.set(n.id, i));
  }

  const nbrs = new Map<string, string[]>();
  for (const n of nodes) nbrs.set(n.id, []);
  for (const a of arrows) {
    if (a.from === a.to) continue;
    nbrs.get(a.from)!.push(a.to);
    nbrs.get(a.to)!.push(a.from);
  }

  for (let pass = 0; pass < 4; pass++) {
    for (const [, col] of [...cols].sort(([a], [b]) => a - b)) {
      const bary = new Map<string, number>();
      for (const n of col) {
        const rs = nbrs.get(n.id)!.map((id) => rank.get(id) ?? 0);
        bary.set(n.id, rs.length ? rs.reduce((s, r) => s + r, 0) / rs.length : rank.get(n.id)!);
      }
      // scope first: a namespace is a container in the DSL, so its resources have
      // to stay adjacent in a column or the container cannot be drawn around them.
      col.sort(
        (a, b) =>
          a.scope.localeCompare(b.scope) ||
          bary.get(a.id)! - bary.get(b.id)! ||
          a.name.localeCompare(b.name),
      );
      col.forEach((n, i) => rank.set(n.id, i));
    }
  }
  return cols;
}

export const NODE_W = 168;
export const NODE_H = 40;
const COL = 236;
const ROW = 58;

// place turns the ordered columns into coordinates.
//
// Rows are banded BY SCOPE across the whole picture, not per column: the project
// root gets the top band and each namespace one below it. Laying out each column
// independently lets a root-scope resource land on the same rows as a namespace's,
// and the container drawn around that namespace then encloses a resource that is
// not in it — a diagram that lies about the one boundary it exists to show.
function place(cols: Map<number, Node[]>) {
  const all = [...cols.values()].flat();
  const bands = [...new Set(all.map((n) => n.scope))].sort((a, b) =>
    a === "" ? -1 : b === "" ? 1 : a.localeCompare(b),
  );

  const BAND_GAP = 30;
  const bandY = new Map<string, number>();
  let y = 22;
  for (const band of bands) {
    bandY.set(band, y);
    const tallest = Math.max(...[...cols.values()].map((c) => c.filter((n) => n.scope === band).length));
    y += tallest * ROW + BAND_GAP;
  }

  for (const [layer, col] of cols) {
    const used = new Map<string, number>();
    for (const n of col) {
      const i = used.get(n.scope) ?? 0;
      used.set(n.scope, i + 1);
      n.x = layer * COL + 20;
      n.y = bandY.get(n.scope)! + i * ROW;
    }
  }
}

export function extent(nodes: Node[]) {
  const w = Math.max(...nodes.map((n) => n.x + NODE_W), 400) + 24;
  const h = Math.max(...nodes.map((n) => n.y + NODE_H), 200) + 24;
  return { w, h };
}

// blastRadius: everything reachable from a node by following arrows forward. This is
// the query the model exists to make answerable — "what would this reach if it were
// compromised" — and it is a graph walk, not a review.
export function blastRadius(start: string, arrows: Arrow[]): Set<string> {
  const out = new Map<string, string[]>();
  for (const a of arrows) {
    if (a.ref) continue; // a build-time reference grants no runtime access
    if (!out.has(a.from)) out.set(a.from, []);
    out.get(a.from)!.push(a.to);
  }
  const seen = new Set<string>([start]);
  const stack = [start];
  while (stack.length) {
    for (const next of out.get(stack.pop()!) ?? []) {
      if (!seen.has(next)) {
        seen.add(next);
        stack.push(next);
      }
    }
  }
  seen.delete(start);
  return seen;
}

// reachesFrom: the inverse walk — who can get to this node. "What is exposed to the
// internet" is this, started from every ingress node.
export function reachesFrom(target: string, arrows: Arrow[]): Set<string> {
  const inb = new Map<string, string[]>();
  for (const a of arrows) {
    if (a.ref) continue;
    if (!inb.has(a.to)) inb.set(a.to, []);
    inb.get(a.to)!.push(a.from);
  }
  const seen = new Set<string>([target]);
  const stack = [target];
  while (stack.length) {
    for (const prev of inb.get(stack.pop()!) ?? []) {
      if (!seen.has(prev)) {
        seen.add(prev);
        stack.push(prev);
      }
    }
  }
  seen.delete(target);
  return seen;
}
