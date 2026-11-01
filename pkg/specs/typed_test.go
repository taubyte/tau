package specs_test

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/specs"
)

// typed_test reads the vocabulary's own compiled fixture through the generated
// types, one test per thing a flat reader could not say: an entry, a value that
// may be absent, a block the compiler wrote on the author's behalf, a type's own
// branch, and the constants a consumer compares against.

func resource(t *testing.T, scope []string, kind, name string) specs.Resource {
	t.Helper()
	r, _, err := load(t).Resource(scope, kind, name)
	require.NoError(t, err)
	return r
}

func TestEntriesAreReadPerEntry(t *testing.T) {
	api := specs.ReadApplication(resource(t, nil, specs.KindApplication, "api"))
	require.Equal(t, int64(8080), api.Ports["http"].Number)
	require.Equal(t, int64(9090), api.Ports["metrics"].Number)

	require.Equal(t, "info", api.Variables["LOG_LEVEL"].Value)
	require.Equal(t, specs.VariablesEntryAtRun, api.Variables["LOG_LEVEL"].At)
	require.Equal(t, specs.VariablesEntryAsEnv, api.Variables["LOG_LEVEL"].As)
	stripe := api.Variables["STRIPE_KEY"]
	require.True(t, stripe.Secret.Declared)
	require.NotEmpty(t, stripe.Secret.Name, "a reference compiles to an id")
	require.False(t, api.Variables["REGION"].Secret.Declared)

	shop := specs.ReadGateway(resource(t, nil, specs.KindGateway, "shop"))
	require.Len(t, shop.Transformers, 2)
	require.Equal(t, "redact", shop.Transformers[0].Name)
	require.Equal(t, specs.TransformersEntryFailClosed, shop.Transformers[0].Fail)
	require.Equal(t, "tier_header", shop.Transformers[1].Name)
	require.Equal(t, "/shop", shop.Transformers[1].Namespace)
	require.Equal(t, specs.TransformersEntryFailOpen, shop.Transformers[1].Fail)

	pg := specs.ReadProxy(resource(t, nil, specs.KindProxy, "postgres"))
	require.Len(t, pg.Transformers, 1, "the proxy reads the same entry type")

	wf := specs.ReadWorkflow(resource(t, nil, specs.KindWorkflow, "checkout"))
	require.Equal(t, "reserve", wf.Start)
	require.Len(t, wf.Steps, 3)
	reserve := wf.Steps[0]
	require.Equal(t, "reserve", reserve.Name)
	require.Equal(t, "reserve_stock", reserve.Call.Function)
	require.Equal(t, "order.lines", reserve.Call.Input)
	require.Equal(t, "reservation", reserve.Call.Result)
	require.Equal(t, int64(4), *reserve.Attempts)
	require.Equal(t, "5s", reserve.Backoff.Interval)
	require.Equal(t, 2.0, *reserve.Backoff.Factor)
	require.Equal(t, "1m", reserve.Backoff.Ceiling)
	require.True(t, reserve.Backoff.Declared)

	settle := wf.Steps[1]
	require.Equal(t, "30s", settle.Wait)
	require.False(t, settle.Call.Declared, "a wait calls nothing")
	require.False(t, settle.Backoff.Declared)
	require.Nil(t, settle.Backoff.Factor)

	// WHAT A LOWERING WRITES IS DECLARED, so it reads like anything else.
	start := wf.Entry["start"]
	require.Equal(t, specs.KindSync, start.Kind)
	require.Equal(t, "checkout_reserve", start.Name)
	require.Equal(t, "send", start.As)

	env := specs.ReadEnvironments(specs.Resource{"branches": []any{"main"}, "shadows": map[string]any{
		"preview": map[string]any{"branches": []any{"feat"}},
	}})
	require.Equal(t, []string{"feat"}, env.Shadows["preview"].Branches)
}

func TestAbsentIsNotZero(t *testing.T) {
	db := specs.ReadApplication(resource(t, nil, specs.KindApplication, "db"))
	require.Equal(t, int64(2000), *db.Execution.Cpu)
	api := specs.ReadApplication(resource(t, nil, specs.KindApplication, "api"))
	require.Nil(t, api.Execution.Cpu, "no cpu was written")
	require.Equal(t, int64(512_000_000), api.Execution.Memory)

	receipts := specs.ReadStorage(resource(t, nil, specs.KindStorage, "receipts"))
	require.NotNil(t, receipts.Instances.Limit, "a limit of 0 is written, and is an answer")
	require.Equal(t, int64(0), *receipts.Instances.Limit)
	chat := specs.ReadBus(resource(t, nil, specs.KindBus, "presence"))
	require.Nil(t, chat.Instances.Limit)
	require.False(t, chat.Instances.Declared)

	orders := specs.ReadCache(resource(t, nil, specs.KindCache, "orders"))
	require.True(t, *orders.Replicated)
	require.Equal(t, 24*time.Hour, *orders.Ttl)
	sessions := specs.ReadCache(resource(t, []string{"shop"}, specs.KindCache, "sessions"))
	require.Nil(t, sessions.Replicated, "unset, which is neither true nor false")

	opus := specs.ReadModel(resource(t, nil, specs.KindModel, "opus"))
	require.Equal(t, int64(4096), *opus.Parameters.MaxTokens)
	require.Equal(t, 0.2, *opus.Parameters.Temperature)
	require.Nil(t, opus.Parameters.TopP)
	require.Equal(t, specs.ModelInferenceKindRemote, opus.Inference.Kind)
	require.Equal(t, "https://api.anthropic.com", opus.Inference.RemoteEndpoint)
}

func TestADefaultedBlockIsNotADeclaredOne(t *testing.T) {
	pool := specs.ReadApplication(resource(t, nil, specs.KindApplication, "worker_pool"))
	require.NotNil(t, pool.Liveness.Failures, "the default is in the object")
	require.Equal(t, int64(3), *pool.Liveness.Failures)
	require.False(t, pool.Liveness.Declared, "and is not evidence anybody wrote a probe")
	require.True(t, pool.Readiness.Declared)
	require.Equal(t, specs.ApplicationReadinessKindFunction, pool.Readiness.Kind)
	require.False(t, pool.Regions.Declared)

	api := specs.ReadApplication(resource(t, nil, specs.KindApplication, "api"))
	require.True(t, api.Liveness.Declared)
	require.Equal(t, specs.ApplicationLivenessKindHttp, api.Liveness.Kind)
	require.Equal(t, "/livez", api.Liveness.HttpPath)
	require.Equal(t, specs.RegionsPolicyFailover, api.Regions.Policy)
	require.Equal(t, "region-2", api.Regions.Standby)

	site := specs.ReadGateway(resource(t, nil, specs.KindGateway, "api"))
	require.Equal(t, specs.GatewayRoutingRouter, site.Routing)
	require.False(t, site.Route.Declared, "a router gateway carries only route/match, and that is a default")
	require.Equal(t, "edge_router", site.Router.Name)

	vol := specs.ReadVolume(resource(t, nil, specs.KindVolume, "pgdata"))
	require.Equal(t, 15*time.Minute, *vol.Checkpoint.Interval)
	require.Equal(t, int64(20_000_000_000), vol.Quota)
}

func TestATypedKindReadsItsOwnBranch(t *testing.T) {
	orders := specs.ReadDatabase(resource(t, nil, specs.KindDatabase, "orders"))
	require.Equal(t, specs.DatabaseTypeSql, orders.Type)
	require.True(t, orders.Sql.Declared)
	require.False(t, orders.Kv.Declared)
	require.Equal(t, "postgres", orders.Sql.Engine)
	require.Equal(t, "db.example.com:5432", orders.Sql.Host)
	require.Equal(t, specs.DatabaseSqlTlsVerifyFull, orders.Sql.Tls)
	require.NotEmpty(t, orders.Sql.CredentialsName)

	knowledge := specs.ReadDatabase(resource(t, nil, specs.KindDatabase, "knowledge"))
	require.Equal(t, specs.DatabaseTypeVector, knowledge.Type)
	require.Equal(t, int64(1536), *knowledge.Vector.Dimensions)
	require.Equal(t, "cosine", knowledge.Vector.Metric)

	queue := specs.ReadSync(resource(t, nil, specs.KindSync, "orders"))
	require.Equal(t, specs.SyncTypeQueue, queue.Type)
	require.Equal(t, specs.SyncQueueConsensusRaft, queue.Queue.Consensus)
	require.Equal(t, int64(3), *queue.Queue.MaxAttempts)
	require.Equal(t, 45*time.Second, *queue.Queue.VisibilityTimeout)
	require.NotEmpty(t, queue.Queue.DeadLetterName)
	require.Nil(t, queue.Queue.RetryFactor)
	require.False(t, queue.Log.Declared)

	audit := specs.ReadSync(resource(t, nil, specs.KindSync, "audit"))
	require.Equal(t, specs.SyncTypeLog, audit.Type)
	require.Equal(t, 30*24*time.Hour, *audit.Log.RetentionAge)
	require.Nil(t, audit.Log.RetentionSize)

	receipts := specs.ReadStorage(resource(t, nil, specs.KindStorage, "receipts"))
	require.Equal(t, specs.StorageTypeObject, receipts.Type)
	require.True(t, *receipts.Object.Versioning)
	require.Equal(t, int64(1_000_000_000), receipts.Size)
}

func TestSharedBlocksReadTheSameFromAnyKind(t *testing.T) {
	cache := specs.ReadCache(resource(t, nil, specs.KindCache, "orders"))
	db := specs.ReadDatabase(resource(t, nil, specs.KindDatabase, "profiles"))
	for _, a := range []specs.Authorizer{cache.Authorizer, db.Authorizer} {
		require.Equal(t, "tier_check", a.Name)
		require.Equal(t, "/shop", a.Namespace)
	}
	r := resource(t, nil, specs.KindDatabase, "tokens")
	require.Equal(t, specs.ReadInstances(r), specs.ReadDatabase(r).Instances)
	require.Equal(t, specs.InstancesAdmissionValidator, specs.ReadInstances(r).Admission)

	fn := specs.ReadFunction(resource(t, nil, specs.KindFunction, "reserve_stock"))
	require.Equal(t, "services/reserve", fn.Source.Value, "the short form, at the block's own key")
	require.Equal(t, specs.FunctionBuildKindCommands, fn.Build.Kind)
}

func TestTheKeysAreTheCompiledPaths(t *testing.T) {
	r := resource(t, nil, specs.KindSync, "orders")
	n, ok := r.Int(segments(specs.SyncQueueKeyMaxAttempts)...)
	require.True(t, ok)
	require.Equal(t, int64(3), n)
	require.Equal(t, "postgres", resource(t, nil, specs.KindDatabase, "orders").String(segments(specs.DatabaseSqlKeyEngine)...))
	require.Equal(t, "number", specs.ApplicationPortsEntryKeyNumber, "an entry's keys are its own")
}

func segments(key string) []string { return strings.Split(key, "/") }
