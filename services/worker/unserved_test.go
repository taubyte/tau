package worker

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/specs"
)

// What is carried to a tier, what is refused by name, and for each refusal that a
// block of defaults alone is not one.

// untouched is the fixture as the author wrote it, servable or not.
func untouched(t *testing.T) *specs.Project {
	t.Helper()
	b, err := json.Marshal(catalogue(t))
	require.NoError(t, err)
	p, err := specs.Load(b)
	require.NoError(t, err)
	return p
}

func appliedTo(t *testing.T, p *specs.Project, runtime, kind, name string) (invoke.Spec, error) {
	t.Helper()
	r, id, err := p.Resource(nil, kind, name)
	require.NoError(t, err)
	spec := invoke.Spec{Runtime: runtime}
	return spec, applied(&spec, Target{Kind: kind, Name: name}, p, r, id)
}

func TestTheFixturesApplicationsAreRefusedByName(t *testing.T) {
	p := untouched(t)

	_, err := appliedTo(t, p, runtimeContainer, specs.KindApplication, "api")
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "limits.max_instances is 10")
	require.ErrorContains(t, err, `liveness (GET /livez on port "http", replaced after 5 failures)`)

	_, err = appliedTo(t, p, runtimeContainer, specs.KindApplication, "db")
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "source.image_pull_secret names the secret")

	_, err = appliedTo(t, p, runtimeContainer, specs.KindApplication, "worker_pool")
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "readiness asks the function")
}

func TestACPUCeilingAndAReadinessProbeAreCarried(t *testing.T) {
	spec, err := appliedTo(t, fixture(t), runtimeContainer, specs.KindApplication, "db")
	require.NoError(t, err, "the harness's copy is servable")
	require.Equal(t, int64(2000), spec.CPU)
	require.Equal(t, &invoke.Probe{Path: "/", Port: "pg"}, spec.Readiness)

	spec, err = appliedTo(t, fixture(t), runtimeContainer, specs.KindApplication, "api")
	require.NoError(t, err)
	require.Equal(t, &invoke.Probe{Path: "/healthz", Port: "http"}, spec.Readiness)
	require.Zero(t, spec.CPU, "api declares no ceiling, and none is invented")
}

// judge applies a resource's own declarations in an empty project.
func judge(t *testing.T, runtime, kind string, r specs.Resource) (invoke.Spec, error) {
	t.Helper()
	p, err := specs.Load([]byte(`{"object":{"id":"Qmp"}}`))
	require.NoError(t, err)
	spec := invoke.Spec{Runtime: runtime}
	return spec, applied(&spec, Target{Kind: kind, Name: "x"}, p, r, "Qmx")
}

func TestACPUCeilingOnTheWasmTierIsRefused(t *testing.T) {
	_, err := judge(t, "wasm", specs.KindFunction, specs.Resource{"execution": map[string]any{"cpu": float64(250)}})
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "execution.cpu is 250 millicores, and the wasm tier")

	_, err = judge(t, "wasm", specs.KindFunction, specs.Resource{"execution": map[string]any{"memory": float64(1 << 20)}})
	require.NoError(t, err, "no ceiling, nothing to refuse")
}

func TestADefaultedLivenessBlockIsNotRefused(t *testing.T) {
	_, err := judge(t, runtimeContainer, specs.KindApplication, specs.Resource{
		"liveness": map[string]any{"failures": float64(3)},
	})
	require.NoError(t, err)

	_, err = judge(t, runtimeContainer, specs.KindApplication, specs.Resource{
		"liveness": map[string]any{"failures": float64(3), "kind": "function", "function": map[string]any{"name": "Qmcheck", "namespace": "/ops"}},
	})
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "liveness (the function Qmcheck in /ops, replaced after 3 failures)")
}

func TestASandboxsOwnClockIsRefused(t *testing.T) {
	_, err := judge(t, runtimeContainer, specs.KindSandbox, specs.Resource{
		"lifetime": map[string]any{"idle": float64(10 * time.Minute)},
	})
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "lifetime (idle 10m0s, max none)")

	_, err = judge(t, runtimeContainer, specs.KindSandbox, specs.Resource{})
	require.NoError(t, err)
}

func TestAnUnreadDelegatedBlockIsRefused(t *testing.T) {
	r := specs.Resource{"source": map[string]any{"kind": "git", "git": "https://example.com/a/b", "config": "taubyte.yaml"}}

	_, err := judge(t, runtimeContainer, specs.KindWorker, r)
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, `source.config names "taubyte.yaml"`)

	filled, err := specs.Load([]byte(`{"object":{"id":"Qmp"},"mounted":{"Qmx":{"repo":"b","commit":"c"}}}`))
	require.NoError(t, err)
	spec := invoke.Spec{Runtime: runtimeContainer}
	require.NoError(t, applied(&spec, Target{Kind: specs.KindWorker, Name: "x"}, filled, r, "Qmx"))
}

func TestAPullCredentialInANamespaceSaysWhere(t *testing.T) {
	_, err := judge(t, runtimeContainer, specs.KindSandbox, specs.Resource{
		"source": map[string]any{"kind": "image", "image": "a/b:1", "image_pull_secret": map[string]any{"name": "Qmreg", "namespace": "/ops"}},
	})
	require.ErrorIs(t, err, ErrUnserved)
	require.ErrorContains(t, err, "names the secret Qmreg in /ops")
}

func refusal(kind string, target specs.Resource) string {
	return unservable(specs.Reach{Kind: kind, Label: "it"}, target)
}

func TestALocalStreamIsRefusedAndAnUnlocalOneIsServed(t *testing.T) {
	require.Contains(t, refusal(streamKind, specs.Resource{"local": true}), "is local")
	require.Empty(t, refusal(streamKind, specs.Resource{"local": false}))
	require.Empty(t, refusal(streamKind, specs.Resource{}))
}

func TestACheckpointBudgetIsRefusedAndTheDefaultsAreNot(t *testing.T) {
	defaults := map[string]any{"interval": float64(time.Minute), "on_budget_exceeded": "alert"}
	require.Empty(t, refusal(volumeKind, specs.Resource{"checkpoint": defaults}))

	budgeted := map[string]any{"interval": float64(time.Minute), "on_budget_exceeded": "freeze-writes", "rpo_budget": float64(5 * time.Minute)}
	why := refusal(volumeKind, specs.Resource{"checkpoint": budgeted})
	require.Contains(t, why, "rpo_budget of 5m0s")
	require.Contains(t, why, "never freeze-writes")
}

func TestAnEncryptedCacheIsRefused(t *testing.T) {
	require.Empty(t, refusal(cacheKind, specs.Resource{"consensus": "crdt"}))
	why := refusal(cacheKind, specs.Resource{"consensus": "crdt", "encryption": map[string]any{"key": map[string]any{"name": "Qmkey"}}})
	require.Contains(t, why, "encrypted with the secret Qmkey")
}

func TestWhatAWorkflowsDriversDoNotDoIsRefused(t *testing.T) {
	reach := specs.Reach{Kind: workflowKind, Label: "checkout"}
	step := func(call map[string]any) specs.Resource {
		return specs.Resource{"steps": []any{map[string]any{"name": "reserve", "call": call}}}
	}

	require.Empty(t, unservableWorkflow(reach, specs.ReadWorkflow(step(map[string]any{"function": "Qmf"}))))
	require.Contains(t, unservableWorkflow(reach, specs.ReadWorkflow(specs.Resource{"concurrency": float64(50)})), "allows 50 runs in flight")
	require.Contains(t, unservableWorkflow(reach, specs.ReadWorkflow(step(map[string]any{"function": "Qmf", "input": "order.lines"}))),
		`hands step "reserve" the input "order.lines"`)
	require.Contains(t, unservableWorkflow(reach, specs.ReadWorkflow(step(map[string]any{"function": "Qmf", "result": "reservation"}))),
		`keeps step "reserve"'s answer as "reservation"`)

	// AND THROUGH THE ONE DOOR A GRANT IS DECIDED AT.
	require.Contains(t, refusal(workflowKind, specs.Resource{"concurrency": float64(2)}), "runs in flight")
}

func TestARegionBlockIsNotRead(t *testing.T) {
	pinned := specs.Resource{"regions": map[string]any{"policy": "pinned", "in": []any{"region-1"}}}
	for _, kind := range []string{cacheKind, storageKind, volumeKind, syncKind, workflowKind, databaseKind, functionKind} {
		require.Empty(t, unservable(specs.Reach{Kind: kind, Label: "x", Ops: []string{"get"}}, pinned), kind)
	}
}

func fixture(t *testing.T) *specs.Project {
	t.Helper()
	p, err := specs.Load(compiled(t))
	require.NoError(t, err)
	return p
}
