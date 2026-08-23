import "./style.css";
import { loadTcc, type CompileOk, type Kind, type KindVerbs, type Omission } from "./wasm";
import { buildGraph, categoryOf, fileOf, iconOf, setCategories, setScopes, setShapes, type Graph } from "./graph";
import { esc, jsonRoot } from "./render";
import { graphHost, renderGraph } from "./graph-mount";
import { kindIconMarkup } from "./icons";
import { editorHost, onEditorChange, showFile } from "./editor";
import type { Finding } from "./wasm";
import type { Selection } from "./graph-view";
import { CLOUD_ZONE, INTERNET, place, type Placed } from "./layout";

// The example project is the SAME tree the Go tests compile
// (pkg/tcc/taubyte/v2/fixtures/basic), pulled in raw at build time. One example,
// asserted by tests and rendered here — not a copy that drifts.
const raw = import.meta.glob("../../fixtures/basic/**/*.yaml", {
  query: "?raw",
  import: "default",
  eager: true,
}) as Record<string, string>;

const PREFIX = "../../fixtures/basic/";
const files: Record<string, string> = {};
for (const [path, body] of Object.entries(raw)) {
  files[path.slice(path.indexOf(PREFIX) + PREFIX.length)] = body;
}

let verbs: KindVerbs[] = [];
let kinds: Kind[] = [];
let omissions: Omission[] = [];
let graph: Graph | null = null;
let lastResult: CompileOk | { error: string } | null = null;
// dot runs asynchronously (it is wasm), so what is drawn is the last layout that
// finished. placedFor pins it to the graph it was computed from: positions from a
// previous compile applied to new nodes is worse than one frame of nothing.
let placed: Placed | null = null;
let placedFor: Graph | null = null;
let layoutSeq = 0;
let selection: Selection = null;
let current = Object.keys(files).sort()[0];
let tab: "graph" | "review" | "compiled" | "lowered" | "vocabulary" | "omitted" = "graph";

const app = document.getElementById("app")!;

async function main() {
  onEditorChange((text) => {
    files[current] = text;
    render();
  });
  const tcc = await loadTcc();
  verbs = tcc.verbs();
  kinds = tcc.kinds();
  omissions = tcc.omissions();
  setCategories(kinds);
  // What this vocabulary's shapes are — which block hands traffic on, which fields name
  // another resource — so the graph draws a vocabulary it has never seen.
  setShapes(window.tccV2!.shapes());
  render();
}

function compile(): CompileOk | { error: string } {
  return window.tccV2!.compile(files, { branch: "main" });
}

// Which lowered resources are showing what they BECOME. A box flips; the state is the
// page's, not the config's, so it survives a recompile and never reaches tcc.
const flipped = new Set<string>();

window.addEventListener("tcc-flip", (e) => flipFrame((e as CustomEvent<string>).detail));
window.addEventListener("tcc-pick", (e) => pickNode((e as CustomEvent<string>).detail));

// Picking a node from anywhere: the graph, a frame's label, a finding. One path, so
// what happens when you click a resource does not depend on which view you clicked it
// in.
export function pickNode(id: string): void {
  selection = { id, mode: "blast" };
  const node = graph?.byId.get(id);
  const made = loweredById(id);
  if (made) {
    generated = { title: `${made.kind}/${made.name}.yaml`, body: made.yaml ?? "" };
  } else if (node) {
    generated = null;
    const path = fileOf(node);
    if (path in files) current = path;
  }
  render();
  reveal(".files .file.on");
}

// What is being shown in the editor pane when it is NOT one of the project's files: a
// resource nobody wrote, rendered by tcc. Cleared the moment a real file is picked.
let generated: { title: string; body: string } | null = null;

export function flipFrame(id: string) {
  if (flipped.has(id)) flipped.delete(id);
  else flipped.add(id);
  render();
}

function render() {
  lastResult = compile();
  generatedFiles = null; // the config changed, so what it produces did too
  if (!("error" in lastResult)) {
    // WHICH SCOPES THERE ARE AND WHAT KIND EACH IS, before the graph is built: a frame's
    // label and a document's path both need it, and both used to say `namespace` because
    // that was the only kind of scope there was.
    setScopes(lastResult.scopes ?? []);
    graph = buildGraph(
      lastResult.resources ?? [],
      lastResult.relations ?? [],
      lastResult.names ?? {},
      lastResult.lowered ?? { produced: {}, granted: [], resources: [], relations: [] },
      flipped,
    );
    const g = graph;
    const seq = ++layoutSeq;
    // A keystroke while a layout is in flight makes that layout stale; the counter is
    // what stops it landing after the one it lost to.
    // internet -> zone -> resource. The zone is a real resource when the name lives in
    // one the project declares, and the cloud's own otherwise. Presentation only: none
    // of it is in the model.
    // fqdn alone is the filter: it comes from tcc's ReachableNames, which a zone never
    // populates for itself (it states its fqdn directly, it doesn't resolve one).
    const ingress = g.nodes
      .filter((n) => n.fqdn)
      .map((n) => ({ zone: n.answeredById || CLOUD_ZONE, to: n.id, name: n.fqdn! }));
    place(g.nodes, g.arrows, ingress, g.frames).then((p) => {
      if (seq !== layoutSeq) return;
      placed = p;
      placedFor = g;
      paint();
    });
  }
  paint();
}

// paint rebuilds the DOM from whatever is currently known. It runs on every compile and
// again when a layout resolves — so everything the user was doing lives in an element
// that is kept and re-attached rather than rebuilt: the editor, the graph host, and the
// React root inside it.
function paint() {
  const result = lastResult!;
  const ok = !("error" in result);

  app.innerHTML = "";
  app.appendChild(header(ok ? null : (result as { error: string }).error));

  const cols = div("cols");
  cols.appendChild(filesPane());
  cols.appendChild(editorPane());
  cols.appendChild(mainPane(result));
  app.appendChild(cols);

}

function header(error: string | null): HTMLElement {
  const h = div("head");
  h.innerHTML = `
    <div class="title">
      <h1>tau v2 — the DSL</h1>
      <p>The config on the left is compiled in this tab by tcc itself, built to
      WebAssembly from <code>pkg/tcc/taubyte/v2/schema</code>. Edit anything and the
      graph, the compiled object and the errors all move with it.</p>
    </div>`;
  const tabs = div("tabs");
  for (const [id, label] of [
    ["graph", "Graph"],
    ["review", "Review"],
    ["compiled", "Compiled object"],
    ["lowered", "Lowered filesystem"],
    ["vocabulary", "Vocabulary & verbs"],
    ["omitted", "What is not here"],
  ] as const) {
    const b = document.createElement("button");
    b.textContent = label;
    b.className = tab === id ? "on" : "";
    b.onclick = () => {
      tab = id;
      render();
    };
    tabs.appendChild(b);
  }
  h.appendChild(tabs);
  if (error) {
    const e = div("err");
    e.innerHTML = `<b>compile failed</b><pre>${esc(error)}</pre>`;
    h.appendChild(e);
  }
  return h;
}

function filesPane(): HTMLElement {
  const pane = div("pane files");
  const tree = div("tree");
  tree.appendChild(fileTree());
  pane.appendChild(sectionTitle("the config tree", `${Object.keys(files).length} files`));
  pane.appendChild(tree);
  return pane;
}

// Its own column, so the file is on screen at the same time as the graph it produces.
// It used to sit under the tree, which meant scrolling past thirty-three filenames to
// read the one file you were editing.
function editorPane(): HTMLElement {
  const pane = div("pane editor-pane");
  if (generated) {
    pane.appendChild(sectionTitle(generated.title, "generated — not in your repository, and not editable"));
    pane.appendChild(editorHost());
    showFile("generated:" + generated.title, generated.body, true);
    return pane;
  }
  pane.appendChild(sectionTitle(current, "editable — compiles on every keystroke"));
  pane.appendChild(editorHost());
  showFile(current, files[current] ?? "", false);
  return pane;
}

// The resource a node stands for, when nobody wrote it. It is an ordinary compiled
// resource in the lowered project — there is no separate document for it, and there is
// no yaml anywhere in the compiler to show. What it IS is what it compiled to.
function loweredById(id: string) {
  if (!lastResult || "error" in lastResult) return undefined;
  const made = lastResult.lowered?.produced?.[id];
  if (!made) return undefined;
  return { kind: made.kind, name: made.name, yaml: generatedDocument(made.kind, made.name) };
}

// WHAT IT WOULD BE AS CONFIG comes from the DECOMPILER, which is the only thing in tcc
// that writes yaml — the compiler renders none, ever. Asked for once and kept, because it
// is a whole decompile: nothing needs it until a box is opened.
let generatedFiles: Record<string, string> | null = null;
function generatedDocument(kind: string, name: string): string {
  if (!generatedFiles) {
    const out = window.tccV2?.generated(files, { branch: "main" });
    generatedFiles = out && !("error" in out) ? out : {};
  }
  // Same path fileOf builds for a real resource — layout.resource from tcc, not a
  // guessed template — with no scope so it matches by suffix below regardless of where
  // the decompiler nested it.
  const suffix = fileOf({ kind, name, scope: "" });
  for (const [path, body] of Object.entries(generatedFiles)) {
    if (path === suffix || path.endsWith("/" + suffix)) return body;
  }
  return "";
}

// A real directory tree, because the layout IS part of the DSL: a resource is a file
// under its kind's directory, and a namespace is a directory with a meta.yaml and
// resource directories of its own.
//
// Generic over WHICH paths — the authored tree and the lowered filesystem below are the
// same shape of thing, read off two different path lists, so one tree builder draws both
// rather than a second implementation that could disagree with the first about a kind's
// icon or a directory's sort order.
interface Dir {
  dirs: Map<string, Dir>;
  files: [string, string][];
}
function buildDirTree(paths: string[]): Dir {
  const root: Dir = { dirs: new Map(), files: [] };
  for (const path of paths.slice().sort()) {
    const parts = path.split("/");
    let node = root;
    for (const seg of parts.slice(0, -1)) {
      if (!node.dirs.has(seg)) node.dirs.set(seg, { dirs: new Map(), files: [] });
      node = node.dirs.get(seg)!;
    }
    node.files.push([parts[parts.length - 1], path]);
  }
  return root;
}
function dirEl(d: Dir, current: string | null, onPick: (path: string) => void): HTMLElement {
  const wrap = document.createElement("div");
  for (const [name, child] of [...d.dirs].sort(([a], [b]) => a.localeCompare(b))) {
    const line = document.createElement("div");
    line.className = "dir";
    // A directory in this tree IS a kind — that is the layout rule — so it gets the
    // kind's declared icon. A namespace's own subdirectories are instances, not
    // kinds, and iconOf returns nothing for them.
    const icon = iconOf(name);
    line.innerHTML = icon
      ? `<span class="kindcell ${categoryOf(name)}">${kindIconMarkup(icon, 12)}</span>${esc(name)}/`
      : `${esc(name)}/`;
    wrap.appendChild(line);
    const kids = document.createElement("div");
    kids.className = "kids";
    kids.appendChild(dirEl(child, current, onPick));
    wrap.appendChild(kids);
  }
  for (const [name, path] of d.files) {
    const line = document.createElement("div");
    line.className = "file" + (path === current ? " on" : "");
    line.textContent = name;
    line.onclick = () => onPick(path);
    wrap.appendChild(line);
  }
  return wrap;
}
function fileTree(): HTMLElement {
  return dirEl(buildDirTree(Object.keys(files)), current, (path) => {
    current = path;
    generated = null; // back to a file you can edit
    // The tree and the graph are two views of one resource, so selecting in
    // either selects in both. A file with no node — a namespace's own document —
    // clears the selection rather than leaving one pointing somewhere else.
    const node = graph?.nodes.find((n) => fileOf(n) === path);
    selection = node ? { id: node.id, mode: "blast" } : null;
    render();
    // the graph highlights the pick itself; there is no scroll position to chase
  });
}

// WHICH lowered file is open. The page's, like `current` — it survives a recompile
// (the same path usually still exists) and resets only when it stops existing.
let loweredCurrent: string | null = null;

// THE LOWERED FILESYSTEM: what the second pass actually wrote, as files — not the
// compiled object's shape of them, the yaml. tcc renders none of this by hand; every
// byte here is what a real compile produced, so this tab is not the graph again in a
// different skin, it is proof the graph is drawing a lowering that is really there.
function loweredFsPane(result: CompileOk): HTMLElement {
  const wrap = div("lowered-cols");
  const paths = Object.keys(result.loweredFiles ?? {});
  if (!paths.length) {
    wrap.appendChild(
      sectionTitle("nothing lowered", "this project's config declares no lowering, or nothing in it lowers yet"),
    );
    return wrap;
  }
  if (!loweredCurrent || !paths.includes(loweredCurrent)) loweredCurrent = paths.sort()[0];
  const picked = loweredCurrent;

  const treeCol = div("pane lowered-tree");
  treeCol.appendChild(sectionTitle("the lowered filesystem", `${paths.length} files — what compiling actually wrote`));
  const tree = div("tree");
  tree.appendChild(
    dirEl(buildDirTree(paths), picked, (path) => {
      loweredCurrent = path;
      render();
    }),
  );
  treeCol.appendChild(tree);
  wrap.appendChild(treeCol);

  const viewCol = div("pane lowered-view");
  viewCol.appendChild(sectionTitle(picked, "lowered — not in your repository, and not editable"));
  const pre = document.createElement("pre");
  pre.className = "lowered-yaml";
  pre.textContent = result.loweredFiles?.[picked] ?? "";
  viewCol.appendChild(pre);
  wrap.appendChild(viewCol);

  return wrap;
}

function mainPane(result: CompileOk | { error: string }): HTMLElement {
  const pane = div("pane main");
  if (tab === "vocabulary") {
    pane.appendChild(vocabulary());
    return pane;
  }
  if (tab === "omitted") {
    pane.appendChild(omitted());
    return pane;
  }
  if ("error" in result) {
    pane.appendChild(sectionTitle("nothing to show", "fix the error above"));
    return pane;
  }
  if (tab === "review") {
    pane.appendChild(reviewPane(result as CompileOk));
    return pane;
  }
  if (tab === "lowered") {
    pane.appendChild(loweredFsPane(result as CompileOk));
    return pane;
  }
  if (tab === "compiled") {
    pane.appendChild(
      sectionTitle(
        "the compiled object",
        "a nested map, shown as JSON. On the wire it is CBOR in TNS — never YAML.",
      ),
    );
    pane.appendChild(jsonRoot(result.object, "project", 3));
    if (result.validations?.length) {
      pane.appendChild(
        sectionTitle("deferred validations", "checks the compiler cannot answer alone"),
      );
      pane.appendChild(jsonRoot(result.validations, "validations", 3));
    }
    return pane;
  }
  pane.appendChild(graphPane());
  return pane;
}

function graphPane(): HTMLElement {
  const wrap = div("graphwrap");
  wrap.appendChild(
    sectionTitle(
      "the graph the config already is",
      "an arrow points the way the action goes - which is not always where it is declared",
    ),
  );
  wrap.appendChild(legend());

  // The same host element every time, so the React root — and with it the viewport
  // the user panned to — survives a DOM rebuild.
  wrap.appendChild(graphHost());
  if (graph && placed && placedFor === graph) {
    renderGraph(
      graph,
      placed,
      selection,
      (id) => {
      selection =
        id === null
          ? null
          : { id, mode: selection?.id === id && selection.mode === "blast" ? "reach" : "blast" };
      // Picking a node opens its file. The graph and the tree are two views of one
      // thing, and a node whose source you cannot read is half an answer.
      const node = id === null ? null : graph!.byId.get(id);
      if (node) {
        // A DERIVED node has no file: nobody wrote it. What it does have is the
        // document it would be, which tcc renders — so the pane shows that, read-only,
        // rather than leaving a click with no answer.
        const made = id === null ? undefined : loweredById(id);
        if (made) {
          generated = { title: `${made.kind}/${made.name}.yaml`, body: made.yaml ?? "" };
        } else {
          generated = null;
          const path = fileOf(node);
          if (path in files) current = path;
        }
      }
      render();
      reveal(".files .file.on");
    },
      flipped,
    );
  }
  wrap.appendChild(detail());
  return wrap;
}

// Both panes scroll and both are longer than their viewport, so a selection made in
// one is often off-screen in the other — and a selection you cannot see reads as
// nothing having happened. Each direction reveals the OTHER pane: the one you clicked
// in is already where you are looking. "nearest" so an already-visible target does not
// move at all.
function reveal(sel: string): void {
  document.querySelector(sel)?.scrollIntoView({ block: "nearest", inline: "nearest", behavior: "smooth" });
}

function legend(): HTMLElement {
  const l = div("legend");
  l.innerHTML = `
    <span><i class="sw read"></i>read</span>
    <span><i class="sw write"></i>write</span>
    <span><i class="sw ref"></i>reference</span>
    <span><i class="sw policy"></i>a function it names</span>
    <span class="hint">click a resource for its blast radius · click it again for what can reach it</span>`;
  return l;
}

function detail(): HTMLElement {
  const d = div("detail");
  if (!selection || !graph) {
    // What is reachable from outside is: every resource that compiled a name, plus
    // whatever attaches to it. A traversal, not a scan of every file.
    const named = graph ? graph.nodes.filter((n) => n.fqdn) : [];
    d.innerHTML = `<p class="hint">${
      named.length
        ? named
            .map((n) => {
              // what a name reaches is what it hands requests to, which is an
              // arrow OUT of it — the gateway is the tail of its own route
              const serves = graph!.arrows
                .filter((a) => a.from === n.id && !a.ref)
                .map((a) => graph!.byId.get(a.to)!)
                .map((t) => `<code>${esc(t.kind)}/${esc(t.name)}</code>${t.scope ? ` <span class="c">in ${esc(t.scope)}</span>` : ""}`);
              // whatever the kind derives — an SRV name, anything added later — shown
              // by its own label rather than by a field this file knows about
              const computed = (n.computed ?? [])
                .map((c) => `${esc(c.label)} ${esc(c.value)}`)
                .join(" · ");
              return `<b>${esc(n.fqdn!)}</b>${
                computed ? ` <span class="c">${computed}</span>` : ""
              } — <code>${esc(n.kind)}/${esc(n.name)}</code>${
                serves.length ? ` serves ${serves.join(", ")}` : " serves nothing yet"
              }.`;
            })
            .join("<br>")
        : "Nothing compiled an fqdn, so nothing answers from the internet."
    }</p>`;
    return d;
  }
  // NOT EVERY NODE ON THE CANVAS IS A RESOURCE. The internet and the cloud's own zone
  // are drawn rather than compiled — they are in the path, so a picture without them
  // makes a generated name look like it resolves by magic — and neither is in the
  // model. This used to dereference the lookup straight away, so clicking either one
  // threw before `cols` was ever appended and the whole page went blank.
  const n = graph.byId.get(selection.id);
  if (!n) return ingressDetail(d, selection.id);
  // A resource drawn beside each of its users has one box per user, and each box holds
  // one of the arrows. The panel is about the RESOURCE, so it asks what a box IS: filter
  // by n.id and a secret six things read would report one.
  const of = (id: string) => graph!.byId.get(id)?.of ?? id;
  const outs = graph.arrows.filter((a) => of(a.from) === n.of);
  const ins = graph.arrows.filter((a) => of(a.to) === n.of);
  const line = (a: { verb: string; effect: string }, other: string) =>
    `<li><span class="verb ${a.effect}">${esc(a.verb)}</span> ${esc(other)}</li>`;
  // THE COLUMNS ARE THE ARROW, NOT OWNERSHIP. An edge points the way the action goes,
  // which is not always away from the resource that declared it: a queue delivers to its
  // worker, and a secret delivers its value to whoever named it. Headed "uses"/"used by"
  // the panel read those backwards and called a secret six things read unreachable —
  // the view contradicting the direction the vocabulary states. "reaches"/"reached by"
  // is the wording the legend and the hint below already use, and it stays true
  // whichever way an edge runs.
  d.innerHTML = `
    <h3>${esc(n.kind)}/${esc(n.name)}${n.scope ? ` <span class="c">in ${esc(n.scope)}</span>` : ""}${
      n.copies ? ` <span class="c">one resource, ${n.copies} boxes</span>` : ""
    }</h3>
    <div class="two">
      <div><h4>reaches</h4><ul>${
        outs.map((a) => line(a, `${graph!.byId.get(a.to)!.kind}/${graph!.byId.get(a.to)!.name}`)).join("") ||
        "<li class='hint'>nothing</li>"
      }</ul></div>
      <div><h4>reached by</h4><ul>${
        ins.map((a) => line(a, `${graph!.byId.get(a.from)!.kind}/${graph!.byId.get(a.from)!.name}`)).join("") ||
        "<li class='hint'>nothing</li>"
      }</ul></div>
    </div>
    <p class="hint">showing ${selection.mode === "blast" ? "what this reaches" : "what reaches this"}</p>`;
  return d;
}

// WHAT A NODE THAT IS NOT A RESOURCE IS. The internet and the cloud's own zone are on
// the canvas because they are steps in the path a request takes, not because anything
// in the project declares them — so there is no file to open and no blast radius to
// walk. What they DO have is the set of names that pass through them, which is the
// question anyone clicking them is asking, and it comes straight off the graph.
function ingressDetail(d: HTMLElement, id: string): HTMLElement {
  // Anything else that is not in the model at all: say so plainly rather than dressing
  // it up as one of the two nodes this pane knows how to explain.
  if (id !== CLOUD_ZONE && id !== INTERNET) {
    d.innerHTML = `<p class="hint">Nothing on the graph answers to that any more.</p>`;
    return d;
  }
  const named = [...new Map((graph?.nodes ?? []).map((n) => [n.of, n])).values()].filter((n) => n.fqdn);
  // Which zone answers a name is `answeredById`, or the cloud's own when nothing in
  // the project answers — the same expression the ingress path is built from. The
  // internet is upstream of every one of them.
  const answers =
    id === INTERNET ? named : named.filter((n) => (n.answeredById || CLOUD_ZONE) === id);
  const what =
    id === CLOUD_ZONE
      ? {
          title: "the cloud's zone",
          why: `Not a resource in this project, and nothing here declares it: the cloud's own
                zone is <b>generated</b> for the cloud that runs the project, which is why it has
                no document to open. It is drawn because it is a real step in the path — a
                generated name resolves <i>here</i>, and a picture that skips it makes that
                name look like it works by magic.`,
          none: "Nothing here compiled a generated name, so the cloud's zone answers for nothing.",
        }
      : {
          title: "the internet",
          why: `Not a resource and never part of the model. It is drawn so that traffic from
                outside arrives from somewhere visible rather than from the edge of the canvas.`,
          none: "Nothing compiled an fqdn, so nothing answers from the internet.",
        };
  d.innerHTML = `
    <h3>${esc(what.title)} <span class="c">drawn, not compiled</span></h3>
    <p class="hint">${what.why}</p>
    <h4>${id === CLOUD_ZONE ? "answers for" : "reaches"}</h4>
    <ul>${
      answers
        .map(
          (n) =>
            `<li><code>${esc(n.fqdn!)}</code> <span class="c">${esc(n.kind)}/${esc(n.name)}</span></li>`,
        )
        .join("") || `<li class="hint">${esc(what.none)}</li>`
    }</ul>`;
  return d;
}

// The review: what the graph says, for the person signing off on it rather than the
// person writing it. Every row is derived from the compiled object, and clicking one
// selects that resource on the graph — a finding you cannot locate is half a finding.
function reviewPane(result: CompileOk): HTMLElement {
  const wrap = div("review");
  if (!graph) {
    wrap.appendChild(sectionTitle("nothing to review", "the project has not compiled yet"));
    return wrap;
  }
  // Decided by tcc, rendered here. The page used to recompute these in TypeScript,
  // which was the same reasoning written twice and would have drifted the first time a
  // kind changed.
  const findings = result.findings ?? [];
  // ONE ROW PER RESOURCE, NOT PER BOX. A much-reached sink is drawn beside each of its
  // users, so `graph.nodes` holds six boxes for one secret — and a pane that counts
  // boxes reports six secrets where the project declares one. Every reader here asks
  // what a box IS (`of`), which is the same rule the detail panel and the findings
  // already follow.
  const resources = [...new Map(graph.nodes.map((n) => [n.of, n])).values()];
  const exposure = resources
    // fqdn alone is the filter — see the ingress build in render(). A zone never gets
    // one: it is the column here (zone: below), not a row.
    .filter((n) => n.fqdn)
    .map((n) => ({
      id: n.id,
      kind: n.kind,
      name: n.name,
      at: n.fqdn!,
      zone: n.answeredBy ?? "the cloud's own",
      serves: graph!.arrows
        .filter((a) => a.from === n.id && a.handoff)
        .map((a) => graph!.byId.get(a.to))
        .filter((x): x is NonNullable<typeof x> => !!x)
        .map((t) => `${t.kind}/${t.name}${t.scope ? ` in ${t.scope}` : ""}`),
    }));
  const counts = [...resources.reduce((m, n) => m.set(n.kind, (m.get(n.kind) ?? 0) + 1), new Map<string, number>())]
    .map(([kind, n]) => ({ kind, n }))
    .sort((a, b) => b.n - a.n || a.kind.localeCompare(b.kind));

  const broken = findings.filter((f) => f.severity === "broken").length;
  const checks = findings.filter((f) => f.severity === "check").length;
  wrap.appendChild(
    sectionTitle(
      "what the graph says",
      broken
        ? `${broken} that will not work, ${checks} to look at`
        : checks
        ? `nothing broken, ${checks} to look at`
        : "nothing broken, nothing to look at",
    ),
  );

  // None of this is enforced anywhere, and saying so is the difference between a
  // review tool and a false sense of one.
  const caveat = div("hint");
  caveat.innerHTML =
    "Decided by tcc, in <code>schema/checks.go</code> — the same checks any caller gets from <code>schema.Audit</code>. <b>Nothing refuses a deploy on them yet</b>: what blocks and what warns is a policy decision, not this pane's.";
  wrap.appendChild(caveat);

  wrap.appendChild(exposureTable(exposure));

  const list = div("findings");
  for (const f of findings) list.appendChild(findingRow(f));
  wrap.appendChild(list);

  wrap.appendChild(sectionTitle("inventory", `${resources.length} resources`));
  const inv = div("inventory");
  inv.innerHTML = counts
    .map(
      (c) =>
        `<span class="chip ${categoryOf(c.kind)}">${kindIconMarkup(iconOf(c.kind), 12)}${esc(c.kind)}<b>${c.n}</b></span>`,
    )
    .join("");
  wrap.appendChild(inv);
  return wrap;
}

function exposureTable(rows: { id: string; kind: string; name: string; at: string; zone: string; serves: string[] }[]): HTMLElement {
  const wrap = div("");
  wrap.appendChild(
    sectionTitle("reachable from the internet", rows.length ? `${rows.length} name${rows.length === 1 ? "" : "s"}` : "nothing answers"),
  );
  if (!rows.length) return wrap;
  const t = document.createElement("table");
  t.innerHTML =
    "<thead><tr><th>name</th><th>answered by</th><th>served by</th><th>which fronts</th></tr></thead><tbody>" +
    rows
      .map(
        (e) =>
          `<tr data-id="${esc(e.id)}"><td><code>${esc(e.at)}</code></td><td><code>${esc(e.zone)}</code></td><td><code>${esc(e.kind)}/${esc(e.name)}</code></td><td>${
            e.serves.length ? esc(e.serves.join(", ")) : '<span class="c">nothing yet</span>'
          }</td></tr>`,
      )
      .join("") +
    "</tbody></table>";
  t.onclick = (ev) => {
    const row = (ev.target as HTMLElement).closest("tr[data-id]");
    if (row) pickFromReview(row.getAttribute("data-id")!);
  };
  wrap.appendChild(t);
  return wrap;
}

function findingRow(f: Finding): HTMLElement {
  const row = div(`finding ${f.severity}`);
  const where = `${f.kind}/${f.name}${f.scope ? ` in ${f.scope}` : ""}`;
  row.innerHTML =
    `<span class="sev">${f.severity}</span>` +
    `<div><b><code>${esc(where)}</code> ${esc(f.title)}</b><p>${esc(f.detail)}</p></div>`;
  // f.id is the compiled resource this finding is about (empty when there is none to
  // point at). A much-reached sink is drawn once per user, and every copy carries the
  // same `of` — so this matches on `of`, not `id`, the way every other node lookup here
  // that starts from a real resource id does (see the `of` helpers in graph-view.tsx).
  const node = f.id ? graph?.nodes.find((n) => n.of === f.id) : undefined;
  if (node) {
    row.classList.add("clickable");
    row.onclick = () => pickFromReview(node.id);
  }
  return row;
}

// A finding names a resource; clicking it puts that resource on the graph with its
// blast radius, which is the next question anyone asks about a finding.
function pickFromReview(id: string): void {
  const node = graph?.byId.get(id);
  if (!node) return;
  selection = { id, mode: "blast" };
  const path = fileOf(node);
  if (path in files) current = path;
  tab = "graph";
  render();
  reveal(".files .file.on");
}

function vocabulary(): HTMLElement {
  const wrap = div("vocab");
  wrap.appendChild(sectionTitle("the vocabulary", `${kinds.length} kinds, read off the schema`));
  const t = document.createElement("table");
  t.innerHTML =
    "<thead><tr><th>kind</th><th>what it is</th><th>its own fields</th></tr></thead><tbody>" +
    kinds
      .map(
        (k) =>
          `<tr><td><span class="kindcell ${categoryOf(k.group)}">${kindIconMarkup(k.icon, 14)}<code class="${categoryOf(k.group)}">${esc(k.group)}</code></span>${
            k.container ? ' <span class="tag">scope</span>' : ""
          }</td><td>${esc(k.doc)}</td><td class="fields">${esc(k.fields.join(", "))}</td></tr>`,
      )
      .join("") +
    "</tbody></table>";
  wrap.appendChild(t);

  wrap.appendChild(
    sectionTitle("verbs and effect classes", "what a resource may ask to do to each kind. Only kinds something can reach are here \u2014 nothing uses a cron or a zone, and that absence is the statement"),
  );
  const v = document.createElement("table");
  v.innerHTML =
    "<thead><tr><th>kind</th><th>verbs</th></tr></thead><tbody>" +
    [...verbs]
      .sort((a, b) => a.kind.localeCompare(b.kind))
      .map(
        (kv) =>
          `<tr><td><code>${esc(kv.kind)}</code></td><td>${kv.verbs
            .map((x) => `<span class="verb ${x.effect}">${esc(x.name)}</span>`)
            .join(" ")}</td></tr>`,
      )
      .join("") +
    "</tbody></table>";
  wrap.appendChild(v);
  return wrap;
}

// A closed vocabulary is only honest if you can see what it closed out. Every entry
// here is declared next to the kinds, so "why is there no X" has an answer in the
// same place the kinds do — and an answer that cannot quietly go stale.
function omitted(): HTMLElement {
  const wrap = div("vocab");
  const label: Record<string, string> = {
    never: "refused",
    blocked: "nothing to author yet",
    open: "owner decision",
  };
  for (const state of ["never", "blocked", "open"] as const) {
    const rows = omissions.filter((o) => o.state === state);
    if (!rows.length) continue;
    wrap.appendChild(sectionTitle(label[state], `${rows.length}`));
    const t = document.createElement("table");
    t.innerHTML =
      `<thead><tr><th>not a kind</th><th>why</th><th>${
        state === "never" ? "instead" : "instead / what unblocks it"
      }</th></tr></thead><tbody>` +
      rows
        .map(
          (o) =>
            `<tr><td><code class="om ${o.state}">${esc(o.name)}</code></td><td>${esc(o.why)}</td><td>${
              [
                o.instead ? `<b>use</b> ${esc(o.instead)}` : "",
                o.unblocks ? `<b>needs</b> ${esc(o.unblocks)}` : "",
              ]
                .filter(Boolean)
                .join("<br>") || "&mdash;"
            }</td></tr>`,
        )
        .join("") +
      "</tbody></table>";
    wrap.appendChild(t);
  }
  return wrap;
}

function div(cls: string): HTMLElement {
  const d = document.createElement("div");
  d.className = cls;
  return d;
}
function sectionTitle(title: string, sub = ""): HTMLElement {
  const h = div("st");
  h.innerHTML = `<h2>${esc(title)}</h2>${sub ? `<span>${esc(sub)}</span>` : ""}`;
  return h;
}

main().catch((e) => {
  app.innerHTML = `<div class="err"><b>failed to start</b><pre>${esc(String(e))}</pre>
  <p>Run <code>npm run build:wasm</code> first, or use <code>npm run dev</code> which does it for you.</p></div>`;
});
