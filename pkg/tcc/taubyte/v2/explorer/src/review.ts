// What the graph says about the project, for the person who has to sign off on it.
//
// The graph answers "what is connected to what" by being looked at. An architect
// arrives with different questions — what is exposed, what crosses a tenancy
// boundary, what holds secrets, what is declared and never used — and answering those
// by tracing arrows with a finger does not scale past the first screenful.
//
// Every finding here is derived from the compiled object and the graph, never from a
// second reading of the yaml. Two consequences worth stating:
//
//   - This is the SAME set of checks the publish-time pass owes and does not yet
//     perform (schema/omissions.go). Computing them here does not make them enforced
//     — nothing refuses a deploy — and each one says so.
//   - A finding is not automatically a defect. A queue worker no ingress names is
//     correct; an HTTP application no ingress names is almost certainly a mistake.
//     The wording carries that difference rather than flattening it into a warning
//     count.

import type { Graph, Node } from "./graph";
import type { Compiled, KindVerbs } from "./wasm";

export type Severity = "broken" | "check" | "note";

export interface Finding {
  severity: Severity;
  title: string;
  detail: string;
  /** The resource this is about, so the list can drive the graph. */
  id?: string;
}

export interface Review {
  findings: Finding[];
  exposure: { id: string; kind: string; name: string; at: string; serves: string[] }[];
  counts: { kind: string; n: number }[];
}

const label = (n: Node) => `${n.kind}/${n.name}${n.scope ? ` in ${n.scope}` : ""}`;

// The kinds a route or a proxy can put in front of something. Read off the graph
// rather than restated: an ingress is whatever declares a "serves" arrow.
const servedBy = (g: Graph, id: string) => g.arrows.filter((a) => a.verb === "serves" && a.to === id);

export function review(g: Graph, compiled: Compiled, verbs: KindVerbs[]): Review {
  const findings: Finding[] = [];
  const byKind = new Map<string, Node[]>();
  for (const n of g.nodes) {
    if (!byKind.has(n.kind)) byKind.set(n.kind, []);
    byKind.get(n.kind)!.push(n);
  }

  const delivered = new Set<string>();
  for (const kv of verbs) for (const v of kv.verbs) if (v.delivered) delivered.add(`${kv.kind}/${v.name}`);

  // Resources indexed the way the compiled object stores them, so a node can be read
  // back for the fields the graph does not carry.
  const raw = new Map<string, Compiled>();
  const walk = (scope: Compiled) => {
    for (const val of Object.values(scope)) {
      if (!val || typeof val !== "object" || Array.isArray(val)) continue;
      for (const [id, res] of Object.entries(val as Record<string, Compiled>)) {
        if (!res || typeof res !== "object") continue;
        raw.set(id, res);
        walk(res); // namespaces hold resources; so do nested groups
      }
    }
  };
  walk(compiled);

  // ---- reachable from outside ------------------------------------------------
  //
  // A workload nothing fronts is not always wrong: a queue consumer is started by
  // work arriving, not by a request. The distinction is whether it consumes anything.
  for (const n of g.nodes) {
    if (n.kind !== "application" && n.kind !== "website" && n.kind !== "mcp") continue;
    if (servedBy(g, n.id).length) continue;

    const consumes = g.arrows.some((a) => a.to === n.id && delivered.has(`${a.kind}/${a.verb.split(", ")[0]}`));
    findings.push(
      consumes
        ? {
            severity: "note",
            id: n.id,
            title: `${label(n)} is not fronted by anything`,
            detail: "Correct for a consumer: work arrives from a queue or a channel rather than from a request. Listed so the absence is a decision you can see rather than one you have to remember.",
          }
        : {
            severity: "broken",
            id: n.id,
            title: `nothing reaches ${label(n)}`,
            detail: "No gateway route and no proxy names it, and it consumes nothing — so it runs and answers no one. Add a route, a proxy, or an access declaration from a consumer.",
          },
    );
  }

  // ---- declared ports with no feeder -----------------------------------------
  for (const n of g.nodes) {
    const res = raw.get(n.id);
    const ports = res?.ports;
    if (!ports || typeof ports !== "object") continue;

    const used = new Set<string>();
    // a probe polling a port is a feeder for it, same as an ingress
    for (const probe of ["readiness/port", "liveness/port"]) {
      const port = res[probe];
      if (typeof port === "string") used.add(port);
    }

    // A port is fed by an ingress that targets it, by a health probe, or — the one
    // this check missed at first — by a delivery: a queue posting each item to it is
    // exactly a feeder, and worker_pool was reported as dead config because of it.
    for (const val of Object.values(res)) {
      if (!val || typeof val !== "object" || Array.isArray(val)) continue;
      for (const entry of Object.values(val as Record<string, Compiled>)) {
        const port = entry?.["deliver/port"];
        if (typeof port === "string") used.add(port);
      }
    }

    for (const other of raw.values()) {
      for (const key of ["route/handler/port", "handler/port"]) {
        const port = other[key];
        const name = other[key.replace("/port", "/name")];
        if (typeof port === "string" && name === n.name) used.add(port);
      }
    }
    for (const key of Object.keys(ports as Record<string, unknown>)) {
      if (used.has(key)) continue;
      findings.push({
        severity: "check",
        id: n.id,
        title: `${label(n)} declares port "${key}" and nothing reaches it`,
        detail: "No ingress names it and no health check probes it. Either something should target this port by name, or the workload is not listening on what it says it is.",
      });
    }
  }

  // ---- a container told to receive, with nowhere to put it --------------------
  for (const n of g.nodes) {
    if (n.kind !== "application") continue;
    const res = raw.get(n.id);
    if (!res) continue;
    for (const [group, val] of Object.entries(res)) {
      if (!val || typeof val !== "object" || Array.isArray(val)) continue;
      for (const [target, entry] of Object.entries(val as Record<string, Compiled>)) {
        const ops = Array.isArray(entry?.ops) ? (entry.ops as string[]) : [];
        if (!ops.some((op) => delivered.has(`${group}/${op}`))) continue;
        if (typeof entry["deliver/port"] === "string") continue;
        findings.push({
          severity: "broken",
          id: n.id,
          title: `${label(n)} receives from ${group}/${target} and declares no deliver port`,
          detail: "A container has no host ABI, so an item has to be POSTed to a port it declared. Without deliver.port nothing arrives and the workload idles.",
        });
      }
    }
  }

  // ---- boundary crossings ----------------------------------------------------
  //
  // Not a defect — the point of namespaces is that they can be crossed deliberately.
  // It is the list an architect asks for first, because it is where a tenancy claim
  // is either kept or quietly given up.
  const crossings = g.arrows
    .filter((a) => !a.ref && !a.policy)
    .map((a) => ({ a, from: g.byId.get(a.from)!, to: g.byId.get(a.to)! }))
    .filter(({ from, to }) => from && to && from.scope !== to.scope);
  for (const { a, from, to } of crossings) {
    findings.push({
      severity: "note",
      id: from.id,
      title: `${label(from)} → ${label(to)}`,
      detail: `Crosses a namespace boundary with "${a.verb}". Deliberate or not, this is the edge a tenancy argument is made on.`,
    });
  }

  // ---- who can read a secret -------------------------------------------------
  for (const n of g.nodes) {
    if (n.kind !== "secret") continue;
    const readers = g.arrows
      .filter((a) => a.to === n.id)
      .map((a) => g.byId.get(a.from))
      .filter((x): x is Node => !!x);
    findings.push({
      severity: readers.length > 3 ? "check" : "note",
      id: n.id,
      title: `${label(n)} is read by ${readers.length} resource${readers.length === 1 ? "" : "s"}`,
      detail: readers.length ? readers.map(label).join(", ") : "Nothing reads it. A secret nobody uses is a credential still worth rotating.",
    });
  }

  // ---- what the internet can reach -------------------------------------------
  const exposure = g.nodes
    .filter((n) => n.fqdn)
    .map((n) => ({
      id: n.id,
      kind: n.kind,
      name: n.name,
      at: n.fqdn!,
      serves: g.arrows
        .filter((a) => a.from === n.id && a.verb === "serves")
        .map((a) => g.byId.get(a.to))
        .filter((x): x is Node => !!x)
        .map(label),
    }));

  const counts = [...byKind.entries()]
    .map(([kind, ns]) => ({ kind, n: ns.length }))
    .sort((a, b) => b.n - a.n || a.kind.localeCompare(b.kind));

  const rank: Record<Severity, number> = { broken: 0, check: 1, note: 2 };
  findings.sort((a, b) => rank[a.severity] - rank[b.severity]);

  return { findings, exposure, counts };
}
