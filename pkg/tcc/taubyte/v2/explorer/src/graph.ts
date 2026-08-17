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
  // How a client reaches that name, when it is not just "https". An L4 proxy writes
  // no port and no address, so this is the only place the page can say what to dial.
  dial?: string;
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
  const verbDelivered = new Set<string>();
  for (const kv of verbs) {
    for (const v of kv.verbs) {
      verbEffect.set(`${kv.kind}/${v.name}`, v.effect);
      if (v.delivered) verbDelivered.add(`${kv.kind}/${v.name}`);
    }
  }

  const root = (compiled.object as Compiled) ?? compiled;
  const pendingHandlers: { from: string; kind: string; name: string; scope: string }[] = [];
  // an access entry names its target; it is matched once every node is known
  const pendingPolicy: { from: string; name: string; scope: string | null; verb: string }[] = [];
  const pendingAccess: {
    from: string; kind: string; name: string; scope: string | null; verb: string; effect: Effect;
    inbound?: boolean;
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
      dial: typeof res.protocol === "string"
        ? `${res.protocol}${typeof res.service === "string" ? ` · _${res.service}._${res.protocol}` : ""}`
        : undefined,
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
              // `receive` and `subscribe` are declared by the consumer but the work
              // travels the other way. Drawn from the declaration, a queue worker has
              // two arrows out and none in, and reads as a workload nothing ever
              // starts — which is exactly backwards from what runs.
              inbound: verbDelivered.has(`${key}/${op}`),
            });
          }
        }
        continue;
      }

      // the transformer chain: keyed by the FUNCTION's name, with the fields of this
      // use under it. Same addressing as an authorizer — a name plus an optional
      // namespace — so it resolves through the same pass.
      if (key === "transformers" && !!val && typeof val === "object" && !Array.isArray(val)) {
        for (const [fn, e] of Object.entries(val as Record<string, Compiled>)) {
          const ns = typeof e.namespace === "string" ? (e.namespace as string) : null;
          pendingPolicy.push({
            from: id, name: fn, scope: ns,
            verb: `transforms ${e.direction ?? "inbound"}, #${e.order ?? "?"}`,
          });
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

    // Two places a resource names a function that decides something ABOUT it, rather
    // than something it reaches: who may use it, and whether it is ready. Both are
    // the resource's own declaration, so both arrows start at it.
    const named = (nameKey: string, nsKey: string, verb: string) => {
      const n = res[nameKey];
      if (typeof n !== "string") return;
      const ns = typeof res[nsKey] === "string" ? (res[nsKey] as string) : null;
      pendingPolicy.push({ from: id, name: n, scope: ns, verb });
    };
    named("authorizer/name", "authorizer/namespace", "authorized by");
    named("health/function/name", "health/function/namespace", "readiness decided by");

    // An ingress names its target by (kind, namespace, name) rather than resolving it
    // to an id — a name in another namespace has no spelling the ref resolver accepts.
    // Resolve it here, against the nodes actually compiled. Two prefixes because there
    // are two front doors: a gateway route parses HTTP and carries the handler under
    // `route/`, an L4 proxy parses nothing and has only the handler.
    for (const p of ["route/handler", "handler"]) {
      const hk = res[`${p}/kind`];
      const hn = res[`${p}/name`];
      if (typeof hk !== "string" || typeof hn !== "string") continue;
      const ns = typeof res[`${p}/namespace`] === "string" ? (res[`${p}/namespace`] as string) : "";
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
        // a delivered verb is flipped: the declaration stays where it is authored,
        // the ARROW follows the work
        arrows.push(
          a.inbound
            ? { from: target.id, to: a.from, kind: a.kind, verb: a.verb, effect: a.effect }
            : { from: a.from, to: target.id, kind: a.kind, verb: a.verb, effect: a.effect },
        );
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
          verb: pol.verb, effect: "read", policy: true,
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

  // Coordinates come from ELK now (layout.ts) — this file decides WHAT is on the
  // graph, not where it goes.
  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();
  return { nodes, arrows: live, byId, scopes };
}

export const NODE_W = 168;
export const NODE_H = 40;
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
