package worker

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

// resolve.go turns a name into something the pool can run.
//
//	(project, environment)            -> what it runs   [projects.Cache]
//	(scope, kind, name)               -> a resource     [an index hit]
//	resource                          -> an invoke.Spec [read off the vocabulary]
//
// THE FIRST STEP IS projects.Cache AND NOT A CACHE OF ITS OWN. A gateway does
// exactly the same read on exactly the same request, and two copies of it would
// be two answers to "how long after a deploy does the old code stop being
// served" — which is a promise a platform makes once.

// Target is what a caller names. It is pkg/project's Address under the name a
// worker's callers use for it: a gateway names one too, and two spellings of
// the same five fields is where a caller starts converting between them.
type Target = specs.Address

var (
	ErrNoTarget = errors.New("worker: a target with no project, environment, kind or name names nothing")

	// ErrNoOutcome is a transformer that answered with neither arm. It is its
	// own error because choosing one would be this service deciding whether
	// traffic carries on, which is the transformer's decision and nobody else's.
	ErrNoOutcome  = errors.New("worker: that transformer decided neither to forward nor to answer")
	ErrNoRuntime  = errors.New("worker: nothing runs this kind")
	ErrAmbiguous  = errors.New("worker: the kind runs on more than one tier and the resource does not say which")
	ErrNoArtifact = errors.New("worker: the resource is built by the platform and nothing here resolves what it built")
	ErrNoSecrets  = errors.New("worker: the resource wants a secret and nothing here resolves one")
)

// Artifacts answers what a BUILT resource currently runs.
//
// IT IS A SEAM BECAUSE THE COMPILED CONFIG GENUINELY DOES NOT KNOW. A resource
// whose source is an image or a component names the artifact itself and needs
// nothing here. One whose source is a path in the repository names the SOURCE,
// and what came out of building it is the build service's to say — so a worker
// that guessed a naming convention would be hardcoding one, and a worker that
// silently ran nothing would be worse.
//
// Absent, a built resource is refused with ErrNoArtifact, which says what is
// missing rather than failing later as an image that does not exist.
type Artifacts interface {
	Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error)
}

// Secrets resolves a secret a run-time variable refers to.
//
// A MISSING SECRET IS A REFUSAL AND NOT AN EMPTY STRING. A workload started
// without its database password does not fail where the password is missing; it
// fails somewhere else, later, in a way that reads as a broken application.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// spec reads a resource and says what to run.
//
// EVERY VALUE COMES OFF THE COMPILED RESOURCE, and none of them is a
// constructor argument or an operator setting. `execution/memory` and
// `execution/timeout` are PerEnv in the vocabulary, which means they are
// expected to differ between an environment and the next — a worker that had
// captured either at construction would serve a number the author changed and
// nothing would happen.
func (s *Service) spec(ctx context.Context, t Target, p register.Pointer, proj *specs.Project, r specs.Resource, resourceID string) (invoke.Spec, error) {
	// A KIND THIS SERVICE ANSWERS ITSELF HAS NEITHER OF THE NEXT TWO, and asking
	// for them is what made `mcp` unreachable: runtimeOf refuses a kind the
	// vocabulary gives no engine, and artifactOf then asks the build service for
	// the output of a build that never happened. Invoke's own mcp branch sits
	// after Resolve, so both refusals landed before anything could answer one.
	var runtime, artifact string
	if !servedHere(t.Kind) {
		var err error
		if runtime, err = runtimeOf(t.Kind, r); err != nil {
			return invoke.Spec{}, err
		}
		if artifact, err = s.artifactOf(ctx, t, r, resourceID); err != nil {
			return invoke.Spec{}, err
		}
	}

	env, err := s.envOf(ctx, t, r)
	if err != nil {
		return invoke.Spec{}, err
	}

	reaches, err := reachesOf(t, proj, r)
	if err != nil {
		return invoke.Spec{}, err
	}

	spec := invoke.Spec{
		Runtime:  runtime,
		Name:     strings.Join([]string{t.Project, t.Environment, t.Kind, resourceID}, "/"),
		Revision: string(p.Object),
		Artifact: artifact,
		Env:      env,
		Reaches:  reaches,

		// WHAT THIS WORKLOAD IS, for `taubyte:workload/identity`. It comes off the
		// PROJECT rather than off the Target, because a Target is what somebody
		// ASKED for — a name that resolves in a parent scope, or an id — and
		// what a workload is told about itself has to be where it actually
		// lives.
		Project:     t.Project,
		Environment: t.Environment,
		Kind:        t.Kind,
		Label:       r.String("name"),
	}
	if _, scope, _, ok := proj.Locate(resourceID); ok {
		spec.Scope = scope
	}
	if memory, ok := r.Int("execution", "memory"); ok {
		spec.Memory = memory
	}
	spec.Ports = portsOf(r)
	if timeout, ok := r.Duration("execution", "timeout"); ok {
		spec.Timeout = timeout
	}
	if concurrency, ok := r.Int("limits", "concurrency"); ok {
		spec.Concurrency = int(concurrency)
	}
	// WHAT THE PLATFORM ITSELF RUNS, if anything. It travels beside the
	// artifact rather than instead of it so that a tier reads one field to
	// know which it has.
	spec.Builtin = r.String("source", sourceBuiltin)

	spec.Settings = settingsOf(r)

	// AN MCP'S OWN FIELDS. It runs no engine, so nothing else would carry them
	// — and what they say is how widely its listing may be cached.
	if t.Kind == mcpKind {
		spec.Settings = merged(spec.Settings, mcpSettings(r))
	}

	// WHERE A WORKLOAD MAY REACH ON THE NETWORK IS THE RESOURCE'S, and an
	// absent list is the node-wide floor alone rather than nowhere — the
	// vocabulary's own sentence. The tier enforces it; this only carries it,
	// because which mechanism does the enforcing is the tier's business and
	// the allowlist is the author's.
	spec.Egress = r.Strings("egress")
	return spec, nil
}

// reachesOf turns the access block into grants the tier can enforce.
//
// A LABEL IS RESOLVED HERE AND A HANDLE IS WHAT TRAVELS. The label is scoped —
// the fixture has three caches called `sessions` in three namespaces — so a
// tier handed the label would have to resolve it, which means the tier would
// need the compiled project. The id it resolves to is unambiguous, and the
// handle is the id with the deployment it belongs to in front of it.
//
// A TARGET THIS CLOUD CANNOT SERVE IS A REFUSED GRANT AND NOT A REFUSED
// WORKLOAD. Access is declared per target, so it degrades per target: the
// resource runs, everything else it reaches works, and the one call that needed
// the unservable thing is told which and why. Refusing the whole resource would
// take a function down over one of the things it touches — and the
// vocabulary's own fixture has exactly that shape.
func reachesOf(t Target, proj *specs.Project, r specs.Resource) ([]invoke.Grant, error) {
	var out []invoke.Grant

	// EVERY KIND, NOT JUST THE ONES A TIER SERVES. A tier has to be able to see
	// what it CANNOT do for a workload — a container declaring a volume it will
	// not get is data written to a layer that vanishes — and it cannot see that
	// from a list filtered down to what already works.
	for _, kind := range schema.Kinds() {
		for _, reach := range r.Reaches(kind) {
			target, id, err := proj.Reached(t.Scope, reach)
			if err != nil {
				return nil, fmt.Errorf("%w: %s wants %w", ErrNoTarget, t.Name, err)
			}

			grant := invoke.Grant{
				Kind:     reach.Kind,
				Label:    reach.Label,
				Handle:   specs.Handle(t.Project, t.Environment, id),
				Ops:      reach.Ops,
				Settings: merged(settingsFor(reach.Kind, reach, target), instanceSettings(target)),
				Refused:  unservable(reach, target),
			}

			// A LOCALLY SERVED MODEL IS A WORKLOAD, and where it lives is a
			// question only the compiled project can answer. It is resolved
			// here, where the project is open, rather than by the backing —
			// which holds a handle and would need the register on the call
			// path to work out the same thing.
			if reach.Kind == modelKind {
				grant.Settings = merged(grant.Settings, localModelScope(proj, target, id))
			}

			// STARTING A WORKFLOW IS SENDING TO ITS FIRST STEP'S QUEUE, and the
			// workflow itself says so — the lowering writes `entry/start` onto
			// the shell precisely so that no consumer has to work it out from a
			// naming convention. What the grant carries is that queue's handle,
			// so a tier performing `start` sends and does not have to know what
			// a workflow is.
			if reach.Kind == workflowKind && contains(reach.Ops, opStart) {
				if err := startedBy(proj, t, target, id, &grant); err != nil {
					return nil, err
				}
			}

			// DIALLING A CHANNEL IS INVOKING WHAT SERVES IT, and the channel
			// itself says what that is. The grant carries the resolved address
			// so that a tier performing `dial` invokes and does not have to
			// know what a channel is — the same trade startedBy makes.
			if reach.Kind == channelKind {
				servedBy(proj, t, target, id, &grant)
			}

			// A TOOL'S NAME IS THE AUTHOR'S LABEL AND NOT THE TARGET'S. An mcp
			// publishes its `invoke` grants as tools, so what a model calls
			// back with is the label — which may name a function of another
			// name, in another namespace. A Grant carries a label and a handle
			// and no address, so the address resolved here is what travels, for
			// the reason servedBy states just above.
			if reach.Kind == functionKind && contains(reach.Ops, opInvoke) {
				invokedAt(proj, target, id, &grant)
			}

			// A GUARD IS CROSS-KIND, which is why it is not in settingsFor:
			// attachAccess gives an `authorizer:` block to every kind that
			// declares Ops(), so this runs for all of them rather than being
			// repeated per kind.
			guardedBy(proj, t, target, id, &grant)

			out = append(out, grant)
		}
	}
	return out, nil
}

const (
	cacheKind    = "cache"
	volumeKind   = "volume"
	streamKind   = "stream"
	storageKind  = "storage"
	workflowKind = "workflow"
	busKind      = "bus"
	databaseKind = "database"
	channelKind  = "channel"
	modelKind    = "model"

	// syncKind is one kind with two primitives under it; the type selects which,
	// and settingSyncType is how that travels to a tier.
	syncKind        = "sync"
	queueType       = "queue"
	logType         = "log"
	settingSyncType = "type"

	// sourceBuiltin is the vocabulary's word for a program the runtime ships.
	sourceBuiltin = "builtin"
)

// portsOf is what this workload binds inside its container, by name.
//
// THE VOCABULARY HAS DECLARED THESE SINCE v2 BEGAN AND NOTHING READ THEM. A
// container that binds a fixed port — which is every off-the-shelf image — was
// given an ephemeral port in $PORT and mapped to that same ephemeral number, so
// the thing it was actually listening on was reachable from nowhere.
func portsOf(r specs.Resource) map[string]int {
	declared := r.Sub("ports")
	if len(declared) == 0 {
		return nil
	}

	out := make(map[string]int, len(declared))
	for _, name := range declared.Keys() {
		if number, ok := declared.Sub(name).Int("number"); ok && number > 0 {
			out[name] = int(number)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// merged is two setting maps as one, with nil for neither.
//
// IT IS AN OVERLAY AND NOT A MERGE STRATEGY: the two maps carry different keys
// by construction — a kind's own attributes and the `instances:` block — so
// there is nothing to resolve and a collision would be a bug in one of them.
func merged(first, second map[string]string) map[string]string {
	if len(second) == 0 {
		return first
	}
	if first == nil {
		return second
	}
	for key, value := range second {
		first[key] = value
	}
	return first
}

// startedBy fills in where a `start` on a workflow lands.
//
// THE SHELL RECORDS IT AS AN ORDINARY VALUE — `entry: {start: {kind: queue,
// name: checkout_reserve, as: send}}` — because a lowering that left it to a
// naming convention would be a second place for the convention to be wrong. The
// queue lives INSIDE the workflow, which is a scope of its own, so this is also
// where a consumer needs the read path to understand a scope that is not a
// namespace.
func startedBy(proj *specs.Project, t Target, workflow specs.Resource, id string, grant *invoke.Grant) error {
	kind := workflow.String("entry", "start", "kind")
	name := workflow.String("entry", "start", "name")
	if kind == "" || name == "" {
		// A workflow whose shell carries no entry is a compiled object that did
		// not come from tcc; starting it would mean guessing.
		grant.Refused = fmt.Sprintf("workflow %q records no entry point, so there is nowhere for a start to land", grant.Label)
		return nil
	}

	// WHERE THE WORKFLOW ITSELF IS, which is not where the asker is: a bare
	// name resolves outward, so the workflow may have been found in a parent.
	_, scope, own, ok := proj.Locate(id)
	if !ok {
		grant.Refused = fmt.Sprintf("workflow %q could not be located in this project", grant.Label)
		return nil
	}

	start, startID, err := proj.Resource(append(append([]string{}, scope...), own), kind, name)
	if err != nil {
		grant.Refused = fmt.Sprintf("workflow %q starts at %s/%s, which is not in it: %s", grant.Label, kind, name, err)
		return nil
	}

	// THE START QUEUE'S OWN SETTINGS TRAVEL WITH IT. A queue is opened from the
	// settings on the grant, so without these a `start` opened the first step's
	// queue with pkg/queue's defaults while the DISPATCHER opened the same queue
	// with what its author wrote — two configurations for one queue, and which
	// one was in force depended on who got there first.
	grant.Settings = merged(grant.Settings, syncSettings(specs.Reach{}, start))
	grant.Settings = merged(grant.Settings, map[string]string{
		settingStartKind:   kind,
		settingStartHandle: specs.Handle(t.Project, t.Environment, startID),
		settingStartAs:     workflow.String("entry", "start", "as"),
	})
	return nil
}

// The keys the shell's entry record becomes.
//
// TWO OF THEM ARE BORROWED AND NOT SPELLED. invoke.StartGrant reads back what
// this writes, so a rename on one side and not the other is a workflow that
// silently records no entry at all — see pkg/invoke/start.go for what that
// already cost once.
const (
	opStart = "start"

	settingStartKind   = invoke.SettingStartKind
	settingStartHandle = invoke.SettingStartHandle
	settingStartAs     = "entry/start/as"
)

// servedBy fills in where a `dial` on a channel lands.
//
// A CHANNEL NAMES WHAT ANSWERS IT — `serve: {function: {name: sync, namespace:
// /shop}}` — and that name is resolved HERE, where the compiled project is
// open, rather than by whatever dials. It is the rule Grant.Settings states: a
// backing that had to resolve a name back into a resource would need the
// register on the request path.
//
// THE SCOPE IS RESOLVED, NOT COPIED. An omitted namespace means the channel's
// own scope and then its parents, and a leading "/" ignores the channel
// entirely — one rule, stated once in specs.ScopeOf, and what travels is the
// chain it produced rather than the sentence that produced it.
//
// A NAME THAT RESOLVES TO NOTHING IS REFUSED HERE rather than at the first
// dial. The schema's Ref() already fails such a config at compile, so this
// catches a compiled object that did not come from tcc — and refusing the one
// grant leaves the rest of the workload running, which is the whole reason
// Grant.Refused exists.
func servedBy(proj *specs.Project, t Target, channel specs.Resource, id string, grant *invoke.Grant) {
	kind := channel.String("serve", "kind")
	name := channel.String("serve", "name")
	if kind == "" || name == "" {
		grant.Refused = fmt.Sprintf("channel %q names nothing that serves it, so a dial has nowhere to land", grant.Label)
		return
	}

	// WHERE THE CHANNEL ITSELF IS, which is not where the dialer is: a bare
	// name resolves outward, so the channel may have been found in a parent.
	//
	// ITS SCOPE, NOT ITS SCOPE PLUS ITSELF — the difference from startedBy just
	// above, and the reason that line reads differently. A workflow IS a scope,
	// because its queues live inside it; a channel is an ordinary resource in
	// one, so appending its own name would look for the server in a namespace
	// that does not exist.
	_, scope, _, ok := proj.Locate(id)
	if !ok {
		grant.Refused = fmt.Sprintf("channel %q could not be located in this project", grant.Label)
		return
	}

	at := specs.ScopeOf(scope, channel.String("serve", "namespace"))
	if _, _, err := proj.Resource(at, kind, name); err != nil {
		grant.Refused = fmt.Sprintf("channel %q is served by %s/%s, which is not in this project: %s", grant.Label, kind, name, err)
		return
	}

	grant.Settings = merged(grant.Settings, map[string]string{
		settingProtocol:   channel.String("protocol"),
		settingServeKind:  kind,
		settingServeName:  name,
		settingServeScope: "/" + strings.Join(at, "/"),
	})
}

// What a channel's own block becomes.
const (
	settingProtocol   = "protocol"
	settingServeKind  = "serve/kind"
	settingServeName  = "serve/name"
	settingServeScope = "serve/namespace"
)

// invokedAt records which function an `invoke` grant actually resolved to.
//
// THE CALLER HAS THE LABEL AND NEEDS THE ADDRESS. An mcp looks a tool up by the
// name it published — the author's label — and then has to invoke something.
// Building a target out of the label invoked a function of THAT name in the
// CALLER's scope: the wrong function wherever a grant renamed one, and nothing
// at all wherever it named one in another namespace.
//
// IT IS NOT REFUSED WHEN IT CANNOT BE LOCATED, unlike a channel's server. The
// grant is already good — proj.Reached resolved it — so a missing location is
// this record being absent, and the caller answers for that at the call.
func invokedAt(proj *specs.Project, target specs.Resource, id string, grant *invoke.Grant) {
	_, scope, _, ok := proj.Locate(id)
	if !ok {
		return
	}

	grant.Settings = merged(grant.Settings, map[string]string{
		settingInvokeName:  target.String("name"),
		settingInvokeScope: "/" + strings.Join(scope, "/"),
	})
}

// Where an `invoke` grant's target actually lives.
const (
	settingInvokeName  = "invoke/name"
	settingInvokeScope = "invoke/namespace"
)

// guardedBy fills in who decides what this caller may do to the target.
//
// A RESOURCE NAMES ITS OWN GUARD — `authorizer: {name: tier_check, namespace:
// /shop}` — and that name is resolved HERE for the reason servedBy gives: the
// tier holds a handle and not the compiled project, and a backing that had to
// turn a name into a function would need the register on the request path.
//
// THE SCOPE IS THE TARGET'S, NOT THE CALLER'S. The guard belongs to the thing
// being reached, so an omitted namespace means the TARGET's own scope and then
// its parents — a cache in /shop guarded by `tier_check` means /shop's
// tier_check, whoever is reaching it and from wherever.
//
// A GUARD THAT RESOLVES TO NOTHING IS REFUSED rather than ignored. The schema's
// Ref() catches it at compile, so this is for a compiled object that did not
// come from tcc — and a guard nobody can run must not read as a resource nobody
// guarded.
func guardedBy(proj *specs.Project, t Target, target specs.Resource, id string, grant *invoke.Grant) {
	name := target.String("authorizer", "name")
	if name == "" {
		return
	}
	if grant.Refused != "" {
		// Already unservable for a reason of its own; a second sentence about
		// its guard would bury the first.
		return
	}

	_, scope, _, ok := proj.Locate(id)
	if !ok {
		grant.Refused = fmt.Sprintf("%s %q is guarded by %q and could not be located in this project",
			grant.Kind, grant.Label, name)
		return
	}

	at := specs.ScopeOf(scope, target.String("authorizer", "namespace"))
	guard, _, err := proj.Resource(at, "function", name)
	if err != nil {
		grant.Refused = fmt.Sprintf("%s %q is guarded by the function %q, which is not in this project: %s",
			grant.Kind, grant.Label, name, err)
		return
	}

	// AND IT HAS TO BE AN AUTHORIZER. A function that implements the handler
	// world would instantiate and then be called with a question it reads as a
	// request — the failure `implements` exists to prevent, caught here where
	// the sentence can name both resources instead of at a cold start.
	if world := guard.String("implements"); world != wasm.WorldAuthorizer {
		grant.Refused = fmt.Sprintf("%s %q is guarded by %q, which implements %q rather than an authorizer",
			grant.Kind, grant.Label, name, world)
		return
	}

	grant.Settings = merged(grant.Settings, map[string]string{
		settingAuthorizer:      name,
		settingAuthorizerScope: "/" + strings.Join(at, "/"),
	})
}

// What a resource's own `authorizer:` block becomes, spelled as
// pkg/invoke/wasm reads it.
const (
	settingAuthorizer      = "authorizer/name"
	settingAuthorizerScope = "authorizer/namespace"
)

// settingsFor is what the TARGET (and, for a mount, the entry) declared, in the
// vocabulary's own attribute paths.
func settingsFor(kind string, reach specs.Reach, target specs.Resource) map[string]string {
	switch kind {
	case cacheKind:
		return cacheSettings(target)
	case volumeKind:
		return volumeSettings(reach, target)
	case databaseKind:
		return databaseSettings(target)
	case syncKind:
		return syncSettings(reach, target)
	case storageKind:
		return storageSettings(target)
	case modelKind:
		return modelSettings(target)
	case mcpToolKind:
		// A TOOL NEEDS A DESCRIPTION, and it is the single most load-bearing
		// string in an MCP listing: it is how a model decides whether to call
		// one. It travels for every function because an mcp's tools ARE the
		// functions it declares access to — see services/worker/mcp.go.
		if d := target.String("description"); d != "" {
			return map[string]string{settingDescription: d}
		}
	}
	return nil
}

// localModelScope is the namespace chain holding the workload that serves a
// locally-served model, in the form specs.Address takes.
//
// IT IS THE MODEL'S OWN SCOPE AND THEN THE DECLARED NAMESPACE, which is what an
// omitted `namespace:` means everywhere in this vocabulary — the same rule a
// proxy's forward and a gateway's transformer follow.
func localModelScope(proj *specs.Project, target specs.Resource, id string) map[string]string {
	if target.String("inference", "kind") != "local" {
		return nil
	}

	_, scope, _, ok := proj.Locate(id)
	if !ok {
		return nil
	}

	resolved := specs.ScopeOf(scope, target.String("inference", "local", "namespace"))
	if len(resolved) == 0 {
		// The project root. An empty value is still a value — the backing has
		// to tell "the root" from "nobody worked it out".
		return map[string]string{settingLocalScope: ""}
	}
	return map[string]string{settingLocalScope: strings.Join(resolved, "/")}
}

// settingLocalScope is where that chain travels. It is not one of the
// vocabulary's own attribute paths because it is not an attribute: it is what
// `inference/local/namespace` RESOLVED to.
const settingLocalScope = "inference/local/scope"

// modelSettings carries everything about WHERE inference happens and how it is
// called, which is the whole of what a model resource is.
//
// THE CREDENTIAL IS A REFERENCE AND STAYS ONE, for the reason databaseSettings
// gives: carrying the value here would put a provider key in a spec that is
// logged, cached and compared. The vault resolves it on the node, at the call.
//
// THE PARAMETERS TRAVEL BECAUSE THE CALLER MAY NOT SET THEM. `max_tokens` in
// particular is a ceiling the host clamps a guest's request to, so it has to be
// where the host can read it rather than where only the backing can.
func modelSettings(target specs.Resource) map[string]string {
	out := map[string]string{}

	for _, field := range []string{"identifier", "format"} {
		if value := target.String(field); value != "" {
			out[field] = value
		}
	}

	// WHICH BRANCH OF THE SWITCH, then that branch's own fields. A backing that
	// had to guess from which keys are present would be reading the shape back
	// out of its contents.
	if kind := target.String("inference", "kind"); kind != "" {
		out["inference/kind"] = kind
	}
	for _, path := range [][]string{
		{"inference", "remote", "endpoint"},
		{"inference", "remote", "credentials", "name"},
		{"inference", "remote", "credentials", "namespace"},
		{"inference", "local", "name"},
		{"inference", "local", "namespace"},
		{"inference", "local", "port"},
	} {
		if value := target.String(path...); value != "" {
			out[strings.Join(path, "/")] = value
		}
	}

	if t, ok := target.Float("parameters", "temperature"); ok {
		out["parameters/temperature"] = strconv.FormatFloat(t, 'g', -1, 64)
	}
	if t, ok := target.Float("parameters", "top_p"); ok {
		out["parameters/top_p"] = strconv.FormatFloat(t, 'g', -1, 64)
	}
	if n, ok := target.Int("parameters", "max_tokens"); ok {
		out["parameters/max_tokens"] = strconv.FormatInt(n, 10)
	}
	if stop := target.Strings("parameters", "stop"); len(stop) > 0 {
		// ONE KEY AND NOT N, because Settings is a flat map and a list has to
		// arrive whole: a stop sequence is an exact string, so the separator is
		// a newline — the one character a stop sequence cannot usefully be.
		out["parameters/stop"] = strings.Join(stop, "\n")
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// syncSettings carries what a coordination resource declares about itself.
//
// THE TYPE TRAVELS FIRST, and every consumer needs it: `sync` is one kind with a
// queue and a log under it, whose verbs and whose backings are different things,
// so a tier filtering on kind alone would hand a log's grant to the queue
// interface. It is the same fact `databaseSettings` and `storageSettings` carry
// for their own kinds.
//
// AND EVERY OTHER KEY IS THE VOCABULARY'S OWN ATTRIBUTE PATH, which now begins
// with the type — `queue/visibility_timeout`, `log/retention/age` — because the
// block that is written IS the type.
//
// THE DEAD LETTER IS A REFERENCE and its compiled value is another queue's id,
// which is what a consumer opens to drain it.
func syncSettings(reach specs.Reach, target specs.Resource) map[string]string {
	model := target.String("type")
	if model == "" {
		return nil
	}
	out := map[string]string{settingSyncType: model}

	// WHAT HAPPENS TO TWO CONCURRENT WRITES, for every type. A tier that cannot
	// serve the answer refuses the target by name rather than serving a weaker
	// promise than the author wrote.
	if consensus := target.String(model, "consensus"); consensus != "" {
		out[model+"/consensus"] = consensus
	}

	switch model {
	case logType:
		for _, path := range [][]string{{"retention", "age"}} {
			if d, ok := target.Duration(append([]string{model}, path...)...); ok {
				out[model+"/"+strings.Join(path, "/")] = d.String()
			}
		}
		if n, ok := target.Int(model, "retention", "size"); ok {
			out[model+"/retention/size"] = strconv.FormatInt(n, 10)
		}
		return out
	case queueType:
	default:
		return out
	}

	for _, path := range [][]string{
		{"visibility_timeout"}, {"delay"}, {"retry", "interval"}, {"retry", "ceiling"},
	} {
		if d, ok := target.Duration(append([]string{queueType}, path...)...); ok {
			out[queueType+"/"+strings.Join(path, "/")] = d.String()
		}
	}
	if n, ok := target.Int(queueType, "max_attempts"); ok {
		out[queueType+"/max_attempts"] = strconv.FormatInt(n, 10)
	}
	// A COMPILED NUMBER IS A NUMBER. `retry/factor` is a Float in the
	// vocabulary, so reading it as a string answered "" and the multiplier
	// never travelled — an author who wrote `factor: 2` got a flat interval and
	// nothing said so. Float rather than Int because 1.5 is a legal factor and
	// truncating it would be the same defect one step quieter.
	if f, ok := target.Float(queueType, "retry", "factor"); ok {
		out[queueType+"/retry/factor"] = strconv.FormatFloat(f, 'g', -1, 64)
	}
	if name := target.String(queueType, "dead_letter", "name"); name != "" {
		out[queueType+"/dead_letter/name"] = name
	}

	// WHERE AN ITEM IS POSTED comes from the ENTRY, because it is per use: one
	// container consuming two queues answers them on two paths.
	if port := reach.DeliverPort; port != "" {
		out[settingDeliverPort] = port
	}
	if path := reach.DeliverPath; path != "" {
		out[settingDeliverPath] = path
	}

	return out
}

// storageSettings carries the data model and the ceiling.
//
// THE TYPE IS THE KEY TO THE REST, exactly as a database's is: `object` and
// `streaming` each own their block, so the size lives under whichever one the
// author wrote.
func storageSettings(target specs.Resource) map[string]string {
	model := target.String("type")
	if model == "" {
		return nil
	}

	// THE COMPILED KEY IS NOT THE AUTHORED PATH, and the two differ per field
	// here. `size` is written under the type block — `object: {size: 1GB}` —
	// and compiles to a FLAT `size`, because the vocabulary declares it
	// Key("size") with Path(Either("object","streaming"), "size"). `versioning`
	// is Key("object","versioning") and stays nested. Reading either the way
	// the other is spelled returns nothing at all.
	out := map[string]string{settingStorageType: model}
	if size, ok := target.Int("size"); ok {
		out[settingStorageSize] = strconv.FormatInt(size, 10)
	}
	if versioning, ok := target.Bool(objectStorage, "versioning"); ok && versioning {
		out[settingVersioning] = "true"
	}
	return out
}

// databaseSettings carries the data model and, from the block that model names,
// the connection.
//
// THE TYPE IS THE KEY TO EVERYTHING ELSE. A database's engine, host, database
// name, tls and credential all live under the type's own block — `sql: {engine:
// postgres, host: …}` — because the engines a type may have are not the same,
// which is the schema's own reason for putting them there.
func databaseSettings(target specs.Resource) map[string]string {
	model := target.String("type")
	if model == "" {
		return nil
	}

	// EVERY FIELD OF THE TYPE'S OWN BLOCK THAT THE PLATFORM READS. `dimensions`
	// and `metric` are a vector's and mean nothing to the other three, which is
	// why they travel as settings rather than as fields on anything.
	out := map[string]string{settingType: model}
	//
	// `consensus` IS IN THE LIST BECAUSE A NODE WITH NO DATABASE SERVICE STILL
	// OPENS ONE. Without it a ledger that asked for raft reached that node's
	// own engines as a crdt and was merged -- silently, which is the one thing
	// the field exists to prevent. Carried, the fallback refuses it by name.
	for _, field := range []string{"engine", "host", "database", "tls", "consensus", "dimensions", "metric"} {
		if value := target.String(model, field); value != "" {
			out[model+"/"+field] = value
			continue
		}
		// A width is an Int in the vocabulary, not a string.
		if n, ok := target.Int(model, field); ok {
			out[model+"/"+field] = strconv.FormatInt(n, 10)
		}
	}
	// THE CREDENTIAL IS A REFERENCE AND STAYS ONE. What resolves it is the
	// vault, on the node, at the moment a connection is opened — carrying the
	// value here would put a password in a spec that is logged, cached and
	// compared.
	//
	// FILED UNDER THE MODEL, like every other field in this function. It used to
	// be read from `<model>/credentials/name` and written to a bare
	// `credentials/name` — the right path in, an invented one out — which is a
	// key `database` does not declare at all. `stream` and `bus` DO declare a
	// bare one, so the constant looked right wherever a reader happened to check.
	if name := target.String(model, "credentials", "name"); name != "" {
		out[model+"/"+settingCredentials] = name
	}
	if namespace := target.String(model, "credentials", "namespace"); namespace != "" {
		out[model+"/"+settingCredentialsScope] = namespace
	}
	return out
}

// volumeSettings carries the quota and, from the ENTRY rather than the target,
// where it appears inside the workload — a mount path is per use, which is why
// the vocabulary puts it on the access entry.
func volumeSettings(reach specs.Reach, target specs.Resource) map[string]string {
	out := map[string]string{}
	if quota, ok := target.Int("quota"); ok {
		out[settingQuota] = strconv.FormatInt(quota, 10)
	}
	// HOW OFTEN IT IS CHECKPOINTED, which is the author's number and was not
	// travelling at all: the node fell back to its own default, so a volume
	// asking to be checkpointed every 10s was checkpointed every minute and a
	// restore lost fifty seconds more than the author budgeted for.
	if every, ok := target.Duration("checkpoint", "interval"); ok {
		out[settingCheckpointEvery] = every.String()
	}
	if path := reach.Path; path != "" {
		out[settingMountPath] = path
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// unservable says why this cloud would have to serve a target WEAKER than it
// was declared, or "" when it would not. Both reasons are one line for an
// author to change.
func unservable(reach specs.Reach, target specs.Resource) string {
	if why := unservableKind(reach, target); why != "" {
		return why
	}

	// AN `instances:` BLOCK IS NO LONGER A REFUSAL. It was one for as long as
	// nothing honoured it — the target named who admits an instance and how
	// many there may be, and serving the grant anyway handed a workload ONE
	// store where the config says many. What honours it now is the tier: the
	// instance travels in the label, an Admitter runs the validator, and the
	// ceiling is counted. See pkg/invoke/wasm/instance.go.
	//
	// What travels for it is settingsOfInstances, merged in reachesOf, because
	// the block is on four kinds and settingsFor is per kind.
	return ""
}

// instanceSettings carries the `instances:` block, which is cross-kind — cache,
// database, storage and bus each declare it — so it is merged onto whatever the
// kind's own settings were rather than repeated in four places.
//
// THE ADMISSION SWITCH IS WHAT SAYS THE BLOCK IS THERE. It is a Captures()
// attribute, so its presence is the declaration and a target without one holds
// exactly one instance.
func instanceSettings(target specs.Resource) map[string]string {
	admission := target.String("instances", "admission")
	if admission == "" {
		return nil
	}

	out := map[string]string{settingAdmission: admission}
	if name := target.String("instances", "validator", "name"); name != "" {
		out[settingValidator] = name
	}
	if namespace := target.String("instances", "validator", "namespace"); namespace != "" {
		out[settingValidated] = namespace
	}
	// A LIMIT OF ZERO IS AN ANSWER AND NOT AN ABSENCE — the vocabulary makes it
	// required precisely so that "no ceiling" is a sentence somebody wrote.
	if limit, ok := target.Int("instances", "limit"); ok {
		out[settingLimit] = strconv.FormatInt(limit, 10)
	}
	return out
}

// unservableKind is what one kind declares that this cloud would have to serve
// weaker than written.
func unservableKind(reach specs.Reach, target specs.Resource) string {
	switch reach.Kind {
	case streamKind:
		// AN EXTERNAL ENGINE IS A DRIVER AND A RELEASE, not a config value —
		// the schema says so where it closes the set. This cloud publishes to
		// the swarm's own gossipsub and has no Kafka or NATS client, so a
		// stream on one is refused by name rather than published into nothing.
		//
		// WHICH ENGINE IS OURS IS THE VOCABULARY'S TO SAY. It was the literal
		// "distributed", which is the schema's `tauEngine` spelled again — so a
		// rename refused every stream in the cloud and blamed the wrong thing.
		//
		// NO `busKind` ARM. A bus has no engine axis at all — the schema says
		// "IT HAS NO ENGINE AXIS, and the absence is the statement" — so this
		// could never fire for one, and a reader had to check the vocabulary to
		// learn that the branch was dead.
		if engine := target.String("engine"); engine != "" && engine != schema.TauEngine() {
			return fmt.Sprintf("%s %q is on the %q engine, and this cloud has no driver for it — "+
				"it publishes to the swarm's own gossipsub and nothing else",
				reach.Kind, reach.Label, engine)
		}
		return ""
	case storageKind:
		// STREAMING IS A DIFFERENT INTERFACE AND THIS CLOUD HAS ONLY ONE. Its
		// verbs move bytes a caller does not hold all of at once, so serving it
		// through the object functions would mean a guest holding a whole
		// stream in memory to write it.
		if model := target.String("type"); model != "" && model != objectStorage {
			return fmt.Sprintf("storage %q is %q, and this cloud serves object storage — "+
				"a streaming store's verbs are not these and half-serving them would mean "+
				"holding a whole stream in a guest's memory",
				reach.Label, model)
		}
		return ""
	case syncKind:
		// A CRDT LOG IS A DIFFERENT LOG, not a slower one: append anywhere, and
		// an order that converges rather than one a single writer decided. This
		// cloud keeps the raft promise and has no convergent log yet,
		// so a target asking for the other one is refused BY NAME — the
		// workload still deploys and still reaches everything else it declared.
		if reach.Kind == syncKind && target.String("type") == logType {
			if c := target.String(logType, "consensus"); c != "" && c != "raft" {
				return fmt.Sprintf("log %q asks for %q consensus, and this cloud serves raft logs — "+
					"a convergent log is a different promise, not a slower one",
					reach.Label, c)
			}
		}
		return ""
	case channelKind:
		// A NAMED PORT IS THE ONE THING A DIAL CANNOT REACH. A channel served
		// by a container may name which of its ports the bytes go to, and the
		// container tier gives an instance exactly one port — the one it puts
		// in $PORT and proxies to. Nothing here resolves a second one, so a
		// channel that names one is refused BY NAME rather than delivered to
		// the wrong socket and timed out.
		//
		// IT IS REFUSED PER TARGET RATHER THAN PER WORKLOAD, which is the whole
		// reason Grant.Refused exists: the project still deploys, everything
		// else in it still runs, and the dial is told why in a sentence.
		if port := target.String("serve", "port"); port != "" {
			return fmt.Sprintf("channel %q is delivered to the port %q, and a container here is reached "+
				"on the one port it was given — nothing resolves a second one",
				reach.Label, port)
		}
		return ""
	case cacheKind:
	default:
		// THE OTHER KINDS ARE NOT REFUSED HERE. What a tier can do with a grant
		// is the tier's to say — a volume is a mount, a database is a
		// connection — and this function only knows the properties a CACHE and
		// a STREAM declare that this cloud would have to serve weaker than
		// written.
		return ""
	}

	// `raft` MEANS NOTHING IS SILENTLY DISCARDED, and the store behind a cache
	// here is a CRDT: concurrent writes merge last-writer-wins and the loser is
	// gone with no error. That is what `crdt` asks for and the
	// opposite of what this asks for, so it is refused rather than served as
	// if the author had written the other value.
	//
	// `gossip` IS ACCEPTED and is served STRONGER than declared: gossip is
	// eventually consistent and lossy, a CRDT is eventually consistent and
	// not. An author who accepted a dropped entry gets one that arrives.
	if consensus := target.String("consensus"); consensus == raftConsensus {
		return fmt.Sprintf("%s %q declares consensus %q, and the store behind a cache here merges "+
			"concurrent writes last-writer-wins — a write that lost would be gone with no error",
			reach.Kind, reach.Label, consensus)
	}
	return ""
}

const raftConsensus = "raft"

// The vocabulary's own compiled keys, so a reader finds the attribute by
// searching the schema for the string.
//
// THAT SENTENCE USED TO BE FALSE, and nothing noticed for as long as it was.
// Three of these were not any attribute's key: `keys/ttl` and `keys/size` where
// the vocabulary declares `ttl` and `size` — `keys` is an InSection, a heading
// in an editor, not a path — and `versioning` where it declares
// `object/versioning`. The far end had copied the same inventions, so writer
// and reader agreed with each other and with nothing else, and what an author
// wrote simply stopped taking effect.
//
// TestEveryEmittedSettingIsAnAttribute now holds every key here to
// schema.IsAttribute, so the next rename is a red test rather than a silent
// default. A key that is composed rather than copied — a handle, a resolved
// scope — is listed in that test as such, with the reason.
const (
	settingTTL       = "ttl"
	settingSize      = "size"
	settingQuota     = "quota"
	settingMountPath = "path"

	// How often a volume is walked into the content-addressed store.
	settingCheckpointEvery = "checkpoint/interval"

	// Where the platform posts an item to a consumer that has no host ABI.
	settingDeliverPort = "deliver/port"
	settingDeliverPath = "deliver/path"

	// The `instances:` block, spelled as pkg/invoke/wasm reads it.
	settingAdmission = "instances/admission"
	settingLimit     = "instances/limit"
	settingValidator = "instances/validator/name"
	settingValidated = "instances/validator/namespace"

	// What a storage declared: which data model, how big, and whether old
	// versions are kept.
	settingStorageType = "type"
	settingStorageSize = "size"
	settingVersioning  = "object/versioning"

	// objectStorage is the one data model this cloud serves.
	objectStorage = "object"

	// settingType is a database's data model, and the prefix every other
	// connection setting is filed under.
	settingType             = "type"
	settingCredentials      = "credentials/name"
	settingCredentialsScope = "credentials/namespace"
)

// cacheSettings is what the TARGET declared, carried to whatever backs the
// kind — which has the handle and not the compiled project.
func cacheSettings(target specs.Resource) map[string]string {
	out := map[string]string{}
	if ttl, ok := target.Duration("ttl"); ok {
		out[settingTTL] = ttl.String()
	}
	if size, ok := target.Int("size"); ok {
		out[settingSize] = strconv.FormatInt(size, 10)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// settingsOf is what a tier needs off the resource that is not common to every
// tier.
//
// THREE THINGS, NAMED, AND NOT THE WHOLE RESOURCE. Passing everything would put
// every access rule and every variable into every Spec, which is a wider thing
// to hold for something that is not read:
//
//	serve/*          how a thing is served — a static site's mode and index.
//	execution/call   the exported symbol a wasm component is entered at.
//	implements       which host interface it was built against.
//
// The last two are the vocabulary's own words. `implements` defaults to
// `handler` in the schema, so a resource that says nothing still says handler;
// this carries what the compile resolved rather than restating the default.
//
// Nothing here is per-environment beyond what the compile already did: this
// reads the compiled object, so a `mode_preview` is already `mode`.
func settingsOf(r specs.Resource) map[string]string {
	out := map[string]string{}

	if block, ok := r.Map("serve"); ok {
		for key, value := range block {
			if s, ok := value.(string); ok {
				out["serve/"+key] = s
			}
		}
	}

	if call := r.String("execution", "call"); call != "" {
		out["execution/call"] = call
	}
	if world := r.String("implements"); world != "" {
		out["implements"] = world
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// servedHere is a kind this service answers rather than hands to a tier.
//
// IT IS NOT A SECOND ENGINE TABLE. `Engines()` says which tier may run a kind
// and the answer for this one is genuinely none — which runtimeOf reports as an
// error because for every other kind it is one. This says the other half: no
// tier runs it BECAUSE this service does, so the tier and the artifact are
// questions that have no answer rather than questions that failed.
func servedHere(kind string) bool { return kind == mcpKind }

// runtimeOf says which tier runs this.
//
// IT ASKS THE VOCABULARY RATHER THAN HOLDING A TABLE. Which tiers a kind may
// run on is `Engines()` on that kind in pkg/tcc/taubyte/v2/schema, and a copy
// of that here would be a second place to add a tier — one of which would be
// forgotten, and the failure would be a workload that will not start on the
// tier its config selected.
func runtimeOf(kind string, r specs.Resource) (string, error) {
	// A kind with more than one tier authors the choice; `worker` is the one
	// that does today.
	if declared := r.String("runtime"); declared != "" {
		return declared, nil
	}

	engines := schema.EnginesOf(kind)
	switch len(engines) {
	case 1:
		return engines[0], nil
	case 0:
		return "", fmt.Errorf("%w: %s", ErrNoRuntime, kind)
	default:
		return "", fmt.Errorf("%w: %s runs on %s", ErrAmbiguous, kind, strings.Join(engines, " or "))
	}
}

// artifactOf reads what to run out of the resource, or asks the build service
// when the resource names a source rather than an artifact.
func (s *Service) artifactOf(ctx context.Context, t Target, r specs.Resource, resourceID string) (string, error) {
	// `source/kind` is the vocabulary's own discriminator, so which branch was
	// taken is read rather than guessed from which field happens to be set.
	switch r.String("source", "kind") {
	case "image":
		return r.String("source", "image"), nil
	case "component":
		return r.String("source", "component"), nil
	case sourceBuiltin:
		// A BUILTIN HAS NO ARTIFACT AND ASKING FOR ONE IS THE BUG. The program
		// is one the runtime ships — a workflow step's driver, an agent's loop
		// — so there was never a build, and falling through to the artifact
		// seam asked the build service for the output of one. That is why every
		// workflow this compiler produced resolved to ErrNoArtifact.
		return "", nil
	}

	if s.artifacts == nil {
		return "", fmt.Errorf("%w: %s/%s", ErrNoArtifact, t.Kind, t.Name)
	}
	return s.artifacts.Artifact(ctx, t.Project, t.Environment, t.Kind, resourceID)
}

// envOf is the run-time half of the variables block.
//
// BUILD-TIME VARIABLES ARE NOT HERE. `at: build` says the value is baked into
// the artifact, and handing one to a running workload would be handing it a
// value that may not be the one it was built with.
func (s *Service) envOf(ctx context.Context, t Target, r specs.Resource) (map[string]string, error) {
	variables := r.Sub("variables")
	if len(variables) == 0 {
		return nil, nil
	}

	out := map[string]string{}
	for _, name := range r.Keys("variables") {
		v := variables.Sub(name)
		if v.String("at") == "build" || v.String("as") != "env" {
			continue
		}

		if id := v.String("secret", "name"); id != "" {
			if s.secrets == nil {
				return nil, fmt.Errorf("%w: %s wants %s", ErrNoSecrets, t.Name, name)
			}
			value, err := s.secrets.Secret(ctx, t.Project, t.Environment, id)
			if err != nil {
				return nil, fmt.Errorf("worker: %s: resolving %s: %w", t.Name, name, err)
			}
			out[name] = value
			continue
		}
		out[name] = v.String("value")
	}
	return out, nil
}

// Resolve is the whole read: a target in, a spec out. It is exported because a
// gateway placing a request, and an operator asking what a name resolves to,
// want exactly this and nothing after it.
func (s *Service) Resolve(ctx context.Context, t Target) (invoke.Spec, error) {
	if t.Project == "" || t.Environment == "" || t.Kind == "" || t.Name == "" {
		return invoke.Spec{}, ErrNoTarget
	}

	proj, pointer, err := s.projects.At(ctx, register.Deployment{Project: t.Project, Environment: t.Environment})
	if err != nil {
		return invoke.Spec{}, err
	}

	resource, resourceID, err := proj.Resource(t.Scope, t.Kind, t.Name)
	if err != nil {
		return invoke.Spec{}, err
	}

	return s.spec(ctx, t, pointer, proj, resource, resourceID)
}

// mcpSettings is what an mcp declares about its listing.
func mcpSettings(r specs.Resource) map[string]string {
	out := map[string]string{}
	if scope := r.String("tools", "cache_scope"); scope != "" {
		out[settingMCPCacheScope] = scope
	}
	if ttl, ok := r.Duration("tools", "cache_ttl"); ok {
		out[settingMCPCacheTTL] = ttl.String()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
