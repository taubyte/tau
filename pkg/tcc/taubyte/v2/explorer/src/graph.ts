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
  /** How this resource is named, before the zone is resolved. */
  host?: { kind: string; name: string; zone: string | null };
  /** A zone's own apex, so a host that names it can be spelled out. */
  zoneFqdn?: string;
  /** Which zone answers this name — a zone is not an ingress, it is what resolves one. */
  answeredBy?: string;
  /** That zone's node id, or "" when it is the cloud's own and not a resource here. */
  answeredById?: string;
  // How a client reaches that name, when it is not just "https". An L4 proxy writes
  // no port and no address, so this is the only place the page can say what to dial.
  dial?: string;
  /** A resource nothing authored: what a lowered resource becomes, or a part of one. */
  derived?: { of: string; why: string };
  /** The frame this resource is the box FOR, when it has one. */
  frame?: string;
  /** Drawn as nothing: the box says what this is, so the node would be a duplicate. */
  anchor?: boolean;
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
  /**
   * What each frame is called. A namespace is one frame; a workflow is another, drawn
   * the same way because it is the same thing to a reader — a box holding parts that
   * belong together. The label is here rather than in the layout so the layout keeps
   * knowing nothing about kinds.
   */
  frames: Map<string, string>;
}

// Which category a kind belongs to is DECLARED on the kind, in the DSL
// (Category(...) in definition.go), and read back through kinds(). This file used
// to keep its own table; that table drifted the moment a kind was added, which is
// exactly the failure the whole "the config is the architecture" claim is about.
let CATEGORY: Record<string, string> = {};
let ICON: Record<string, string> = {};

export function setCategories(kinds: { group: string; category: string; icon?: string }[]) {
  CATEGORY = Object.fromEntries(kinds.map((k) => [k.group, k.category || "other"]));
  // The icon is declared on the kind too — Icon(...) in definition.go — so it comes
  // from the same place and drifts for the same reason if it is copied anywhere else.
  ICON = Object.fromEntries(kinds.map((k) => [k.group, k.icon || ""]));
}

export function categoryOf(kind: string): string {
  return CATEGORY[kind] ?? "other";
}

export function iconOf(kind: string): string {
  return ICON[kind] ?? "";
}

const isScopeContainer = (k: string) => k === "namespace";

/** One resource a lowered resource becomes, as computed by tcc — never here. */
export interface Lowered {
  kind: string;
  name: string;
  of: string;
  scope: string;
  why: string;
  links: { kind: string; name: string; verb: string; scope?: string }[];
  source?: { kind: string; name: string; scope: string; path: string; part?: Record<string, unknown> };
}

export function buildGraph(
  compiled: Compiled,
  verbs: KindVerbs[],
  projectLabel = "",
  lowered: Lowered[] = [],
  /** Which lowered resources are showing what they BECOME rather than what was written. */
  flipped: Set<string> = new Set(),
): Graph {
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
      // The name is a zone plus a label now, and for a generated host the zone is
      // not knowable here at all — it is the cloud's, decided at deploy. Resolved in
      // a second pass, once the zones are compiled and can be looked up.
      host:
        typeof res["host/kind"] === "string"
          ? {
              kind: res["host/kind"] as string,
              name: typeof res["host/name"] === "string" ? (res["host/name"] as string) : "",
              zone: typeof res["host/zone"] === "string" ? (res["host/zone"] as string) : null,
            }
          : undefined,
      zoneFqdn: group === "zone" && typeof res.fqdn === "string" ? (res.fqdn as string) : undefined,
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

      // the transformer chain: an ORDERED list of objects, each naming a function by
      // (namespace, name) — the same pair every other cross-namespace reference uses,
      // so it resolves through the same pass.
      if (key === "transformers" && Array.isArray(val)) {
        val.forEach((entry, i) => {
          const e = entry as Compiled;
          if (typeof e?.name !== "string") return;
          const ns = typeof e.namespace === "string" ? (e.namespace as string) : null;
          pendingPolicy.push({
            from: id, name: e.name, scope: ns,
            verb: `transforms #${i + 1}${e.fail === "open" ? ", fails open" : ""}`,
          });
        });
        continue;
      }

      // A gateway's host/zone is drawn as the INGRESS PATH — internet, then the zone
      // that answers, then what the name points at — so drawing it again here as a
      // reference would say the same thing twice, and in the wrong direction: the
      // gateway does not reach the zone, the zone answers for the gateway.
      if (key === "host/zone") continue;

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
    named("readiness/function/name", "readiness/function/namespace", "readiness decided by");
    named("liveness/function/name", "liveness/function/namespace", "liveness decided by");

    // An ingress names its target by (kind, namespace, name) rather than resolving it
    // to an id — a name in another namespace has no spelling the ref resolver accepts.
    // Resolve it here, against the nodes actually compiled. Three prefixes because
    // there are three ways in: a gateway route parses HTTP and carries the handler
    // under `route/`, an L4 proxy parses nothing and forwards, and a stream is the
    // one that never leaves the project.
    for (const p of ["route/handler", "forward", "serve"]) {
      const hk = res[`${p}/kind`];
      const hn = res[`${p}/name`];
      if (typeof hk !== "string" || typeof hn !== "string") continue;
      const ns = typeof res[`${p}/namespace`] === "string" ? (res[`${p}/namespace`] as string) : "";
      pendingHandlers.push({ from: id, kind: hk, name: hn, scope: ns.replace(/^\/+/, "") });
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
      const target = nodes.find((n) => n.kind === "function" && n.name === pol.name && n.scope === scope);
      if (target) {
        arrows.push({
          from: pol.from, to: target.id, kind: "function",
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
  const live = merge(arrows.filter((a) => byId.has(a.to) && byId.has(a.from)));

  // Coordinates come from ELK now (layout.ts) — this file decides WHAT is on the
  // graph, not where it goes.
  // A host names a zone; the zone knows its apex. A GENERATED host names the cloud's
  // own zone, which no compiled object contains and no config may assert — so it is
  // shown as the placeholder it is rather than guessed at.
  // Which zone answers each name, so the zone stops looking like a leaf nothing uses.
  const zoneOf = new Map<string, string>();
  const zoneIdOf = new Map<string, string>();
  for (const n of nodes) {
    if (!n.host) continue;
    const label = n.host.name ? `${n.host.name}.` : "";
    if (n.host.kind === "generated") {
      // The project's own label is derived from its id, so it is known here. The
      // cloud's fqdn is not in the config and is not guessed.
      n.fqdn = projectLabel ? `${label}${projectLabel}.⟨cloud⟩` : `${label}⟨cloud zone⟩`;
      continue;
    }
    const zoneID = n.host.zone?.replace(/^zone\//, "");
    const zone = zoneID ? byId.get(zoneID) : undefined;
    n.fqdn = zone?.zoneFqdn ? `${label}${zone.zoneFqdn}` : `${label}⟨zone⟩`;
    if (zone) {
      zoneOf.set(n.id, `${zone.kind}/${zone.name}`);
      zoneIdOf.set(n.id, zone.id);
    }
  }

  for (const n of nodes) {
    n.answeredBy = zoneOf.get(n.id);
    n.answeredById = zoneIdOf.get(n.id);
  }

  // A LOWERED RESOURCE IS A BOX, the same way a namespace is: a frame holding the parts
  // that belong to it. Two things can be inside it, and the box flips between them —
  // what was WRITTEN (the steps, wired to each other and to what they run) and what it
  // BECOMES (the queues and the entity). Both are read from the same plan tcc computed;
  // neither is recomputed here, and nothing in this file knows what a step is.
  const frames = new Map<string, string>();
  const sourcesOf = new Map<string, Lowered[]>();
  for (const d of lowered) {
    const of = d.of;
    sourcesOf.set(of, [...(sourcesOf.get(of) ?? []), d]);
  }

  for (const [of, parts] of sourcesOf) {
    const src = nodes.find((n) => `${n.kind}/${n.name}` === of);
    if (!src) continue;

    // `@` cannot begin a resource name, so a frame can never collide with a namespace.
    const frame = `${src.scope ? `${src.scope}/` : ""}@${src.name}`;
    frames.set(frame, `${src.kind} · ${src.name}`);
    src.scope = frame;
    src.frame = frame;
    // The BOX IS the resource, so drawing the resource inside it says the same thing
    // twice. It stays in the model — everything that points at a workflow still points
    // at it, and the layout needs something inside the cluster to route those edges to —
    // but it is drawn as nothing: an anchor the arrows land on, at the box's edge.
    src.anchor = true;

    if (flipped.has(src.id)) {
      for (const d of parts) {
        const id = `derived:${d.kind}/${d.name}`;
        if (byId.has(id)) continue;
        const node: Node = {
          id, kind: d.kind, name: d.name, scope: frame,
          derived: { of: d.of, why: d.why }, layer: 0, x: 0, y: 0,
        };
        nodes.push(node);
        byId.set(id, node);
        live.push({ from: src.id, to: id, kind: d.kind, verb: "becomes", effect: "read", ref: true });
      }
      for (const d of parts) {
        const from = byId.get(`derived:${d.kind}/${d.name}`);
        if (!from) continue;
        for (const l of d.links ?? []) {
          const to =
            byId.get(`derived:${l.kind}/${l.name}`) ??
            nodes.find((n) => n.kind === l.kind && n.name === l.name && n.scope === d.scope);
          if (!to) continue;
          live.push({ from: from.id, to: to.id, kind: l.kind, verb: l.verb, effect: "write" });
        }
      }
      continue;
    }

    // As written: one node per part, in the order the author wrote them. A part with no
    // name of its own is skipped rather than drawn as an empty box.
    const partNodes = new Map<string, Node>();
    for (const d of parts) {
      const path = d.source?.path;
      const partName = typeof d.source?.part?.name === "string" ? (d.source.part.name as string) : "";
      if (!path || !partName || partNodes.has(path)) continue;
      const id = `part:${of}/${path}`;
      const node: Node = {
        id, kind: "step", name: partName, scope: frame,
        derived: { of, why: d.why }, layer: 0, x: 0, y: 0,
      };
      partNodes.set(path, node);
      nodes.push(node);
      byId.set(id, node);
      live.push({ from: src.id, to: id, kind: "step", verb: "runs", effect: "read", ref: true });
    }

    // The wiring, attributed to the part rather than to what it became: a step runs its
    // function, and hands on to the step after it.
    const partByName = new Map([...partNodes.values()].map((n) => [n.name, n]));
    for (const d of parts) {
      const from = d.source?.path ? partNodes.get(d.source.path) : undefined;
      if (!from) continue;
      for (const l of d.links ?? []) {
        // A link to another part of the same lowering is drawn between the PARTS: the
        // queue named `checkout_charge` is the step named `charge`, seen from the other
        // side of the lowering.
        const sibling = [...sourcesOf.get(of)!].find((o) => o.kind === l.kind && o.name === l.name);
        const to = sibling?.source?.part?.name
          ? partByName.get(sibling.source.part.name as string)
          : nodes.find((n) => n.kind === l.kind && n.name === l.name && n.scope === (l.scope || src.scope));
        if (!to || to.id === from.id) continue;
        live.push({ from: from.id, to: to.id, kind: l.kind, verb: l.verb, effect: "write" });
      }
    }
  }

  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();
  return { nodes, arrows: live, byId, scopes, frames };
}

export const NODE_W = 168;
// MUST match what .rf-node actually renders at (style.css pins the same number).
// dot reserves corridors for the edges around a box of exactly this size: when the
// rendered node was 6px taller than the reserved one, every route dot planned along
// the bottom of a node ran under it instead, and arrowheads landed on corners.
export const NODE_H = 46;
// One line per PAIR, not per verb.
//
// `api` declaring `caches: { orders: { ops: [get, put] } }` was drawing two arrows
// between the same two boxes, following almost the same path — twice the ink for one
// fact, and on a real project the duplicates are most of what makes the picture hard
// to read.
//
// The verbs merge into the tooltip and the EFFECT takes the strongest of them, which
// is the honest colour for a blast-radius view: if a caller may put, the edge is a
// write edge no matter how many reads sit beside it.
//
// A reference and a policy arrow never merge into a use, because they are not one:
// they carry no verb and they are excluded from the blast radius.
const RANK: Record<Effect, number> = { read: 0, write: 1, admin: 2 };

function merge(arrows: Arrow[]): Arrow[] {
  const out = new Map<string, Arrow>();
  for (const a of arrows) {
    const key = `${a.from}->${a.to}|${a.policy ? "p" : a.ref ? "r" : "use"}`;
    const seen = out.get(key);
    if (!seen) {
      out.set(key, { ...a });
      continue;
    }
    if (!seen.verb.split(", ").includes(a.verb)) seen.verb += `, ${a.verb}`;
    if (RANK[a.effect] > RANK[seen.effect]) seen.effect = a.effect;
  }
  return [...out.values()];
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
