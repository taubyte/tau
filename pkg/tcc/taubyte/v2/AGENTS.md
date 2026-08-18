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

`tools/tcc-gen` is the exception — it carries `--dsl v1|v2` to select the vocabulary.

**Read `taubyte/v1/schema/definition.go` first.** It is a complete worked example of
every construct. `functions` alone answers most questions.

---

## 1. Where things live

| Authored | Loaded by |
|---|---|
| `functions/checkout.yaml` — a resource, one flat file, `id` at the document root | `DefineGroup("functions", DefineIter(attrs…))` |
| `namespaces/shop/config.yaml` + `namespaces/shop/functions/*.yaml` — a scope | `DefineGroup("namespaces", DefineIterGroup(attrs, groups…))` |
| `clouds:` as a map inside the project's `config.yaml` | `DefineGroup("clouds", DefineIter(attrs…))` |

A **resource** (`DefineIter`, `Group == false`) reads its attributes from the
document root and **never walks children** — `engine/node.go:478-483` returns before
the child walk at `:529`. Passing a `*Node` to `DefineIter` compiles and is silently
dropped.

A **container** (`DefineIterGroup`, `Group == true`) walks children, but reads its own
attributes from the `config` leaf (`node.go:486`). In a directory that is
`config.yaml`; in a flat document it is a literal `config:` key. This is why a
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

Related conditionals: `RequiredWhen`, `RequiredUnless`, `OnlyWhen`, `ShowWhen`,
`SectionWhen`, `DerivedBool`, `EnumBool`.

---

## 4. `Tag()` is the wire key, and it must be a path

TNS stores the compiled map as a tree and merges it as a CRDT, so the compiled key
**is** the TNS path. Use slashes:

```go
Tag("route/handler/namespace")   // three mergeable segments
Tag("handler-namespace")         // one opaque leaf — wrong
```

`Path(...)` is where the value is **read from**, `Tag(...)` is where it is **written
to**. Two independent knobs; don't collapse them.

The rule to hold: **an attribute authored under a nested block compiles to that
path.** `Path("execution","call")` ⇒ `Tag("execution/call")`. Where the path contains
an `Either(...)`, the tag names the branch by hand — `Tag("engine/distributed/size")`,
`Tag("route/handler/namespace")`.

### `Tag(x)` on an attribute already named `x` deletes the field

`applyInstanceOps` step 5 does `sel.Move(a.Name, tag)`, and `Move` is
`data[to] = data[from]; delete(data, from)` (`object/generic.go:216-224`). Same name in
and out ⇒ the field is set and then deleted, silently. Only tag when the name differs.

---

## 5. Things that do not exist — stop looking

- **A wrapper for the access block.** A resource declares what it reaches as
  `databases:` / `storages:` / `secrets:` at the top level of its own document, the way
  it declares `source:` and `execution:`. There is no `uses:` (or `edges:`) key; it read
  as a section bolted onto the resource and it only ever existed because it was easier
  to generate.
- **List-of-objects / map-of-objects attributes.** Types are
  `int | bool | float64 | string | []string` (`engine/types.go:32-44`).
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
- **More than one walked container.** `containerKey` (`interp/shape.go`) returns the
  **first** top-level group whose iterator is a group **and which carries no
  `Resource(...)`**, and `utils.Global` walks the project root plus that one. A second
  scope silently gets no ref resolution.

### What NOT to do instead

Do not smuggle structure into strings. A route is not
`"POST /checkout function:/checkout"`, an edge is not `"kv/orders get,put"`. It buys a
`[]string` and gives up per-field validation, completion, JSON-Schema emission and
per-field error locations — four of the five reasons the DSL exists. If the shape does
not fit, say so and stop; do not invent a seventh encoding.

(The one accepted exception is `zones.records`, which uses zone-file presentation
lines — an actual industry interchange format, not an invented grammar. The cost is
recorded in `omissions.go`.)

---

## 6. References

```go
StringSlice("domains", Ref("domains"), …)                    // []name → []id
String("source", Ref("module", Prefix("module/")), …)        // "module/x" → "module/<id>"
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
  schema expects `<name>/config.yaml`. §1.
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

## 11. Decisions are not yours

`omissions.go` records what the vocabulary deliberately lacks, and carries a `Decided`
axis — `ByOwner` / `ByDefault` / `Forced`. A technical constraint decides itself. A
**name**, or whether a capability is worth its support cost, is a product call: mark it
`ByDefault`, say what would unblock it, and do not write a confident rationale under
it. `databases` was renamed to `kv` on a rationale that had already expired, and had to
be reverted.

When a shape is refused twice, stop building and ask. Six workarounds in a row cost
more than one question.
