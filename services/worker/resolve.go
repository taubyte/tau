package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/register"
	"github.com/taubyte/tau/pkg/specs"
	schema "github.com/taubyte/tau/pkg/tcc/taubyte/v2/schema"
)

//	(project, environment)            -> what it runs   [projects.Cache]
//	(scope, kind, name)               -> a resource     [an index hit]
//	resource                          -> an invoke.Spec [read off the vocabulary]
//
// The first step is projects.Cache, shared with the gateway, so deploy propagation
// is one promise.

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

// Artifacts answers what a built package currently is. A workload runs its package,
// so it is always asked about under kind `package` with the package's own id. An image
// names its artifact and a builtin has none. Absent, a built package is refused with
// ErrNoArtifact.
type Artifacts interface {
	Artifact(ctx context.Context, project, environment, kind, resourceID string) (string, error)
}

// Secrets resolves a secret a workload reads through its vault. A missing secret is a
// refusal, not an empty string.
type Secrets interface {
	Secret(ctx context.Context, project, environment, secretID string) (string, error)
}

// spec reads a resource and says what to run. Every value comes off the compiled
// resource: `execution/memory` and `execution/timeout` are PerEnv.
// specOf is everything in t's spec but its artifact, a function of the compiled
// project alone (resolved.go), and the package the artifact is asked about.
func specOf(t Target, home []string, p register.Pointer, proj *specs.Project, r specs.Resource, resourceID string) (resolvedSpec, error) {
	// A kind this service answers itself has no runtime or artifact; asking for them
	// would refuse it before Invoke's own branch.
	workload := specs.ReadWorkloadBlock(r)
	var runtime string
	var pkg ranPackage
	if !servedHere(t.Kind) {
		var err error
		if pkg, err = packageOf(t, proj, workload); err != nil {
			return resolvedSpec{}, err
		}
		if runtime, err = runtimeOf(t.Kind, pkg.Package); err != nil {
			return resolvedSpec{}, err
		}
	}

	env := envOf(workload.Variables)

	reaches, err := reachesOf(t, proj, r)
	if err != nil {
		return resolvedSpec{}, err
	}

	spec := invoke.Spec{
		Runtime: runtime,
		// The scope the workload lives in is in it: an id is derived from the bare
		// name, so two workloads of one name in two namespaces share it, and the pool
		// keeps warm instances by this name. Where it lives, not who asked: one
		// reached from a child scope is still one.
		Name:     strings.Join(slices.Concat([]string{t.Project, t.Environment}, home, []string{t.Kind, resourceID}), "/"),
		Revision: string(p.Object),
		Env:      env,
		Reaches:  reaches,

		// Identity comes off where the workload lives, not what the caller asked for.
		Project:     t.Project,
		Environment: t.Environment,
		Kind:        t.Kind,
		Label:       workload.Name,
		Completes:   schema.CompletesOf(t.Kind),
	}
	if _, scope, _, ok := proj.Locate(resourceID); ok {
		spec.Scope = scope
	}
	spec.Memory = workload.ExecutionMemory
	spec.Ports = portsOf(workload.Ports)
	spec.Timeout = workload.ExecutionTimeout
	if concurrency := workload.LimitsConcurrency; concurrency != nil {
		spec.Concurrency = int(*concurrency)
	}
	// Beside the artifact, so a tier reads one field to know which it has.
	spec.Builtin = pkg.Source.Builtin

	spec.Settings = settingsOf(workload)

	// An mcp runs no engine, so its own fields travel here.
	if t.Kind == mcpKind {
		spec.Settings = merged(spec.Settings, mcpSettings(specs.ReadMcp(r).Tools))
	}

	// Egress is the resource's; an absent list is the node-wide floor alone. The tier
	// enforces it.
	spec.Egress = workload.Egress

	// Carried where a tier honours it, refused by name where none does; a kind served
	// here runs on no tier.
	if runtime != "" {
		if err := appliedWith(&spec, t, proj, r, pkg, workload); err != nil {
			return resolvedSpec{}, err
		}
	}
	return resolvedSpec{spec: spec, pkg: pkg}, nil
}

// reachesOf turns the access block into grants. A label is scoped, so it is resolved
// here and a handle (the id prefixed by its deployment) travels. A target this cloud
// cannot serve is a refused grant, not a refused workload: everything else the
// resource reaches still works.
func reachesOf(t Target, proj *specs.Project, r specs.Resource) ([]invoke.Grant, error) {
	var out []invoke.Grant

	// Every kind, not just those a tier serves: a tier must see what it cannot do.
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

			// A locally served model is a workload, located here where the project is open.
			if reach.Kind == modelKind {
				grant.Settings = merged(grant.Settings, localModelScope(proj, target, id))
			}

			// Starting a workflow is sending to its first step's queue, recorded by the lowering
			// as `entry/start`.
			if reach.Kind == workflowKind && contains(reach.Ops, opStart) {
				if err := startedBy(proj, t, target, id, &grant); err != nil {
					return nil, err
				}
			}

			// Dialling a channel is invoking what serves it, resolved here.
			if reach.Kind == channelKind {
				servedBy(proj, t, target, id, &grant)
			}

			// A tool's name is the author's label, which may name a function elsewhere, so the
			// resolved address travels.
			if reach.Kind == functionKind && contains(reach.Ops, opInvoke) {
				invokedAt(proj, target, id, &grant)
			}

			// A guard is cross-kind, so it is not in settingsFor.
			guardedBy(proj, t, target, id, &grant)

			out = append(out, grant)
		}
	}
	return out, nil
}

const (
	cacheKind    = specs.KindCache
	volumeKind   = specs.KindVolume
	streamKind   = specs.KindStream
	storageKind  = specs.KindStorage
	workflowKind = specs.KindWorkflow
	busKind      = specs.KindBus
	databaseKind = specs.KindDatabase
	channelKind  = specs.KindChannel
	modelKind    = specs.KindModel

	// syncKind is one kind with two primitives under it; the type selects which,
	// and settingSyncType is how that travels to a tier.
	syncKind        = specs.KindSync
	queueType       = specs.SyncTypeQueue
	logType         = specs.SyncTypeLog
	settingSyncType = specs.SyncKeyType
)

// portsOf is what this workload binds inside its container, by name. Off-the-shelf
// images bind fixed ports.
func portsOf(declared map[string]specs.ApplicationPortsEntry) map[string]int {
	if len(declared) == 0 {
		return nil
	}

	out := make(map[string]int, len(declared))
	for name, port := range declared {
		if port.Number > 0 {
			out[name] = int(port.Number)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// merged overlays two setting maps, nil for neither. Their keys are disjoint by
// construction, so a collision is a bug.
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

// startedBy fills in where a `start` on a workflow lands, recorded on the shell as
// an ordinary value. The queue lives inside the workflow, a scope that is not a
// namespace.
func startedBy(proj *specs.Project, t Target, workflow specs.Resource, id string, grant *invoke.Grant) error {
	entry := specs.ReadWorkflow(workflow).Entry[workflowEntryStart]
	kind, name := entry.Kind, entry.Name
	if kind == "" || name == "" {
		// A workflow whose shell carries no entry is a compiled object that did
		// not come from tcc; starting it would mean guessing.
		grant.Refused = fmt.Sprintf("workflow %q records no entry point, so there is nowhere for a start to land", grant.Label)
		return nil
	}

	// Where the workflow is, which may be a parent of the asker's scope.
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

	// The start queue's settings travel with it, so a `start` and the dispatcher open the
	// queue with one configuration.
	grant.Settings = merged(grant.Settings, syncSettings(specs.Reach{}, start))
	grant.Settings = merged(grant.Settings, map[string]string{
		settingStartKind:   kind,
		settingStartHandle: specs.Handle(t.Project, t.Environment, startID),
		settingStartAs:     entry.As,
	})
	return nil
}

// The keys the shell's entry record becomes. Two are borrowed from invoke.StartGrant,
// which reads them back.
const (
	opStart = "start"

	// workflowEntryStart is the entry the lowering records a workflow's start under; the
	// name is the lowering's, not an attribute.
	workflowEntryStart = "start"

	settingStartKind   = invoke.SettingStartKind
	settingStartHandle = invoke.SettingStartHandle
	settingStartAs     = "entry/start/as"
)

// servedBy fills in where a `dial` on a channel lands, resolving the serving name
// here so no backing needs the register on the request path. The scope is the chain
// specs.ScopeOf produces. A name resolving to nothing refuses only this grant (Ref()
// catches it at compile for tcc output).
func servedBy(proj *specs.Project, t Target, channel specs.Resource, id string, grant *invoke.Grant) {
	declared := specs.ReadChannel(channel)
	kind, name := declared.Serve.Kind, declared.Serve.Name
	if kind == "" || name == "" {
		grant.Refused = fmt.Sprintf("channel %q names nothing that serves it, so a dial has nowhere to land", grant.Label)
		return
	}

	// Where the channel is. Its scope, not scope plus itself as in startedBy: a channel
	// is an ordinary resource, a workflow is a scope.
	_, scope, _, ok := proj.Locate(id)
	if !ok {
		grant.Refused = fmt.Sprintf("channel %q could not be located in this project", grant.Label)
		return
	}

	at := specs.ScopeOf(scope, declared.Serve.Namespace)
	if _, _, err := proj.Resource(at, kind, name); err != nil {
		grant.Refused = fmt.Sprintf("channel %q is served by %s/%s, which is not in this project: %s", grant.Label, kind, name, err)
		return
	}

	grant.Settings = merged(grant.Settings, map[string]string{
		settingProtocol:   declared.Protocol,
		settingServeKind:  kind,
		settingServeName:  name,
		settingServeScope: "/" + strings.Join(at, "/"),
		settingServePort:  declared.Serve.Port,
	})
}

// What a channel's own block becomes. The namespace travels RESOLVED, as the
// chain the server was found in, under the attribute's own key.
const (
	settingProtocol   = specs.ChannelKeyProtocol
	settingServeKind  = specs.ChannelServeKeyKind
	settingServeName  = specs.ChannelServeKeyName
	settingServeScope = specs.ChannelServeKeyNamespace
	settingServePort  = specs.ChannelServeKeyPort
)

// invokedAt records which function an `invoke` grant resolved to, since an mcp looks
// a tool up by label and needs the address. Not refused when unlocatable:
// proj.Reached already resolved the grant.
func invokedAt(proj *specs.Project, target specs.Resource, id string, grant *invoke.Grant) {
	_, scope, _, ok := proj.Locate(id)
	if !ok {
		return
	}

	grant.Settings = merged(grant.Settings, map[string]string{
		settingInvokeName:  specs.ReadFunction(target).Name,
		settingInvokeScope: "/" + strings.Join(scope, "/"),
	})
}

// Where an `invoke` grant's target actually lives.
const (
	settingInvokeName  = "invoke/name"
	settingInvokeScope = "invoke/namespace"
)

// guardedBy fills in who decides what this caller may do to the target, resolved
// here as servedBy does. The scope is the target's, not the caller's. A guard
// resolving to nothing is refused, so an unrunnable guard never reads as unguarded.
func guardedBy(proj *specs.Project, t Target, target specs.Resource, id string, grant *invoke.Grant) {
	guardian := specs.ReadAuthorizer(target)
	name := guardian.Name
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

	at := specs.ScopeOf(scope, guardian.Namespace)
	guard, _, err := proj.Resource(at, specs.KindFunction, name)
	if err != nil {
		grant.Refused = fmt.Sprintf("%s %q is guarded by the function %q, which is not in this project: %s",
			grant.Kind, grant.Label, name, err)
		return
	}

	// It must be an authorizer: a handler-world function would read the question as a
	// request.
	if world := specs.ReadFunction(guard).Implements; world != wasm.WorldAuthorizer {
		grant.Refused = fmt.Sprintf("%s %q is guarded by %q, which implements %q rather than an authorizer",
			grant.Kind, grant.Label, name, world)
		return
	}

	grant.Settings = merged(grant.Settings, map[string]string{
		settingAuthorizer:      name,
		settingAuthorizerScope: "/" + strings.Join(at, "/"),
	})
}

// What a resource's own `authorizer:` block becomes, under the keys
// pkg/invoke/wasm reads it by. The namespace travels RESOLVED.
const (
	settingAuthorizer      = specs.AuthorizerKeyName
	settingAuthorizerScope = specs.AuthorizerKeyNamespace
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
	case streamKind:
		return streamSettings(target)
	case mcpToolKind:
		// A tool needs a description (see mcp.go); it travels for every function.
		if d := specs.ReadFunction(target).Description; d != "" {
			return map[string]string{settingDescription: d}
		}
	}
	return nil
}

// localModelScope is the namespace chain holding the workload serving a local model:
// the model's scope and then the declared namespace.
func localModelScope(proj *specs.Project, target specs.Resource, id string) map[string]string {
	inference := specs.ReadModel(target).Inference
	if inference.Kind != specs.ModelInferenceKindLocal {
		return nil
	}

	_, scope, _, ok := proj.Locate(id)
	if !ok {
		return nil
	}

	resolved := specs.ScopeOf(scope, inference.LocalNamespace)
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

// modelSettings carries where inference happens and how it is called. The credential
// stays a reference, resolved by the vault at the call. Parameters travel because
// the host clamps a guest's request to them (`max_tokens`).
func modelSettings(target specs.Resource) map[string]string {
	model := specs.ReadModel(target)
	out := map[string]string{}
	put := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}

	put(specs.ModelKeyIdentifier, model.Identifier)
	put(specs.ModelKeyFormat, model.Format)

	// The branch first, then its fields, so a backing need not infer the shape.
	put(specs.ModelInferenceKeyKind, model.Inference.Kind)
	put(specs.ModelInferenceKeyRemoteEndpoint, model.Inference.RemoteEndpoint)
	put(specs.ModelInferenceKeyRemoteCredentialsName, model.Inference.RemoteCredentialsName)
	put(specs.ModelInferenceKeyRemoteCredentialsNamespace, model.Inference.RemoteCredentialsNamespace)
	put(specs.ModelInferenceKeyLocalName, model.Inference.LocalName)
	put(specs.ModelInferenceKeyLocalNamespace, model.Inference.LocalNamespace)
	put(specs.ModelInferenceKeyLocalPort, model.Inference.LocalPort)

	parameters := model.Parameters
	if t := parameters.Temperature; t != nil {
		out[specs.ModelParametersKeyTemperature] = strconv.FormatFloat(*t, 'g', -1, 64)
	}
	if t := parameters.TopP; t != nil {
		out[specs.ModelParametersKeyTopP] = strconv.FormatFloat(*t, 'g', -1, 64)
	}
	if n := parameters.MaxTokens; n != nil {
		out[specs.ModelParametersKeyMaxTokens] = strconv.FormatInt(*n, 10)
	}
	if stop := parameters.Stop; len(stop) > 0 {
		// One key, newline-separated: a stop sequence is an exact string and a newline is the
		// one character it cannot usefully be.
		out[specs.ModelParametersKeyStop] = strings.Join(stop, "\n")
	}

	if len(out) == 0 {
		return nil
	}
	return out
}

// syncSettings carries what a coordination resource declares. The type travels first,
// or a tier filtering on kind would hand a log's grant to the queue interface. Other
// keys are attribute paths beginning with the type. The dead letter compiles to
// another queue's id.
func syncSettings(reach specs.Reach, target specs.Resource) map[string]string {
	declared := specs.ReadSync(target)
	if declared.Type == "" {
		return nil
	}
	out := map[string]string{settingSyncType: declared.Type}
	duration := func(key string, d *time.Duration) {
		if d != nil {
			out[key] = d.String()
		}
	}

	// What happens to two concurrent writes; a tier that cannot honour it refuses by name.
	switch declared.Type {
	case logType:
		log := declared.Log
		if log.Consensus != "" {
			out[specs.SyncLogKeyConsensus] = log.Consensus
		}
		duration(specs.SyncLogKeyRetentionAge, log.RetentionAge)
		if n := log.RetentionSize; n != nil {
			out[specs.SyncLogKeyRetentionSize] = strconv.FormatInt(*n, 10)
		}
		return out
	case queueType:
	default:
		return out
	}

	queue := declared.Queue
	if queue.Consensus != "" {
		out[specs.SyncQueueKeyConsensus] = queue.Consensus
	}
	duration(specs.SyncQueueKeyVisibilityTimeout, queue.VisibilityTimeout)
	duration(specs.SyncQueueKeyDelay, queue.Delay)
	duration(specs.SyncQueueKeyRetryInterval, queue.RetryInterval)
	duration(specs.SyncQueueKeyRetryCeiling, queue.RetryCeiling)
	if n := queue.MaxAttempts; n != nil {
		out[specs.SyncQueueKeyMaxAttempts] = strconv.FormatInt(*n, 10)
	}
	// `retry/factor` is a Float, and 1.5 is legal.
	if f := queue.RetryFactor; f != nil {
		out[specs.SyncQueueKeyRetryFactor] = strconv.FormatFloat(*f, 'g', -1, 64)
	}
	if name := queue.DeadLetterName; name != "" {
		out[specs.SyncQueueKeyDeadLetterName] = name
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

// storageSettings carries the data model and the ceiling; the size lives under the
// type's block.
func storageSettings(target specs.Resource) map[string]string {
	storage := specs.ReadStorage(target)
	if storage.Type == "" {
		return nil
	}

	// The compiled key is not the authored path: `size` compiles flat and `versioning`
	// stays nested under `object`. The size is required.
	out := map[string]string{
		settingStorageType: storage.Type,
		settingStorageSize: strconv.FormatInt(storage.Size, 10),
	}
	if versioning := storage.Object.Versioning; versioning != nil && *versioning {
		out[settingVersioning] = "true"
	}
	return out
}

// databaseSettings carries the data model and, from the block that type names, the
// connection.
func databaseSettings(target specs.Resource) map[string]string {
	database := specs.ReadDatabase(target)
	if database.Type == "" {
		return nil
	}

	// `dimensions` and `metric` are a vector's, so they travel as settings.
	out := map[string]string{settingType: database.Type}
	put := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}
	// A width is an Int in the vocabulary, not a string.
	number := func(key string, n *int64) {
		if n != nil {
			out[key] = strconv.FormatInt(*n, 10)
		}
	}
	// `consensus` travels because a node with no database service still opens one, and
	// must refuse a raft ledger rather than merge it as crdt. `size` travels so the
	// engine can enforce it. The credential stays a reference, resolved by the vault on
	// connection.
	switch database.Type {
	case specs.DatabaseTypeSql:
		sql := database.Sql
		put(specs.DatabaseSqlKeyEngine, sql.Engine)
		put(specs.DatabaseSqlKeyHost, sql.Host)
		put(specs.DatabaseSqlKeyDatabase, sql.Database)
		put(specs.DatabaseSqlKeyTls, sql.Tls)
		put(specs.DatabaseSqlKeyConsensus, sql.Consensus)
		number(specs.DatabaseSqlKeySize, sql.Size)
		put(specs.DatabaseSqlKeyCredentialsName, sql.CredentialsName)
		put(specs.DatabaseSqlKeyCredentialsNamespace, sql.CredentialsNamespace)
	case specs.DatabaseTypeDocument:
		document := database.Document
		put(specs.DatabaseDocumentKeyEngine, document.Engine)
		put(specs.DatabaseDocumentKeyHost, document.Host)
		put(specs.DatabaseDocumentKeyDatabase, document.Database)
		put(specs.DatabaseDocumentKeyTls, document.Tls)
		put(specs.DatabaseDocumentKeyConsensus, document.Consensus)
		number(specs.DatabaseDocumentKeySize, document.Size)
		put(specs.DatabaseDocumentKeyCredentialsName, document.CredentialsName)
		put(specs.DatabaseDocumentKeyCredentialsNamespace, document.CredentialsNamespace)
	case specs.DatabaseTypeKv:
		kv := database.Kv
		put(specs.DatabaseKvKeyEngine, kv.Engine)
		put(specs.DatabaseKvKeyConsensus, kv.Consensus)
		number(specs.DatabaseKvKeySize, kv.Size)
	case specs.DatabaseTypeVector:
		vector := database.Vector
		put(specs.DatabaseVectorKeyEngine, vector.Engine)
		put(specs.DatabaseVectorKeyConsensus, vector.Consensus)
		number(specs.DatabaseVectorKeySize, vector.Size)
		number(specs.DatabaseVectorKeyDimensions, vector.Dimensions)
		put(specs.DatabaseVectorKeyMetric, vector.Metric)
	}
	return out
}

// volumeSettings carries the quota and, from the entry, the mount path, which is per
// use.
func volumeSettings(reach specs.Reach, target specs.Resource) map[string]string {
	volume := specs.ReadVolume(target)

	// THE QUOTA IS REQUIRED, so a compiled volume always carries one.
	out := map[string]string{settingQuota: strconv.FormatInt(volume.Quota, 10)}

	// The author's checkpoint interval, or the node falls back to its own default.
	if every := volume.Checkpoint.Interval; every != nil {
		out[settingCheckpointEvery] = every.String()
	}
	if path := reach.Path; path != "" {
		out[settingMountPath] = path
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

	// A narrowed tool grant is an authorization bound nothing here applies, so it fails
	// closed rather than serving every tool. Enforcing it belongs at one tool-call
	// admission point.
	if len(reach.Tools) > 0 {
		return fmt.Sprintf("%s %q is narrowed to the tools %v, and this cloud cannot apply that narrowing yet — "+
			"served, it would reach every tool the server offers",
			reach.Kind, reach.Label, reach.Tools)
	}

	// An `instances:` block is honoured by the tier (pkg/invoke/wasm/instance.go); its
	// settings are merged in reachesOf.
	return ""
}

// instanceSettings carries the cross-kind `instances:` block. The admission switch is
// a Captures() attribute, so its presence is the declaration.
func instanceSettings(target specs.Resource) map[string]string {
	instances := specs.ReadInstances(target)
	if instances.Admission == "" {
		return nil
	}

	out := map[string]string{settingAdmission: instances.Admission}
	if name := instances.ValidatorName; name != "" {
		out[settingValidator] = name
	}
	if namespace := instances.ValidatorNamespace; namespace != "" {
		out[settingValidated] = namespace
	}
	// A limit of zero is an answer, not an absence.
	if limit := instances.Limit; limit != nil {
		out[settingLimit] = strconv.FormatInt(*limit, 10)
	}
	return out
}

// unservableKind is what one kind declares that this cloud would serve weaker than
// written.
func unservableKind(reach specs.Reach, target specs.Resource) string {
	switch reach.Kind {
	case volumeKind:
		// Nothing measures a volume's last snapshot against a budget, so a budget is refused.
		checkpoint := specs.ReadVolume(target).Checkpoint
		if budget := checkpoint.RpoBudget; budget != nil {
			return fmt.Sprintf("%s %q declares an rpo_budget of %s, and nothing here measures a volume's last snapshot "+
				"against one — it would never %s when the budget was passed",
				reach.Kind, reach.Label, budget, checkpoint.OnBudgetExceeded)
		}
		return ""
	case workflowKind:
		return unservableWorkflow(reach, specs.ReadWorkflow(target))
	case storageKind:
		// Streaming storage is a different interface, which this cloud does not have.
		if model := specs.ReadStorage(target).Type; model != "" && model != objectStorage {
			return fmt.Sprintf("storage %q is %q, and this cloud serves object storage — "+
				"a streaming store's verbs are not these and half-serving them would mean "+
				"holding a whole stream in a guest's memory",
				reach.Label, model)
		}
		return ""
	case syncKind:
		// A CRDT log is a different log (convergent order); this cloud has only raft logs, so
		// it is refused by name and the workload still deploys.
		if declared := specs.ReadSync(target); declared.Type == logType {
			if c := declared.Log.Consensus; c != "" && c != specs.SyncLogConsensusRaft {
				return fmt.Sprintf("log %q asks for %q consensus, and this cloud serves raft logs — "+
					"a convergent log is a different promise, not a slower one",
					reach.Label, c)
			}
		}
		return ""
	case channelKind:
		// A named port is delivered to: the container tier keeps every declared port.
		return ""
	case cacheKind:
	default:
		// What a tier can do with other kinds is the tier's to say.
		return ""
	}

	// `raft` means nothing is silently discarded, and a cache here is a last-writer-wins
	// CRDT, so it is refused. `gossip` is served stronger than declared.
	if consensus := specs.ReadCache(target).Consensus; consensus == specs.CacheConsensusRaft {
		return fmt.Sprintf("%s %q declares consensus %q, and the store behind a cache here merges "+
			"concurrent writes last-writer-wins — a write that lost would be gone with no error",
			reach.Kind, reach.Label, consensus)
	}

	// Nothing encrypts cache entries with the named key, so every peer would hold
	// plaintext.
	if key := specs.ReadCache(target).Encryption; key.KeyName != "" {
		return fmt.Sprintf("%s %q is encrypted with the secret %s%s, and a cache here stores its entries as written — "+
			"every peer holding a copy would hold them in the clear",
			reach.Kind, reach.Label, key.KeyName, inNamespace(key.KeyNamespace))
	}
	return ""
}

// unservableWorkflow is what a workflow declares that its drivers do not do. A driver
// hands the item's bytes to the step's function and sends the answer on; nothing
// parses the state, so selections and named results cannot be honoured, and nothing
// counts runs in flight.
func unservableWorkflow(reach specs.Reach, workflow specs.Workflow) string {
	if most := workflow.Concurrency; most != nil {
		return fmt.Sprintf("%s %q allows %d runs in flight, and nothing here counts a workflow's runs — "+
			"every start would be admitted", reach.Kind, reach.Label, *most)
	}
	for _, step := range workflow.Steps {
		if input := step.Call.Input; input != "" {
			return fmt.Sprintf("%s %q hands step %q the input %q, and a step here is handed the whole run's state — "+
				"nothing evaluates a selection of it", reach.Kind, reach.Label, step.Name, input)
		}
		if result := step.Call.Result; result != "" {
			return fmt.Sprintf("%s %q keeps step %q's answer as %q, and a step's answer here replaces the run's state — "+
				"nothing keeps it under a name", reach.Kind, reach.Label, step.Name, result)
		}
	}
	return ""
}

// The vocabulary's own compiled keys. TestEveryEmittedSettingIsAnAttribute holds each
// to schema.IsAttribute; composed keys are listed there with the reason.
const (
	settingTTL   = specs.CacheKeyTtl
	settingSize  = specs.CacheKeySize
	settingQuota = specs.VolumeKeyQuota

	// Where a volume appears inside the workload: the access entry's, which is handed to
	// Reaches rather than generated.
	settingMountPath = "path"

	// How often a volume is walked into the content-addressed store.
	settingCheckpointEvery = specs.VolumeCheckpointKeyInterval

	// Where the platform posts an item to a consumer that has no host ABI.
	settingDeliverPort = "deliver/port"
	settingDeliverPath = "deliver/path"

	// The `instances:` block, under the keys pkg/invoke/wasm reads it by.
	settingAdmission = specs.InstancesKeyAdmission
	settingLimit     = specs.InstancesKeyLimit
	settingValidator = specs.InstancesKeyValidatorName
	settingValidated = specs.InstancesKeyValidatorNamespace

	// What a storage declared: which data model, how big, and whether old
	// versions are kept.
	settingStorageType = specs.StorageKeyType
	settingStorageSize = specs.StorageKeySize
	settingVersioning  = specs.StorageObjectKeyVersioning

	// objectStorage is the one data model this cloud serves.
	objectStorage = specs.StorageTypeObject

	// settingType is a database's data model, and the prefix every other
	// connection setting is filed under.
	settingType = specs.DatabaseKeyType
)

// cacheSettings is what the TARGET declared, carried to whatever backs the
// kind — which has the handle and not the compiled project.
func cacheSettings(target specs.Resource) map[string]string {
	cache := specs.ReadCache(target)

	// THE SIZE IS REQUIRED, so a compiled cache always carries one.
	out := map[string]string{settingSize: strconv.FormatInt(cache.Size, 10)}
	if ttl := cache.Ttl; ttl != nil {
		out[settingTTL] = ttl.String()
	}
	return out
}

// settingsOf is what a tier needs off the resource beyond what every tier gets:
//
//	serve/*          what a served block points at: its kind, name, namespace and port.
//	execution/call   the exported symbol a wasm component is entered at.
//	implements       which host interface it was built against.
//
// It reads the compiled object, so per-environment values are already resolved.
func settingsOf(w specs.WorkloadBlock) map[string]string {
	out := map[string]string{}
	put := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}

	put(specs.WorkloadBlockKeyServeKind, w.ServeKind)
	put(specs.WorkloadBlockKeyServeName, w.ServeName)
	put(specs.WorkloadBlockKeyServeNamespace, w.ServeNamespace)
	put(specs.WorkloadBlockKeyServePort, w.ServePort)

	put(specs.WorkloadBlockKeyExecutionCall, w.ExecutionCall)
	put(specs.WorkloadBlockKeyImplements, w.Implements)

	if len(out) == 0 {
		return nil
	}
	return out
}

// servedHere is a kind this service answers rather than hands to a tier, so tier and
// artifact have no answer rather than failing.
func servedHere(kind string) bool { return kind == mcpKind }

// ranPackage is what a workload runs, read off the package it names.
type ranPackage struct {
	specs.Package
	ID string
}

// packageOf reads the package a workload names. The reference compiled to the
// package's id, so it is found by id rather than by a name looked up again.
func packageOf(t Target, proj *specs.Project, w specs.WorkloadBlock) (ranPackage, error) {
	id := w.PackageName
	if id == "" {
		return ranPackage{}, fmt.Errorf("%w: %s/%s names no package", ErrNoRuntime, t.Kind, t.Name)
	}
	kind, scope, name, ok := proj.Locate(id)
	if !ok || kind != specs.KindPackage {
		return ranPackage{}, fmt.Errorf("%w: %s/%s names the package %s, which this deployment does not hold", ErrNoRuntime, t.Kind, t.Name, id)
	}
	r, _, err := proj.Resource(scope, specs.KindPackage, name)
	if err != nil {
		return ranPackage{}, err
	}
	return ranPackage{Package: specs.ReadPackage(r), ID: id}, nil
}

// runtimeOf is the tier that runs the package, which is its type, refused where the
// vocabulary's Engines() says the kind cannot run it.
func runtimeOf(kind string, pkg specs.Package) (string, error) {
	engines := schema.EnginesOf(kind)
	if len(engines) == 0 {
		return "", fmt.Errorf("%w: %s", ErrNoRuntime, kind)
	}
	if !slices.Contains(engines, pkg.Type) {
		return "", fmt.Errorf("%w: a %s runs on %s, and the package %s is of type %q", ErrNoRuntime, kind, strings.Join(engines, " or "), pkg.Name, pkg.Type)
	}
	return pkg.Type, nil
}

// artifactOf is what the package is: an image names itself, a builtin ships with the
// runtime, and anything built is the plm service's to say.
func (s *Service) artifactOf(ctx context.Context, t Target, pkg ranPackage) (string, error) {
	// `source/kind` is the vocabulary's own discriminator, so which branch was
	// taken is read rather than guessed from which field happens to be set.
	switch pkg.Source.Kind {
	case specs.PackageSourceKindImage:
		return pkg.Source.Image, nil
	case specs.PackageSourceKindBuiltin:
		return "", nil
	}
	if s.artifacts == nil {
		return "", fmt.Errorf("%w: package %s", ErrNoArtifact, pkg.Name)
	}
	return s.artifacts.Artifact(ctx, t.Project, t.Environment, specs.KindPackage, pkg.ID)
}

// envOf is the variables block delivered as environment. A secret is never one: the
// workload reads it through its vault.
func envOf(variables map[string]specs.VariablesEntry) map[string]string {
	out := map[string]string{}
	for name, v := range variables {
		if v.As == specs.VariablesEntryAsEnv {
			out[name] = v.Value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Resolve is the whole read: a target in, a spec out, exported for a gateway placing
// a request and an operator asking what a name resolves to.
func (s *Service) Resolve(ctx context.Context, t Target) (invoke.Spec, error) {
	if t.Project == "" || t.Environment == "" || t.Kind == "" || t.Name == "" {
		return invoke.Spec{}, ErrNoTarget
	}

	deployment := register.Deployment{Project: t.Project, Environment: t.Environment}
	proj, pointer, err := s.projects.At(ctx, deployment)
	if err != nil {
		return invoke.Spec{}, err
	}

	key := keyOf(t)
	if held, ok := s.resolutions.get(deployment, pointer.Object, key); ok {
		return s.resolved(ctx, t, held)
	}

	resource, resourceID, home, err := proj.Found(t.Scope, t.Kind, t.Name)
	if err != nil {
		return invoke.Spec{}, err
	}
	held, err := specOf(t, home, pointer, proj, resource, resourceID)
	if err != nil {
		return invoke.Spec{}, err
	}
	s.resolutions.put(deployment, pointer.Object, key, held)
	return s.resolved(ctx, t, held)
}

// mcpSettings is what an mcp declares about its listing.
func mcpSettings(tools specs.McpTools) map[string]string {
	out := map[string]string{}
	if scope := tools.CacheScope; scope != "" {
		out[settingMCPCacheScope] = scope
	}
	if ttl := tools.CacheTtl; ttl != nil {
		out[settingMCPCacheTTL] = ttl.String()
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
