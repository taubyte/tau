package wasm_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/invoke/wasm"
)

// workflow_test is the one verb a workflow has, and the reason it is its own
// door rather than a queue send with a nicer name.

// startGrant is a workflow a workload may begin a run of, as the composer
// writes one: the grant is the WORKFLOW's and the handle it carries is the
// first step's queue.
func startGrant(label, startHandle string, ops ...string) invoke.Grant {
	if len(ops) == 0 {
		ops = []string{"start"}
	}
	return invoke.Grant{
		Kind: "workflow", Label: label, Handle: "shop/prod/QmWorkflow", Ops: ops,
		Settings: map[string]string{
			"entry/start/kind":   "sync",
			"type":               "queue",
			"entry/start/handle": startHandle,
			"entry/start/as":     "send",
		},
	}
}

func starting(t *testing.T, grants ...invoke.Grant) (invoke.Instance, *work) {
	t.Helper()

	if len(grants) == 0 {
		grants = []invoke.Grant{startGrant("checkout", "shop/prod/QmFirstStep")}
	}
	sent := newWork()
	instance := start(t, tier(t, cacher(t), wasm.WithQueues(sent)), invoke.Spec{
		Name: "fn", Reaches: grants,
	})
	return instance, sent
}

// TestSTARTINGARunIsSendingToTheFirstStEP, at the handle the workflow's own
// entry record named — so no caller works it out from the step's name.
func TestStartingAWorkflowQueuesTheFirstStep(t *testing.T) {
	instance, sent := starting(t)

	require.Equal(t, 204, call(t, instance, "/start/checkout/order-1", []byte("what it carries")).Status)

	require.Equal(t, [][3]string{{"shop/prod/QmFirstStep", "order-1", "what it carries"}}, sent.pushed())
}

// TestTHEGRANTISTheWorkflowsAndNotTheQueues. An author who wrote `workflow:
// checkout: {ops: [start]}` granted a pipeline; spending that through the queue
// interface would let a caller push straight into a later step and skip the one
// that reserves the stock.
func TestAWorkflowGrantIsNotAQueueGrant(t *testing.T) {
	instance, sent := starting(t)

	res := call(t, instance, "/send/checkout/1", []byte("x"))
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-queue:checkout", string(res.Body))
	require.Empty(t, sent.pushed())
}

// TestAWORKFLOWTheConfigNeverGrantedIsRefusedByNAME.
func TestAnUngrantedWorkflowIsRefusedByName(t *testing.T) {
	instance, sent := starting(t)

	res := call(t, instance, "/start/somebody-elses/1", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-workflow:somebody-elses", string(res.Body))
	require.Empty(t, sent.pushed())
}

// TestTHEVERBIsCheckedToo, so a grant that names a workflow without `start`
// starts nothing.
func TestAGrantWithoutStartCannotStart(t *testing.T) {
	instance, sent := starting(t, startGrant("checkout", "shop/prod/QmFirstStep", "read"))

	res := call(t, instance, "/start/checkout/1", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "denied:checkout cannot start", string(res.Body))
	require.Empty(t, sent.pushed())
}

// TestAWORKFLOWWithNoEntryRecordStartsNothing, rather than guessing which of
// its queues is first.
func TestAWorkflowWithNoEntryIsUnavailable(t *testing.T) {
	grant := startGrant("checkout", "")
	delete(grant.Settings, "entry/start/handle")

	instance, sent := starting(t, grant)

	res := call(t, instance, "/start/checkout/1", nil)
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "unavailable:")
	require.Contains(t, string(res.Body), "records no queue")
	require.Empty(t, sent.pushed())
}

// TestAREFUSEDWorkflowGrantSaysWhy.
func TestARefusedWorkflowSaysWhy(t *testing.T) {
	grant := startGrant("checkout", "shop/prod/QmFirstStep")
	grant.Refused = "workflow \"checkout\" records no entry point"

	instance, sent := starting(t, grant)

	res := call(t, instance, "/start/checkout/1", nil)
	require.Equal(t, 403, res.Status)
	require.Contains(t, string(res.Body), "denied:")
	require.Contains(t, string(res.Body), "no entry point")
	require.Empty(t, sent.pushed())
}

// TestANodeThatCannotQueueRefusesTheWorkload, which is the shape every other
// capability keeps.
func TestAServableWorkflowNeedsAQueueBacking(t *testing.T) {
	_, err := tier(t, cacher(t)).Start(t.Context(), invoke.Spec{
		Name: "fn", Artifact: "bafy",
		Reaches: []invoke.Grant{startGrant("checkout", "shop/prod/QmFirstStep")},
	})
	require.ErrorIs(t, err, wasm.ErrNoWorkflows)
}

// TestTHEIMPORTIsRegisteredWithNoGrant, because an unprovided component import
// is a trap that takes the instance with it.
func TestTheWorkflowImportExistsWithoutAGrant(t *testing.T) {
	instance := start(t, tier(t, cacher(t)), invoke.Spec{Name: "fn"})

	res := call(t, instance, "/start/anything/1", nil)
	require.Equal(t, 403, res.Status)
	require.Equal(t, "no-such-workflow:anything", string(res.Body))
}
