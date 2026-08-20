// The graph the compiled config already is.
//
// Nothing here infers anything, and by now nothing here CAN: tcc hands over the
// resources and the relationships between them, already resolved and already pointing
// the way the action goes. A node is a resource, an arrow is a relation, and the arrow's
// colour is the effect the vocabulary gave that verb.
//
// This file used to do the resolving — which name means which resource, which map of
// maps is a group of resources, which way a delivered verb runs. Every one of those is
// a rule the compiler already applies, and a second implementation of a rule is a
// disagreement waiting to happen: the copy here drew a node for every access entry and
// dropped a use whose namespace was relative, both of which read as facts about the
// config. What is left is layout and paint.

import type { Compiled, Effect, Emission, Instance, LoweredProject, Relation, Shapes } from "./wasm";

export interface Node {
  id: string;
  /**
   * The RESOURCE this box is. Usually its own id — but a resource many things reach is
   * drawn once per user, and every copy carries the same `of`. Anything about identity
   * — is this the selection, does this light up — asks this, never the id.
   */
  of: string;
  /** How many boxes this resource has, when it has more than one. */
  copies?: number;
  kind: string;
  name: string;
  scope: string; // "" for the project root, else the namespace name
  // What this resource is reached at, as far as it is knowable. From tcc's names: a
  // name is the label it chose plus the zone that answers, and gluing them together is
  // the vocabulary's job, not a drawing's.
  fqdn?: string;
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
  /** Traffic goes here: what a route, a proxy or a stream hands on to. */
  handoff?: boolean;
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

/** What this vocabulary's shapes are, from tcc. Set once, with the kinds. */
let SHAPES: Shapes = {
  refs: [],
  inbound: [],
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
  /** Every resource, from tcc: what a resource IS, and where it lives, is the schema's. */
  resources: Instance[],
  /** Every resolved relationship, from tcc. This file resolves no names. */
  relations: Relation[] = [],
  names: Record<string, { name: string; zone: string; fqdn: string; generated: boolean }> = {},
  /** What each lowerable resource becomes, from tcc. */
  emissions: Emission[] = [],
  /** The project AFTER lowering — the same shapes, because it is the same kind of thing. */
  loweredProject: LoweredProject = { resources: [], relations: [] },
  /** Which boxes are showing what they BECOME rather than what was written. */
  flipped: Set<string> = new Set(),
): Graph {
  const nodes: Node[] = [];
  const arrows: Arrow[] = [];

  // an access entry names its target; it is matched once every node is known
  // WHAT RESOURCES EXIST, from tcc. Finding them in the compiled object means telling a
  // nested group from an access block from a list of objects, and that is schema
  // knowledge: this file's own version of the walk drew a node for every access entry —
  // twenty boxes named after resources that already had one.
  for (const r of resources) {
    nodes.push({
      id: r.id,
      of: r.id,
      kind: r.kind,
      name: r.name || r.id.slice(0, 8),
      scope: r.scope,
      // Whatever this kind DERIVES — an SRV name, anything added later. Computed by the
      // engine, labelled by the schema, and never assembled here: a renderer composing
      // its own version of a fact is a second definition that agrees by coincidence.
      computed: derivedOf(r.kind, r.fields),
      layer: 0,
      x: 0,
      y: 0,
    });
  }

  // EVERY RELATIONSHIP, already resolved. Which name means which resource, which way a
  // delivered verb runs, and which reference points back at its declarer are all decided
  // by the compiler — this loop only picks how each class is drawn.
  for (const r of relations) {
    arrows.push({
      from: r.from,
      to: r.to,
      kind: r.kind,
      verb: r.verb,
      effect: (r.effect || "read") as Effect,
      policy: r.class === "policy" || undefined,
      handoff: r.class === "handoff" || undefined,
      ref: r.class === "reference" || undefined,
    });
  }

  const byId = new Map(nodes.map((n) => [n.id, n]));
  // An arrow to something outside this project compiles to an id we do not hold.
  const live = merge(arrows.filter((a) => byId.has(a.to) && byId.has(a.from)));

  // Coordinates come from ELK now (layout.ts) — this file decides WHAT is on the
  // graph, not where it goes.
  // WHAT EACH RESOURCE IS REACHED AT comes from tcc: the label it chose, the zone that
  // answers for it, and the project's own derived label are three separate places, and
  // whoever glues them together decides what the name IS. This file used to glue them.
  //
  // The cloud's fqdn is the one piece nothing knows — it is not in the config — so the
  // gap is DRAWN as a gap rather than filled in with a guess.
  for (const n of nodes) {
    const at = names[n.id];
    if (!at) continue;
    n.fqdn = at.generated ? `${at.fqdn || n.name}.⟨cloud⟩` : at.fqdn || `${at.name}.⟨zone⟩`;
    const zone = at.zone ? byId.get(at.zone) : undefined;
    if (zone) {
      n.answeredBy = `${zone.kind}/${zone.name}`;
      n.answeredById = zone.id;
    }
  }

  // A LOWERED RESOURCE IS A BOX, the same way a namespace is: a frame holding what it
  // becomes, with two faces — what was WRITTEN (the steps) and what it IS (the queues).
  //
  // Both faces are the SAME NODES. What a lowering produced is a real resource in the
  // lowered project, with a real id and real relations, so the page does not build a
  // second kind of node with a second kind of edge for it: it draws the resources tcc
  // says exist and the relations tcc says hold, which is what it does for everything
  // else. Turning a box over changes what the nodes are CALLED — a queue by its own
  // name, or by the step that produced it — and nothing else. Nothing moves, because
  // nothing about the layout changed.
  const frames = new Map<string, string>();
  const derivedIds = new Set<string>();

  for (const e of emissions) {
    const src = nodes.find((n) => n.kind === e.of.kind && n.name === e.of.name && n.scope === e.of.scope);
    if (!src) continue;

    // Where the resource actually LIVES, kept before the frame takes its place: what is
    // inside the box still reaches resources in the namespace the box is in.
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
    // Does the CLOSED face have anything of its own to show? A workflow does — its
    // steps — so its hidden parts keep reserving space and the box is the same size
    // either way, which is what stops the page moving when it is opened. An agent does
    // not: its lowering is not per-element, so closed there is nothing inside, and
    // reserving the open size drew a large empty rectangle.
    const shows = e.documents.some((d) => typeof d.part?.name === "string");

    for (const d of e.documents) {
      if (byId.has(d.id)) continue;
      const part = typeof d.part?.name === "string" ? (d.part.name as string) : "";
      if (!open && !shows) continue; // nothing to show, and nothing to reserve
      const node: Node = {
        id: d.id,
        of: d.id,
        // Closed, this node wears the step it came from; open, it wears what it is.
        kind: open || !part ? d.kind : "step",
        name: open || !part ? d.name : part,
        scope: frame,
        derived: { of: `${e.of.kind}/${e.of.name}`, why: d.why },
        // Something with no step behind it — the failure queue — exists in the layout
        // either way, so the box does not change size, and is simply not drawn on the
        // face where it does not belong.
        hidden: !open && !part,
        layer: 0,
        x: 0,
        y: 0,
      };
      nodes.push(node);
      byId.set(d.id, node);
      derivedIds.add(d.id);
    }
  }

  // THE EDGES INSIDE A BOX ARE THE PROJECT'S OWN. A queue is received from by the
  // function that runs the step, and that is an access entry in the lowered project —
  // the same shape, read the same way, as every other edge on this page. There is no
  // lowering-specific edge code left, and there is nothing here that knows what a step
  // is.
  for (const r of loweredProject.relations) {
    if (!derivedIds.has(r.from) && !derivedIds.has(r.to)) continue;
    if (!byId.has(r.from) || !byId.has(r.to)) continue;
    live.push({
      from: r.from,
      to: r.to,
      kind: r.kind,
      verb: r.verb,
      effect: (r.effect || "read") as Effect,
      policy: r.class === "policy" || undefined,
      ref: r.class === "reference" || undefined,
    });
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

  // A RESOURCE THAT EVERYTHING REACHES IS DRAWN ONCE PER USER.
  //
  // A secret six things read is one box with six lines converging on it, and those lines
  // cross everything between them and it. In the config it is one resource; on the page
  // the single box is what costs the most to read. So it is drawn beside each of its
  // users, and the box says how many of it there are — one resource, several pictures of
  // it, which is the honest way round: hiding the count would make copies read as
  // separate resources.
  //
  // Only a SINK is split — something with arrows in and none out. Copying a resource
  // that reaches things would copy those arrows too, or make one copy the real one and
  // the rest decoration, and either way it puts back the crossings it was meant to
  // remove.
  const nodesOut = splitSinks(nodes, live2, byId);

  const scopes = [...new Set(nodesOut.map((n) => n.scope))].filter(Boolean).sort();
  return { nodes: nodesOut, arrows: live2, byId, scopes, frames };
}


/** How many users a resource needs before drawing it beside each of them pays off. */
const SPLIT_AT = 3;

// splitSinks replaces a much-reached sink with one box per user, in place in the arrow
// list. Returns the nodes to draw; byId gains the copies, so anything that looks a node
// up by an arrow's endpoint still finds one.
function splitSinks(nodes: Node[], arrows: Arrow[], byId: Map<string, Node>): Node[] {
  const inbound = new Map<string, Arrow[]>();
  const outbound = new Set<string>();
  for (const a of arrows) {
    inbound.set(a.to, [...(inbound.get(a.to) ?? []), a]);
    outbound.add(a.from);
  }

  const out: Node[] = [];
  for (const n of nodes) {
    const ins = inbound.get(n.id) ?? [];
    // A box that is a frame, an anchor or a part of one belongs to a layout that already
    // groups it; splitting it would take it out of its box.
    const splittable =
      !n.frame && !n.anchor && !n.derived && !n.hidden && !outbound.has(n.id) && ins.length >= SPLIT_AT;
    if (!splittable) {
      out.push(n);
      continue;
    }
    ins.forEach((a, i) => {
      const copy: Node = { ...n, id: `${n.id}~${i}`, of: n.id, copies: ins.length };
      a.to = copy.id;
      byId.set(copy.id, copy);
      out.push(copy);
    });
  }
  return out;
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
