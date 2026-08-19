// The graph the compiled config already is.
//
// Nothing here infers anything: a node is a compiled resource, an arrow is a
// `use-<kind>-<verb>` key on it, and the arrow's colour is the verb's effect class.
// That is the whole claim of the model — the diagram is a read of the config, not a
// drawing kept beside it — so this file is deliberately dumb.

import type { Compiled, Effect, KindVerbs, Shapes } from "./wasm";

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
  /** Whatever this kind derives — computed by the engine, labelled by the schema. */
  computed?: { label: string; value: string }[];
  /** A resource nothing authored: what a lowered resource becomes, or a part of one. */
  derived?: { of: string; why: string };
  /** The frame this resource is the box FOR, when it has one. */
  frame?: string;
  /**
   * Where the resource actually lives, when a frame has taken over its scope. The
   * frame is a layout key, not a place — everything that answers "where is this" (its
   * file, its namespace) has to keep asking the real one.
   */
  home?: string;
  /** Drawn as nothing: the box says what this is, so the node would be a duplicate. */
  anchor?: boolean;
  /** In the layout but not on the page: it exists on the box's other face. */
  hidden?: boolean;
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
  /** Which face of a box this belongs to. Absent means both. */
  face?: "written" | "lowered";
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

export function setCategories(kinds: { group: string; category: string; icon?: string; container?: boolean }[]) {
  SCOPE_CONTAINER = kinds.find((k) => k.container)?.group ?? "";
  CATEGORY = Object.fromEntries(kinds.map((k) => [k.group, k.category || "other"]));
  // The icon is declared on the kind too — Icon(...) in definition.go — so it comes
  // from the same place and drifts for the same reason if it is copied anywhere else.
  ICON = Object.fromEntries(kinds.map((k) => [k.group, k.icon || ""]));
}

export function categoryOf(kind: string): string {
  // `step` is not a kind — it is a part of one, and the vocabulary has no category for
  // it. It gets its own so it is coloured as what it is rather than falling through to
  // the colour of everything unrecognised.
  if (kind === "step") return "part";
  return CATEGORY[kind] ?? "other";
}

export function iconOf(kind: string): string {
  // `step` is a PART of a kind rather than a kind, so the schema has no icon for it and
  // the lookup fell through to the fallback dot — which is what every step was drawn as.
  if (kind === "step") return "step";
  return ICON[kind] ?? "";
}

// WHICH KIND IS THE SCOPE CONTAINER is a fact the schema states — kinds() has carried
// `container` from the start — and this file was typing the name instead. A vocabulary
// that called its scopes something else would have compiled here and drawn a flat
// project.
let SCOPE_CONTAINER = "";
const isScopeContainer = (k: string) => !!SCOPE_CONTAINER && k === SCOPE_CONTAINER;

/** One resource a lowered resource becomes, as computed by tcc — never here. */
export interface Lowered {
  kind: string;
  name: string;
  of: string;
  scope: string;
  why: string;
  id?: string;
  yaml?: string;
  acts?: boolean;
  links: {
    kind: string;
    name: string;
    verb: string;
    scope?: string;
    effect?: string;
    ref?: boolean;
    from?: { kind: string; name: string };
  }[];
  source?: { kind: string; name: string; scope: string; path: string; part?: Record<string, unknown> };
}

/** What this vocabulary's shapes are, from tcc. Set once, with the kinds. */
let SHAPES: Shapes = {
  handsTo: [],
  names: [],
  kinds: {},
  access: { ops: "ops", namespace: "namespace" },
  layout: { resource: "{kind}/{name}.yaml", scope: "{name}", leaf: "project" },
};

export function setShapes(s: Shapes): void {
  SHAPES = s;
}

/** Where a resource's document lives, from the layout tcc reports. */
export function fileOf(n: { kind: string; name: string; scope: string; home?: string }): string {
  const scope = n.home ?? n.scope;
  const dirs = scope
    ? scope.split("/").map((s) => SHAPES.layout.scope.replace("{name}", s)).join("/") + "/"
    : "";
  return dirs + SHAPES.layout.resource.replace("{kind}", n.kind).replace("{name}", n.name);
}

/** The naming block a kind declares, read into the shape the drawing uses. */
function hostOf(kind: string, res: Compiled): Node["host"] {
  const at = SHAPES.kinds[kind]?.answersAt;
  if (!at) return undefined;
  const k = res[`${at}/kind`];
  if (typeof k !== "string") return undefined;
  return {
    kind: k,
    name: typeof res[`${at}/name`] === "string" ? (res[`${at}/name`] as string) : "",
    zone: typeof res[`${at}/zone`] === "string" ? (res[`${at}/zone`] as string) : null,
  };
}

/** Every derived field of a kind, computed by the engine. */
function derivedOf(kind: string, res: Compiled): { label: string; value: string }[] | undefined {
  const specs = SHAPES.kinds[kind]?.derived;
  if (!specs?.length || !window.tccV2) return undefined;
  const out: { label: string; value: string }[] = [];
  for (const spec of specs) {
    const inputs = spec.from.map((f) => (typeof res[f] === "string" ? (res[f] as string) : ""));
    if (inputs.some((i) => i === "")) continue;
    const value = window.tccV2.derive(spec.derivation, ...inputs);
    if (value) out.push({ label: spec.label || spec.field, value });
  }
  return out.length ? out : undefined;
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
  const pendingPolicy: { from: string; name: string; scope: string | null; verb: string; kind: string }[] = [];
  const pendingAccess: {
    from: string; kind: string; name: string; scope: string | null; verb: string; effect: Effect;
    inbound?: boolean;
  }[] = [];

  // an access block is a map of target-name -> { ops, namespace? }
  // An ACCESS BLOCK is a key that names a reachable kind, holding entries with the ops
  // field the schema says an access entry has. It used to be "entries with a key called
  // ops", which is the same guess written as a fact.
  const looksLikeAccess = (v: unknown): v is Record<string, Compiled> =>
    !!v && typeof v === "object" && !Array.isArray(v) &&
    Object.values(v as Record<string, unknown>).every(
      (e) => !!e && typeof e === "object" && !Array.isArray(e) && SHAPES.access.ops in (e as object),
    );

  // A compiled resource may itself hold a nested group — a gateway holds its
  // routes — and a namespace may hold namespaces, at any depth. Both are just
  // maps of maps in the compiled object, so the walk is written once and applies
  // to whatever the schema nests next.
  // A nested map is a group of RESOURCES only if its key is a kind. `variables:` and
  // `ports:` are maps of maps too, and drawing them put a node on the page for every
  // environment variable — they are data belonging to a resource, not resources.
  const looksLikeResources = (key: string, v: unknown): v is Record<string, Compiled> =>
    kindNames.has(key) &&
    !!v &&
    typeof v === "object" &&
    !Array.isArray(v) &&
    Object.values(v as Record<string, unknown>).every(
      (x) => !!x && typeof x === "object" && !Array.isArray(x),
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
      // WHERE this resource says what name it answers on is the schema's answer
      // (AnswersAt), not a key spelled here.
      host: hostOf(group, res),
      // A kind that carries an fqdn and answers nothing else is what resolves a name:
      // it is read off the field, not off the kind's name.
      zoneFqdn: !SHAPES.kinds[group]?.answersAt && typeof res.fqdn === "string" ? (res.fqdn as string) : undefined,
      // Whatever this kind DERIVES — an SRV name, anything added later. Computed by the
      // engine, labelled by the schema, and never assembled here: a renderer composing
      // its own version of a fact is a second definition that agrees by coincidence.
      computed: derivedOf(group, res),
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
          const opsField = entry[SHAPES.access.ops];
          const ops = Array.isArray(opsField) ? (opsField as string[]) : [];
          // "/x" is absolute from the project root, "x" is relative to this
          // resource's own namespace, "/" is the root itself, absent means the
          // resource's own scope then its parents.
          const nsField = entry[SHAPES.access.namespace];
          const ns = typeof nsField === "string" ? (nsField as string) : null;
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

      // A LIST OF OBJECTS whose elements name resources — a transformer chain — needs
      // no branch of its own: each element's fields are declared in the schema the same
      // way a resource's are, so the same pass that draws an authorizer draws these.
      if (Array.isArray(val)) {
        val.forEach((entry, i) => {
          const el = entry as Compiled;
          if (!el || typeof el !== "object") return;
          for (const f of SHAPES.names.filter((x) => x.in === key)) {
            const n = el[f.field];
            if (typeof n !== "string" || n === "") continue;
            const ns = f.namespace && typeof el[f.namespace] === "string" ? (el[f.namespace] as string) : null;
            pendingPolicy.push({ from: id, name: n, scope: ns, kind: f.kind, verb: `${key} #${i + 1}` });
          }
        });
        continue;
      }

      // A REFERENCE is one the schema declares — `Ref(kind)` on the attribute — not any
      // string that happens to contain a slash. The guess drew whatever looked the part,
      // and could not tell a reference from a value with a slash in it.
      const declared = (SHAPES.kinds[group]?.refs ?? []).find((r) => r.field === key);
      if (declared && typeof val === "string" && val !== "") {
        const to = val.slice(val.indexOf("/") + 1);
        if (to && to !== val) {
          // WHICH WAY IT RUNS is the schema's to say. Most references are declared by
          // the thing that depends on the target; some — a name and the zone that
          // answers for it — run the other way, and an arrow from the declarer would
          // claim the opposite of what happens.
          arrows.push(
            declared.inbound
              ? { from: to, to: id, kind: group, verb: key, effect: "read", ref: true }
              : { from: id, to, kind: declared.kind, verb: key, effect: "read", ref: true },
          );
        }
        continue;
      }

      // a nested group under this resource
      if (looksLikeResources(key, val)) {
        for (const [childID, child] of Object.entries(val)) {
          addResource(key, childID, child, scope);
          arrows.push({ from: id, to: childID, kind: key, verb: key, effect: "read", ref: true });
        }
      }
    }

    // Two places a resource names a function that decides something ABOUT it, rather
    // than something it reaches: who may use it, and whether it is ready. Both are
    // the resource's own declaration, so both arrows start at it.
    // A field that holds the NAME of another resource — an authorizer, a readiness
    // function, a step's function — is declared as one in the schema (NamesResource),
    // so this draws them all without knowing which fields they are. It used to be three
    // hardcoded field names, which meant a fourth drew nothing.
    for (const f of SHAPES.names) {
      if (f.in) continue; // it lives on a nested entry, not on the resource
      const n = res[f.field];
      if (typeof n !== "string" || n === "") continue;
      const ns = f.namespace && typeof res[f.namespace] === "string" ? (res[f.namespace] as string) : null;
      // The verb is the field's own name, which is what the author wrote and what the
      // reader is looking for — `authorizer/name` reads as "authorizer".
      pendingPolicy.push({
        from: id, name: n, scope: ns, kind: f.kind,
        verb: f.field.replace(/\/?name$/, "").replace(/\//g, " ") || f.kind,
      });
    }

    // An ingress names its target by (kind, namespace, name) rather than resolving it
    // to an id — a name in another namespace has no spelling the ref resolver accepts.
    // Resolve it here, against the nodes actually compiled. WHICH blocks those are is
    // the schema's answer, not this file's: a gateway route carries its handler under
    // `route/`, a proxy forwards, a stream is served — three spellings on purpose, and
    // a list of them here is a copy that goes stale the next time a kind gains one.
    for (const p of SHAPES.handsTo) {
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
      const target = nodes.find((n) => n.kind === pol.kind && n.name === pol.name && n.scope === scope);
      if (target) {
        arrows.push({
          from: pol.from, to: target.id, kind: pol.kind,
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
    // WHICH BRANCH this is does not need its name: a name in a zone the project owns
    // carries a reference to that zone, and the other kind of name does not. Reading
    // the branch's spelling meant this file knew the word "generated".
    if (!n.host.zone) {
      // The project's own label is derived from its id, so it is known here. The
      // cloud's fqdn is not in the config and is not guessed.
      n.fqdn = projectLabel ? `${label}${projectLabel}.⟨cloud⟩` : `${label}⟨cloud zone⟩`;
      continue;
    }
    // A scalar reference compiles to "<kind>/<id>", whatever the kind is.
    const zoneID = n.host.zone.slice(n.host.zone.indexOf("/") + 1);
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
  // that belong to it, with two faces — what was WRITTEN (the steps) and what it BECOMES
  // (the queues). Both are read from the same plan tcc computed; neither is recomputed
  // here, and nothing in this file knows what a step is.
  //
  // THE LAYOUT IS THE SAME FOR BOTH FACES, and that is the whole trick. Turning a box
  // over used to re-run dot with a different node count, which moved every other node on
  // the page — a gateway three columns away jumped 300px, and panning to hold the box
  // still just pushed other things off the edge of the panel. So the layout is always
  // computed for the LOWERED face, edges included, and the written face reuses those
  // positions: one node per derived resource, wearing the step's name when the box is
  // closed and the queue's when it is open.
  //
  // Nothing moves, because nothing about the layout changed. No pan, no animation, no
  // clipping.
  const frames = new Map<string, string>();
  const sourcesOf = new Map<string, Lowered[]>();
  for (const d of lowered) {
    sourcesOf.set(d.of, [...(sourcesOf.get(d.of) ?? []), d]);
  }

  for (const [of, parts] of sourcesOf) {
    const src = nodes.find((n) => `${n.kind}/${n.name}` === of);
    if (!src) continue;

    // Where the resource actually LIVES, kept before the frame takes its place: the
    // parts inside a box still reach resources in the namespace the box is in.
    const home = src.scope;

    // `@` cannot begin a resource name, so a frame can never collide with a namespace.
    const frame = `${src.scope ? `${src.scope}/` : ""}@${src.name}`;
    frames.set(frame, `${src.kind} · ${src.name}`);
    src.home = home;
    src.scope = frame;
    src.frame = frame;
    // The box IS the resource, so drawing the resource inside it says the same thing
    // twice. It stays in the model — everything that points at a workflow still points
    // at it — but it is drawn as a point the arrows land on, at the box's edge.
    src.anchor = true;

    const open = flipped.has(src.id);
    const byPath = new Map<string, Node>();

    // The part that ACTS for this resource, when the lowering names one: an agent's
    // tools are called by its loop, so with the box open the agent's own edges hang off
    // the loop rather than leaving the box for reasons the picture does not give.
    const actor = open ? parts.find((d) => d.acts) : undefined;
    if (actor) {
      for (const a of live) {
        if (a.from === src.id) a.from = `derived:${actor.kind}/${actor.name}`;
      }
    }

    // Does the CLOSED face have anything of its own to show? A workflow does — its
    // steps — so its hidden parts keep reserving space and the box is the same size
    // either way, which is what stops the page moving when it is opened.
    //
    // An agent does NOT: its lowering is not per-element, so closed there is nothing
    // inside, and reserving the open size drew a large empty rectangle. It gets a box
    // the size of its label instead, and grows when it is opened. A box with nothing in
    // it is worse than a box that changes size.
    const showsParts = parts.some((d) => typeof d.source?.part?.name === "string");

    for (const d of parts) {
      const id = `derived:${d.kind}/${d.name}`;
      if (byId.has(id)) continue;
      const part = typeof d.source?.part?.name === "string" ? (d.source.part.name as string) : "";
      if (!open && !showsParts) continue; // nothing to show, and nothing to reserve
      const node: Node = {
        id,
        // Closed, this node IS the step it came from; open, it is what the step became.
        kind: open || !part ? d.kind : "step",
        name: open || !part ? d.name : part,
        scope: frame,
        derived: open ? { of: d.of, why: d.why } : undefined,
        // A derived resource with no step behind it — the failure queue — exists in the
        // layout either way, so the box does not change size, and is simply not drawn
        // on the face where it does not exist.
        hidden: !open && !part,
        layer: 0,
        x: 0,
        y: 0,
      };
      nodes.push(node);
      byId.set(id, node);
      if (d.source?.path) byPath.set(d.source.path, node);
    }

    // Both faces' edges, tagged with the face they belong to. dot sees all of them, so
    // the ranking is the same whichever face is showing; the page draws one face.
    for (const d of parts) {
      const self = byId.get(`derived:${d.kind}/${d.name}`);
      if (!self) continue;
      for (const l of d.links ?? []) {
        const target =
          byId.get(`derived:${l.kind}/${l.name}`) ??
          nodes.find((n) => n.kind === l.kind && n.name === l.name && n.scope === (l.scope || home));
        if (!target) continue;
        const effect = (l.effect as Effect) ?? "write";

        // OPEN: exactly what tcc said, origin included — a function sends to the next
        // queue, a queue points at its dead-letter.
        const origin = l.from
          ? (byId.get(`derived:${l.from.kind}/${l.from.name}`) ??
            nodes.find((n) => n.kind === l.from!.kind && n.name === l.from!.name))
          : self;
        if (origin) {
          live.push({ from: origin.id, to: target.id, kind: l.kind, verb: l.verb, effect, ref: l.ref, face: "lowered" });
        }

        // CLOSED: the same relationships attributed to the STEP, because that is what a
        // reader has in front of them — the hop to the next step is the step's, not a
        // queue's, and the function is what the step runs. A link that only points at
        // lowered machinery (the dead-letter) has nothing to say as written.
        if (l.ref) continue;
        if (!target.hidden) {
          live.push({ from: self.id, to: target.id, kind: l.kind, verb: l.verb, effect, face: "written" });
        }

        // ...and an edge that comes in from OUTSIDE lands on the workflow. Open, the
        // function that ran a step sends to the next step's queue, which is a thing
        // inside the box; closed, that queue is not a thing the reader has been shown,
        // so the arrow would either vanish — leaving the function with no outgoing edge
        // it plainly has — or point at machinery. It points at the box, which is the
        // resource that owns what happens next.
        if (origin && origin.id !== self.id && origin.scope !== frame) {
          live.push({ from: origin.id, to: src.id, kind: src.kind, verb: l.verb, effect, face: "written" });
        }
      }
    }
  }

  // The zone that answers for a name is drawn as the INGRESS PATH — internet, then the
  // zone, then what the name points at — which is the same pair as the inbound
  // reference the config declares. Drawn twice it is two lines saying one thing, so the
  // one the path already covers is dropped.
  //
  // Not "skip this key": the rule is that an edge the ingress path draws is not drawn
  // again, which holds for whatever else ever answers for a name.
  const drawnByIngress = new Set(
    nodes.filter((n) => n.answeredById).map((n) => `${n.answeredById}->${n.id}`),
  );
  const live2 = live.filter((a) => !drawnByIngress.has(`${a.from}->${a.to}`));

  const scopes = [...new Set(nodes.map((n) => n.scope))].filter(Boolean).sort();
  return { nodes, arrows: live2, byId, scopes, frames };
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
