# Working on the v2 DSL

Everything here was learned the expensive way — by getting it wrong against a reviewer
who wrote tcc. Read it before touching `schema/`.

Line references are to `pkg/tcc/` at the time of writing. If one is stale, re-derive
it rather than trusting the prose.

---

## 0. The rule that matters most

**Do not modify `pkg/tcc/`.** Not `engine/`, not `interp/`, not `object/`. The DSL is
rich enough; if something seems impossible, you are using it wrong. Every time this
looked like an engine limitation it turned out to be a construct not yet found.

When it really is a missing primitive — say so, name the SHAPE the generic layer is
missing, and add it in the generic layer's own language. `Lowerable`, `LoweredRoot` and
`ContainerKeys` are what that looks like: none of them knows a kind, each replaced a
"there is exactly one of these" assumption rather than carving out a special case. A term
in `engine` or `interp` that names a v2 kind is the thing this rule is really against.

`tools/tcc-gen` is the exception — it carries `--dsl v1|v2` to select the vocabulary.

**Read `taubyte/v1/schema/definition.go` first.** It is a complete worked example of
every construct. `functions` alone answers most questions.

---

## 1. Where things live

| Authored | Loaded by |
|---|---|
| `function/checkout.yaml` — a resource, one flat file, `id` at the document root | `DefineGroup("functions", DefineIter(attrs…))` |
| `namespace/shop/meta.yaml` + `namespace/shop/functions/*.yaml` — a scope | `DefineGroup("namespace", DefineIterGroup(attrs, groups…).With(Leaf("meta")))` |
| `clouds:` as a map inside the project's `project.yaml` | `DefineGroup("clouds", DefineIter(attrs…))` |

A **resource** (`DefineIter`, `Group == false`) reads its attributes from the
document root and **never walks children** — `engine/node.go:478-483` returns before
the child walk at `:529`. Passing a `*Node` to `DefineIter` compiles and is silently
dropped.

A **container** (`DefineIterGroup`, `Group == true`) walks children, but reads its own
attributes from its leaf (`node.go:486`). In a directory that is
the document the group NAMES with `Leaf(...)` — `meta.yaml` in v2, `config.yaml` in v1,
and nothing at all when a group names none; in a flat document it is a literal key by that
name. This is why a
container instance cannot be a flat `shop.yaml` with `id` at the root — the resulting
error is `required attribute 'id'`, and that error is **correct**.

A **nested resource** (`DefineIter` + `Nested(groups…)`) walks its children against
its own document AND stays a resource: scalars parsed, wire keys applied, indexed by
name, no `config` leaf. This is what an access block uses. It was added to the engine
on 2026-08-16 under `ENGINE-UPGRADE.md`, with v1 output proven byte-identical.

---

## 2. Nesting inside one file is attribute paths

This is the answer to "how do I put a block inside a resource". `functions` carries its
whole `trigger:` block this way:

```go
String("type",        Path("trigger", "type"), …)
StringSlice("paths",  Path("trigger", "paths"), …)
StringSlice("domains", Path("trigger", "domains"), Ref("domains"), …)
Duration("timeout",   Path("execution", "timeout"), …)
String("fullname",    Path("source", "github", "fullname"), …)
```

Arbitrarily deep, fixed keys. Reach for this before anything else.

---

## 2a. Repeated objects: named entries, or an ordered list

Two shapes, and the difference is whether an entry has a NAME or a POSITION.

```go
Nested(DefineGroup("variables", DefineIter(attrs)))   // keyed by name
Nested(DefineList("transformers", attrs))             // ordered by position
```

```yaml
variables:                      transformers:
    LOG_LEVEL: {value: info}        - name: redact
    REGION:    {value: eu-west}     - namespace: /shop
                                      name: tier_header
```

Prefer the NAMED form. A name is a handle — it can be referred to, diffed, and
merged; an index is none of those, and in a CRDT two edits to "element 3" are two
edits to different things. Reach for `DefineList` when the entries are a PIPELINE,
where running them in another order means something else. Ordering written by hand
inside a mapping (`order: 1`) is the tell that the shape is wrong.

Inside an element, everything the ENGINE does works: types, `Required()`,
`Default()`, `InSet`, validators, unknown-key rejection, per-field error locations.
What does NOT work is anything the INTERPRETER runs per object instance — `Ref()`,
`Duration()`/`Bytes()` scalar parsing, `Tag()` renames, `EnumBool`, `DerivedBool`,
`OnlyWhen` — because a list compiles to one ordered value rather than to children.
`DefineList` panics at construction if an attribute declares one, so this is a build
failure and never a quietly mis-compiled field. Name another resource from inside an
element the way every cross-namespace reference does — a `namespace` and a `name`
(§5) — not a `Ref()`.

---

## 3. The conditional switch: `Either(...)` + `Key()`

A closed set of alternative keys, with `Key()` capturing which branch was taken and
the branch's own fields hanging off the same `Either`:

```go
String("handler",           Path("route","handler", Either(handlerKinds...)),              Key(), …)
String("handler-namespace", Path("route","handler", Either(handlerKinds...), "namespace"), …)
String("handler-name",      Path("route","handler", Either(handlerKinds...), "name"), Required(), …)
```

```yaml
route:
    handler:
        function:              # the switch
            namespace: /shop/eu
            name: notify
```

Same construct as v1's `storages.type` (`Path(Either("object","streaming"))`, `Key()`)
and `git-provider` (`Path("source", Either("github"))`, `Key()`).

A key outside the set fails to compile with `attribute handler has an empty key` —
`inferPathQuery` finds no match, so the switch captures nothing. That is how you keep
an unroutable target from becoming a rule that silently never fires. Prefer this over
one boolean/enum plus five parallel fields.

A switch may sit on an OPTIONAL block (fixed 2026-08-18). An absent block leaves the
key unset; a block that IS there but names something outside the set is still
`attribute X has an empty key`. The two are told apart by whether the parent exists, so
a switch whose `Either()` is at the top level — its parent being the document — keeps
the old behaviour and always demands a branch.

Related conditionals: `RequiredWhen`, `RequiredUnless`, `OnlyWhen`, `ShowWhen`,
`SectionWhen`, `DerivedBool`, `EnumBool`.

---

## 4. A key may never contain `/`

The compiled configuration is an OBJECT, and `/` is what separates one level of it from
the next — in a TNS path, in a lowering's `At(path…)`, in every walk over the tree. So a
key holding a slash is a key pretending to be a path: it reads as hierarchy everywhere it
is printed and is one opaque leaf everywhere it is stored, which for a CRDT means two
writers touching different halves of it collide over one cell.

```go
String("execution-call", Path("execution", "call"))   // nesting is DECLARED
String("execution/call")                              // panics
Tag("route/handler/namespace")                        // panics
```

**This is what `Path(...)` is for.** Hierarchy is segments the engine builds levels from;
it is never a key that spells its own path. Same for anything else that is not a name — a
dot, a space, a leading digit. A key is a variable name, `-` allowed.

`engine/keys.go` refuses all of it at construction — attribute names, `Tag`, `DefineGroup`,
`DefineList`, `Leaf` — the way `DefineList` already refuses a term that cannot run inside an
element. A schema that cannot express a legal object is a build failure, not a config that
compiles into a shape nothing can merge.

`Path(...)` is where the value is **read from**, `Tag(...)` is where it is **written
to**. Two independent knobs; don't collapse them.

### A mutual exclusion is `Either()`, never a check

A gateway declares a `router` — one function that routes everything, and then there are
no static routes at all — OR a `route`, which itself names a `handler` or hands the
decision to a router of its own. Same for a proxy with `forward`.

None of that is an audit rule. It is the switch the vocabulary already has:

```go
String(Key("routing"), Path(Either("route", "router")), Captures(), Required())
String(Key("route", "target"), Path("route", Either("handler", "router")), Captures())
```

`inferPathQuery` refuses a document that opens two branches — *"ambiguous path: route and
router each match, and only one may be set"*, with a line number — and `Captures()` on a
`Required()` switch refuses one that opens none. "Exactly one" is a property of the
DECLARATION; there is nothing left to check afterwards, and I wrote a `checkRouting`
before remembering that.

The inner switch is NOT `Required()`: a switch inside an optional block is demanded only
when the block is there, which `parentExists` already answers. And a field under one
branch is `RequiredWhen` the switch names that branch — `route.path` is required when the
routing is a route, and meaningless when a router routes everything.

**`Serves()` says an edge is a handoff.** It used to be inferred from `KindFrom` being
set — the author picking the kind stood in for "traffic goes here" — which a router
breaks: its kind is fixed and it is still where the request goes.

### A reference is a BLOCK, and `{kind}/{name}` is never written

```yaml
model:                       source:                    dead-letter:
    namespace: /shop             module: paylib             name: orders_dead
    name: opus
```

Three facts, three places. The KIND is on the `Ref` and never repeated in the value — a
second copy is free to disagree the moment a group is renamed. The NAMESPACE is authored
beside the name, which `model/opus` had nowhere to put, so it silently meant "somewhere up
from here". And the compiled value is **the id alone**: ids are unique, so nothing else is
needed to find the resource.

`Prefix(...)` is gone from v2. It was always `group + "/"` — the same fact a third time —
and single-versus-list is the attribute's own type (`String` vs `StringSlice`). v1 keeps its
composite through `FlatValue()`, because its consumers read `libraries/<id>` off the
compiled object and the OLD compiler built it that way long before tcc.

This is also why `agent.model` is no longer destroyed. `model` is a kind AND an attribute,
so the attribute's block and the access group generated for the kind land at one key —
and they coexist, because **a key an attribute declares is not an entry of the group that
shares its name** (`engine/node.go`, `claimedUnder`), and a second declaration arriving at
a key MERGES rather than replacing it.

Resource names are variable names: `orders-dead` is not one.

### `Tag(x)` on an attribute already named `x` deletes the field

`applyInstanceOps` step 5 does `sel.Move(a.Name, tag)`, and `Move` is
`data[to] = data[from]; delete(data, from)` (`object/generic.go:216-224`). Same name in
and out ⇒ the field is set and then deleted, silently. Only tag when the name differs.

---

### Kind names are SINGULAR

`function/checkout.yaml`, `cache: { orders: … }` — the identifier names the KIND, not the
collection. It reads "a function named checkout", and it is one rule with no exceptions:
`messaging` and `mcp` could never be pluralised, `storages` was not a word anyone says,
and `ServedByRoute` carried a `k+"s"` to bridge singular handler kinds to plural group
names. That hack is gone.

A consequence worth knowing: a kind name may no longer collide with a resource's own
block. `cache` used to keep its capacity under `storage:` while the access group for
storages was `storages:`; singular made both `storage:`. The cache's fields are `size:`
and `ttl:` at the top level now. `/tmp`-style throwaway check for this lives in the
review notes — the rule is that no attribute path may START with a kind name.

---

## 5. Things that do not exist — stop looking

- **A wrapper for the access block.** A resource declares what it reaches as
  `database:` / `storage:` / `secret:` at the top level of its own document, the way
  it declares `source:` and `execution:`. There is no `uses:` (or `edges:`) key; it read
  as a section bolted onto the resource and it only ever existed because it was easier
  to generate.
- **A list-of-objects ATTRIBUTE.** Attribute types are
  `int | bool | float64 | string | []string` (`engine/types.go`) and that has not
  changed. A list of objects is a NODE, not a type: `DefineList("transformers", …)`
  (see §2a). Reaching for a new attribute type is still the wrong move.
- **A validated target NAME on a nested entry.** The entry's key is not a value, so
  nothing resolves or checks it — `Key()` reads the last resolved segment of the
  attribute's OWN path, never the key its object is filed under. Recorded in
  omissions.go.
- **Polymorphic refs.** `Ref(group)` binds exactly one group. A target that may be one
  of several kinds is either the `Either` switch of §3 (kind as a key) or one field per
  kind.
- **Recursive schema nodes.** Making a group a child of its own iterator produces a
  cyclic node graph, and `interp/shape.go`'s `eachIterIn` has no visited set — it
  recurses until the stack dies. Declare the depth instead
  (`namespacesGroup(MaxNamespaceDepth)`).
- ~~**More than one walked container.**~~ Fixed 2026-08-22, because a second one was
  needed: `ContainerKeys` (`interp/shape.go`) answers with EVERY top-level group whose
  iterator is a group — including a kind that is a resource AND a container, which is what
  a lowered `workflow` is — and `utils.Global` walks the project root plus all of them. It used to answer with the first, and a second scope
  compiled, indexed its own names and then resolved no references at all — which reads
  like the reference being wrong rather than like half the walk being missing.

### What NOT to do instead

Do not smuggle structure into strings. A route is not
`"POST /checkout function:/checkout"`, an edge is not `"kv/orders get,put"`. It buys a
`[]string` and gives up per-field validation, completion, JSON-Schema emission and
per-field error locations — four of the five reasons the DSL exists. If the shape does
not fit, say so and stop; do not invent a seventh encoding.

This is also why a cross-namespace reference is TWO fields and not one packed path.
`transformers` briefly took addresses — `redact`, `/shop/tier_header` — which is one
dimension in a string and defensible on its own, but it made the same reference look
different depending on where it appeared: a handler and an authorizer spell it
`namespace` + `name`. One notation, everywhere, is worth more than the shorter line.

(The one accepted exception is `zones.records`, which uses zone-file presentation
lines — an actual industry interchange format, not an invented grammar. The cost is
recorded in `omissions.go`.)

---

## 6. References

```go
StringSlice("domains", Ref("domains"), …)                    // []name → []id
String("source", Ref("modules", Prefix("modules/")), …)      // "modules/x" → "modules/<id>"
```

A **scalar** ref needs a `Prefix`; without one `resolveRefAttr` treats the value as a
`[]string` and fails with `<attr> is not a []string`. A slice ref takes bare names.
This one bites even after you have written it down — `Ref("connectors")` on a
`String` compiles fine and fails at runtime.

Names resolve in the authoring scope, then walk up to the project root
(`interp/utils/paths.go`). A sibling namespace's resource has no spelling, so it simply
does not resolve — that is the isolation, and the error is `not indexed`, which names
neither the field nor the file. Worth improving one day; do not paper over it.

---

## 7. Reading a failure

- `required attribute 'id'` on a resource whose `id` is plainly there ⇒ **the YAML did
  not parse**. Everything is missing, and the loader reports the first required field.
  Check the document, not the schema. A `- "*" /api …` list entry is the classic one: a
  quoted scalar followed by trailing text is invalid YAML.
- `required attribute 'id'` on a container ⇒ you are authoring a flat file where the
  schema expects `<name>/<leaf>.yaml` — `namespace.yaml` in v2. §1.
- A field you declared is missing from the compiled object, with no error ⇒ a
  self-referential `Tag`. §4.
- Children silently absent ⇒ you hung a group off a `DefineIter`. §1.
- `invalid type passed to validator`, pointing at a fixture line ⇒ a scalar-only
  option on a `StringSlice`. `InSet` is the usual one: its validator takes a `T`, so
  a `[]string` fails at LOAD, in the user's yaml, not at schema-build where the
  mistake is. Use `EachInSet` (verbs.go). Same family as `Ref` without `Prefix` on a
  `String`, §6.
- `unknown key 'x'; valid keys here: …` ⇒ exactly what it says, and the list is
  generated from the schema, so it is the truth rather than a doc comment. Check the
  spelling against the attribute's `Path(...)`. This used to be SILENT — the field was
  dropped and the compile succeeded — until `engine/strict.go` (2026-08-17); a fixture
  here said `source: true` where the attribute is `polls` and nothing complained for
  weeks. If you are adding an attribute and its key is rejected, the schema is what is
  wrong. It is opt-in per vocabulary (`Strict()` on the root): v2 has it, v1 does not,
  because v1 is frozen with projects deployed against it — so in v1 the key is still
  dropped in silence.
- A `RequiredWhen` naming a `Key()` sibling works (engine reads the matched branch,
  fixed 2026-08-18). A `ShowWhen` naming one is still evaluated by CONSUMERS, not the
  engine, so a UI has to do the same resolution.
- A `ShowWhen` / `RequiredWhen` that never fires ⇒ its first argument is not an
  attribute name. It takes the **sibling attribute's `Name`**, which for a nested field
  is the full slashed one — `ShowWhen("certificate/source", "provided")`, never
  `ShowWhen("certificate", ...)` after the block it appears to name. Nothing validates
  that the target exists, so a wrong name is a condition that is simply never true: the
  field is always hidden, or never required. Eight of these were wrong at once in the
  gateway before anybody noticed, because the compiled output looks identical.
- A defaulted field appearing where its own `ShowWhen` says it is irrelevant ⇒
  `Default()` fires unconditionally. `ShowWhen` is presentation only. If a value would be
  misleading in the compiled object when the branch is not taken, do not give it a
  default — use `RequiredWhen` on the same condition instead.

---

## 8. Verify against the compiler, never against reasoning

Write a throwaway `main.go` that builds a minimal `engine.Root(...)` with
`interp.New(...)` over an `afero.MemMapFs`, and print `obj.Flat()`. Brute-force the
combinations. Every conclusion above that was reached by reading code alone was wrong
at least once; every one reached by running the compiler held.

Regenerate after a schema change:

```
go run ./tools/tcc-gen --dsl v2 --wasm   # explorer/public/tcc.wasm
go run ./tools/tcc-gen --dsl v2 --ts     # explorer/src/gen/schema.ts
go test ./pkg/tcc/... ./tools/tcc-gen/   # v1's parity oracle must stay green
```

---

## 8a. Generated vs derived

Two different things, and the DSL has both:

```go
String("id",         Generated(GenCID))                 // MINTED: fresh entropy, never reproducible
String("zone-label", Derived(DeriveDNSLabel, "id"))     // COMPUTED: same input, same output, forever
```

Confusing them is a bug in either direction — a minted id that reproduced would collide,
a derived name that did not would resolve differently in two consumers. Neither has any
compile effect: both are schema/tooling annotations, and the value is filled in by the
consumer via `engine.Generate` / `engine.Derive`.

A derived field is never authored, so it never lands in the compiled object. Give it a
validator that rejects a non-empty value — otherwise the strict-key check accepts it
(the key IS in the schema) and a stale copy of a derived name sits in the document,
which is the drift deriving it was meant to remove.

`GeneratedBy` / `DerivedBy` address a field of a GROUP. Project-root attributes are not
reachable through them; read `CompileRoot().Attributes` for those.

---

## 8b. A kind that is REWRITTEN rather than run

Some kinds name nothing a component holds. A `workflow` is a way of writing queues; it
LOWERS into them at compile, so the vocabulary gains a construct and operations gains
nothing new to deploy. The mechanism is tcc's (`interp/lowering.go`, `view.go`); the
vocabulary declares what it becomes, in terms:

```go
Lowers(
    Into("queue",
        PerEach("steps"),
        Named(Concat(Attr("name"), Text("_"), Each("name"))),
        Sets("max-attempts", Each("attempts")),
        // who reaches it, and with which of the target kind's own verbs
        Reaches("receive", From("function", Each("run"), Each("namespace"))),
        Reaches("send",
            From("function", Each("run"), Each("namespace")),
            To("queue", Concat(Attr("name"), Text("_"), Next("name")))),
    ),
)
```

**A VALUE IS A TERM, NOT A TEMPLATE.** `Attr(f)` is a field of the resource being lowered,
`Each(f)` one of the element `PerEach` names, `Next(f)` one of the element after it,
`Text` is fixed text, `Scope()` is where the produced resources were put, and `Concat`
puts several in a row. They take the same key spellings everything else does, so a nested
field is `Attr(Key("memory","size"))` and the `/` between levels is the engine's to write
— §4 holds here too, which `"{memory/size}"` quietly did not.

Three things follow, and the third is why it was worth doing:

- the STRUCTURE of a reference cannot be mistyped — there is no brace to leave off and no
  `each.` prefix to misspell. A wrong FIELD name is still a string, and is still caught by
  `CheckLowerings` at build time, which is stronger than Go could be: it is checked against
  the attributes the kind actually declares.
- nothing is substituted. What a lowering produced — its kind and its name — is not a term
  and never was a value: a write that lands on it says so by naming no other target
  (`AtProduced`, which `Reaches` uses when `To(...)` is omitted), and the interpreter puts
  its own two strings in front of the path.
- **a composed value can be read BACKWARDS.** `"{name}_{each.name}"` produced
  `checkout_reserve` and could not be undone — recovering `reserve` meant guessing which
  underscore was the separator, so `reversal.go` declared composition one-way and every
  composed field stayed in the shell. `Concat(Attr("name"), Text("_"), Each("name"))` still
  knows which underscore it wrote: the parts around the composed one are filled in, their
  lengths are the cut, and `my_build_run_tests` gives back `run_tests` and not
  `build_run_tests`. Invertibility is a property the DSL states.

**A SOURCE BECOMES A GROUP, AND A GROUP IS A DIRECTORY THAT IS ALSO A SCOPE.** That is
the whole of it, and it is not a new idea: `namespace` is one, v1's `application` was one,
and a kind that LOWERS becomes one. `layout.go` already said the mechanism out loud — *"a
container's instances are directories, each with the document its `Leaf()` names; every
other resource is one file"* — so what a lowered `workflow` needs is that layout and
nothing else: its own fields in `workflow/checkout/meta.yaml`, the queues it produced in
`workflow/checkout/queue/*.yaml`.

**ONE KIND, TWO SHAPES, ONE PER PASS.** `Lowerable(before, after)` states both:

```go
DefineGroup("workflow", Lowerable(
    DefineIter(attrs, Lowers(Into("queue", …))),                       // authored: one flat file
    DefineIterGroup(attrs, resourceGroups...).With(Leaf("meta")),      // lowered: a directory
))
```

`Authored()` reads the before shape and so does every authoring consumer — the JSON
Schema, the session's kind list, completion. `interp.LoweredRoot` selects the after shape,
and `c.lower()` is the ONLY caller: the second pass writes the authored document into the
container's leaf and its produced children into the kind-named directories beside it, then
compiles that. The compiled resource is still `kind: workflow` at its own id. `KindRoot()`
is that root for a consumer reading a compiled object; `CompileRoot()` stays the authored
one. A kind never has to be leaf and container at once, which is the framing that made
this look impossible.

Four things fall out, none of which needed a new mechanism:

- a name a lowering takes cannot collide with one somebody wrote — different scopes;
- a produced queue's `dead_letter` is an ordinary `Ref()` resolving to the sibling queue
  beside it, because a bare name resolves in the scope it is written in;
- what reaches in says where: `Scope()` is the produced scope, absolute from the project
  root, so `Reaches(...)` writes `namespace:` on the entry beside the ops (that key is the
  access block's field name, not the kind of scope it points into);
- **there is no fourth produced resource.** The scope is the source, so nothing is
  recorded as produced for it and nothing draws a card for a box that IS the scope.

What it costs: two scopes in one place cannot share a NAME, whatever kind each is — every
name resolution in tcc walks a chain of scope names, so an authored `namespace/checkout`
and the scope `workflow/checkout` becomes are one name with two directories behind it.
Reported as *"namespace/checkout is written here AND is the scope workflow/checkout
becomes"*.

**ONE KEY, TWO THINGS, AND THE IDENTITY TELLS THEM APART.** This is the part that bites,
and it is forced by §5: an access block is authored at the top level of a resource's own
document under the KIND it reaches, and a scope's resource groups are keyed by kind too.
So a container that is also a resource has `function:` meaning both — `agent/support`
holds `{<id>: {name: support_infer}}`, the loop it became, beside `{checkout: {ops:
[invoke]}}`, the tool it reaches.

Both halves are needed and neither may read the other's:

- **loading** routes by where a child is authored. `Nested(...)` marks a group as the
  kind's OWN — its access blocks, its variables, its steps — and under a container those
  are read from the `Leaf()` document while everything else is read from the directories.
  A key one of them claims is not an entry of the directory group sharing its name, which
  is `claimedUnder`'s rule one level out.
- **walking** routes by the identity the schema names. A resource is keyed by its `id`; an
  entry is keyed by the label of what it reaches and never had one. `interp.HeldResource`
  is that question for a consumer, and decompile's `strip` already leaned on the same fact
  — *"an id is the one key nothing else in the tree is keyed by"*.

Get either wrong and it is SILENT in both directions: letting the directory win dropped
the agent's declared tools, and walking the held resources as nested data ran every scalar
through its codec twice.

Two knock-on facts a vocabulary has to know: `IndexCtx.Scope` is a CHAIN whose levels each
name their container (`ScopeChain`, with `IDs()` for a vocabulary that has one kind of
scope), and `LayoutOf().Scope` is `{kind}/{name}` rather than one container's name —
`schema.ScopeKinds(obj)` is where a consumer gets the `{kind}` for each level, and it
answers `workflow` for `checkout` and `namespace` for `shop` in the same project.

**COMPILING IS LOWERING.** `Compile` returns the configuration as it RUNS — the queues
are in the object, because nothing runs a workflow. `Authored` is the compile of what was
written, for an editor and for computing a lowering from. A caller who had to remember a
second call would sooner or later hold an object silently missing what it needs.

**Decompile chooses how far back it goes.** Half way, the default, writes only what
somebody wrote: what belongs in a repository. `WithAsLowered` writes everything. The
object carries the record that makes the choice possible — which resources nobody wrote,
where each came from, and which access entries a lowering added to a resource somebody
did. It sits BESIDE the project, where the index subtree sits, because a resource has to
stay the shape its schema declares.

Rules that came out of getting each of these wrong first:

- **A lowering ADDS; it never replaces.** The authored resource stays as the shell, which
  is what everything else points at and what decompile writes back. tcc compilation is
  reversible and that is not negotiable for a new construct.
- **A link is an ACCESS, and its verb is one the target kind declares.** `Reaches("receive",
  …)` produces the entry an author would have written. The prose it replaced —
  `"consumed by"` — was drawing information: there was nothing in it a compiler could
  emit, which is why what a workflow became existed nowhere but a picture.
- **What a lowering produces is config, and it proves it by compiling.** The resources go
  into an in-memory layer over the project and through the ordinary pipeline, so a
  produced resource that a kind would reject is a compile error rather than a document
  that reads fine and deploys to nothing. Nothing renders yaml: the tree goes to yaseer,
  which is what an editor hands one to.
- **A name a lowering takes must be free.** Producing over an authored resource is an
  error before anything is written, not a finding about an object that has already lost
  the file.
- **What the author wrote is theirs.** An op they already granted is not recorded as
  granted, a namespace they wrote on an entry is not replaced — or decompiling to what
  they wrote would delete their own lines.
- **Read the lowering as a PICTURE before believing it.** Three modelling errors were
  invisible in the config and obvious as a diagram: a queue feeding a queue, nothing
  consuming a queue, and nothing starting the workflow at all.
- **A derived resource must be able to EXIST.** `CheckLowerings` refuses a lowering that
  leaves a required field of the produced kind unset, reads a field the shell does not
  have, or cycles. Whether an op is one the target kind DECLARES is not among them and
  cannot be: the generic layer knows a value goes at a path and has no word for a verb.
  That check is this vocabulary's — `CheckLoweredOps`, over `interp.DeclaredWrites`.
- **The printed form of a term is a DIAGNOSTIC, never a value.** `{each.x}` is how a term
  reads in an error; a term that cannot fill drops the field, drops the write, and refuses
  the name. Rendering one into the compiled object put a string nobody wrote where config
  goes, and into a produced resource's address.
- **Templates never grow a calculator.** A step counts `attempts`, not `retries`, so the
  queue's `max-attempts` is a copy. If a lowering needs arithmetic, the field is named
  wrong.
- **The plan carries its source.** A fault found in something nobody wrote is reported
  against something somebody did — the workflow and the step.

## 8b-i. The index

v2 has one footprint, `byName` in `schema/index.go`, and every kind carries it —
`attachIndexes()` hangs it off each group the way `attachAccess()` hangs off the access
blocks, because a rule that holds for all of them is not twenty-five lines each of which
can be forgotten when the twenty-sixth arrives.

```
projects/<pid>[/<scope-kind>/<sid>]…/<kind>/<name>/links
    -> branches/<b>/projects/<pid>[/<scope-kind>/<sid>]…/<kind>/<rid>
```

It answers the question every v2 edge asks. A compiled resource carries its target's
NAME, never its id — `kv/orders` plus where to look — so a node holding
`(scope, kind, name)` has to turn that into a resource, and without a table that is a
walk of every scope's every group. Resolving outward, which is what a bare name does
(`scoping.go`), is dropping the last two segments and asking again.

Three properties of the shape, each a decision rather than an inheritance:

- **The kind is in the key.** v1's by-name index has none, so a consumer picks its own
  kind out by reading the resource type from each value — a consumer parsing this
  vocabulary's address space, which §8c forbids.
- **The scope is its ids, and each level says what KIND of scope it is.** The key and the
  value it holds are built from the same segments; a scope is itself indexed by name,
  which is what turns a written `/shop/eu` into those ids. `<scope-kind>` was the literal
  `namespace` until a lowering made a scope of the source's own kind, and a level indexed
  under the wrong word is a subtree no reader can rebuild the path to.
- **No commit in the value.** The branch is in it and the reader resolves the branch to
  the release it is serving, which is what the trailing `links` bucket is for.

Everything below is why it is only ever expressed this way.

An entry is one of three things — `Link` (a bucket many resources accumulate in), `Set`
(a single fact about this one), `Hole` (a key reserved for something nothing points at
yet) — over a path this vocabulary computes. `ctx.Each(ref, group, key)` is there for the
fan-out: what this resource references, by what those are called.

**Do not add a footprint the driver has to know about.** v1 had six — by-name,
foreign-key, repository, name, by-scope, placeholder — and each one taught the
interpreter a little more of tau's address space, until `pkg/specs` was imported by the
layer that has to serve every vocabulary. All six are expressible as Link/Set/Hole over a
path, which is how they are written now.

If something genuinely cannot be, that is a MISSING PRIMITIVE and it is worth saying so:
raise what shape the driver is missing rather than adding a seventh annotation, because
the seventh is how the first six happened. This index needed exactly one, and it was a
fix rather than an addition: `IndexCtx.Scope` was the id of the scope a resource is in,
worked out by asking the root's container for the name of the object it had been handed
— a direct child only one level down. v1's applications cannot hold applications, so one
level was the whole tree; everything two namespaces deep was indexed as though it sat at
the project root. It is the scope CHAIN now, ids from the root down.

The POSITION is not decoration. Buckets accumulate across scopes in the order groups are
visited, so a different sequence is a different artifact. v2's is the declaration order
of `V2Resources`, with the scope container last.

**A group with no index footprint emits no deferred validations either.** The whole
driver is gated on `UsesIndexing`, and inside it an attribute's `EmitValidation` fires
after the footprint runs — so v2's `zone`/dns check was declared and dead until this
landed. Do not read that coupling as a rule; it is a trap in the driver, recorded in
`.notes/v2/lowering.md`.

## 8c. The rules a consumer depends on

The DSL is the only place that knows this vocabulary. Five rules, each of which was
broken here before it was written down.

**Everything in the DSL is generic.** A term in `engine` or `interp` may not know a
kind. The vocabulary declares; the generic layer carries. `Icon()`, `Category()`,
`Ops()` are the pattern.

**Reuse or enhance before inventing.** `NamesResource()` and `HandsTo()` were added
beside `Ref()` — three ways to say "this points at a resource of kind K", so a consumer
asked three questions, and a new case went into one of them and was forgotten in the
others. It is one term with modifiers: `Ref(kind, ByName(), KindFrom(field),
Inbound())`, read back through `References()`.

**No consumer hardcodes this vocabulary.** A graph, a CLI, a console must draw a version
they have never seen. If a consumer knows which key is an ingress, which field names a
function, which kind is the scope container, or how a file path is built, that is a hole
HERE.

**Never compensate in a view.** A picture special-cased into looking right says the
config is coherent when it is not — and a reviewer reads the picture. If a drawing needs
a fact, this vocabulary states the fact: direction (`Inbound()`), what is worth an arrow
(`Ref`), what a client must dial (`Derived(DeriveSRVName, …)`).

**A view may repeat, but never silently.** One resource can be drawn as several boxes —
a secret six things read is one box with six lines crossing everything between them, and
beside each user it is six boxes crossing nothing. That is a layout decision, and it is
allowed. What is not allowed is a copy that does not say it is one: the count is on every
box, picking one picks all of them, and anything that reports about the resource (what
uses it, what it uses) asks what the box IS, never which box it is. A panel that filtered
by the box would say one thing uses a secret that six things use, which is the view lying
about the config again, in a new place.

## 9. The one modelling rule

**The target owns the policy, the accessor only states intent.** A resource that can
be reached declares its own `authorizer` — a function handed the caller and the
operation, which RETURNS the ops that caller gets. A resource that reaches declares
`ops`, and that is a request and an upper bound, never a grant.

The inverse — the caller naming the authorizer it will be judged by — is policy
written by the party requesting access, which is not policy. It reads fine on one
resource and is indefensible the moment two teams own the two ends.

Corollary: an authorizer is not a kind. It carried exactly a function's fields, and
what makes it one is the host interface it is built against, which is a property of
the artifact rather than of the config.

## 10. Two rules learned the hard way

**Declare a thing once, on the thing.** A kind's verbs are `Ops(ReadOp("get"),
WriteOp("put"))` on the kind itself, and the access block, the load-time check, the
JSON-Schema enum and the effect classes are all read back off it. A parallel table
listing kinds and their verbs drifts the day someone adds a verb.

**Generic means one rule, not a fallback chain.** A first attempt made `Key()` mean
"the branch it matched, or the key it is filed under, or its own last path segment"
depending on the shape of the path. That is three behaviours wearing one name, and
the third existed only to keep an old test green. It was rejected, correctly. If a new
capability needs a new meaning, give it its own addressable form — a path location,
say — rather than a mode on an existing term.

## 10a. A project must be portable, and a project must become a template

Two directions, and every field has to survive both.

**Portable.** A project is imported into ANOTHER cloud and another realm and works
there. So nothing in the config may name the cloud it happens to be running in: not a
fqdn the operator owns, not an address the platform assigned, not an id that only
resolves in one deployment. This is why the generated host is `<name>.<project
label>.<cloud fqdn>` with only the first two halves in the config, why an L4 proxy
authors no port and no ip, and why a gateway names a `zone` RESOURCE rather than a
fqdn. A field that reads fine in one cloud and is meaningless in the next is a bug
even when it compiles.

**Template.** The same project is exported as a starting point for someone else's.
That subtracts IDENTITY: the project id and every resource CID, the git repositories,
the zones the author owns, the secret values. What is left has to still be a valid,
readable project — so identity has to be SEPARABLE rather than woven through, and a
resource has to be nameable by a name (an address, a zone reference) rather than only
by an id that regeneration invalidates.

The test for a new field: could this project be dropped into another cloud unchanged,
and could it be handed to a stranger with the identity stripped? If either answer is
no, the field is carrying deployment state and belongs somewhere else.

## 11. Decisions are not yours

`omissions.go` records what the vocabulary deliberately lacks, and carries a `Decided`
axis — `ByOwner` / `ByDefault` / `Forced`. A technical constraint decides itself. A
**name**, or whether a capability is worth its support cost, is a product call: mark it
`ByDefault`, say what would unblock it, and do not write a confident rationale under
it. `databases` was renamed to `kv` on a rationale that had already expired, and had to
be reverted.

When a shape is refused twice, stop building and ask. Six workarounds in a row cost
more than one question.
