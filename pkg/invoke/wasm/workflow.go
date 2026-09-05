package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// workflow.go is the host half of `taubyte:handler/workflow`.
//
// # It is one function over the queue backing, and it is still not `queue.send`
//
// A workflow lowers to one queue per step and starting a run IS sending to the
// first step's queue — so this reaches the same backing `queue.send` does, with
// the handle the composer resolved from the workflow's own `entry/start`
// record.
//
// WHAT MAKES IT A SEPARATE INTERFACE IS THE GRANT. An author who wrote
// `workflow: checkout: {ops: [start]}` granted a pipeline, not the queues it
// became; serving that through the queue interface would let a caller push
// straight into `checkout_charge` and skip the step that reserves the stock.
// The two doors carry different permissions, so there are two doors.

const workflowInterface = "taubyte:handler/workflow@0.1.0"

// opStart is the vocabulary's one verb for a workflow.
const opStart = "start"

// The error variant's cases, in the order handler.wit declares them.
const (
	errNoSuchWorkflow uint32 = iota
	errWorkflowDenied
	errWorkflowNotWritable
	errWorkflowUnavailable
)

// ErrNoWorkflows is a spec that grants a workflow on a tier composed without a
// queue backing — a workflow is queues, so it is the same backing.
var ErrNoWorkflows = errors.New("wasm: this resource starts a workflow and this tier has nothing to queue one with")

// The settings the composer writes from the workflow's `entry/start` record.
const (
	settingStartKind   = "entry/start/kind"
	settingStartHandle = "entry/start/handle"
)

// workflowKind is the vocabulary's own name for the kind.
const workflowKind = "workflow"

// workflowGrantsOf is every workflow grant.
func workflowGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != workflowKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// servableWorkflows is the workflow grants this tier would have to serve.
func servableWorkflows(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for label, g := range workflowGrantsOf(spec) {
		if g.Refused != "" || !g.Allows(opStart) {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[label] = g
	}
	return out
}

// workflowImports registers `start` for one instantiation.
func (r *Runtime) workflowImports(spec invoke.Spec) []component.Option {
	grants := workflowGrantsOf(spec)

	table := component.NewTypeTable()
	descriptor := table.Func(
		[]component.TypeRef{
			component.Prim("string"),
			component.Prim("string"),
			table.List(component.Prim("u8")),
		},
		table.Result(component.TypeRef{}, table.Variant(
			component.VariantCaseSpec{Name: "no-such-workflow", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "not-writable", Type: component.Prim("string")},
			component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
		)),
	)

	start := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 3 {
			return nil, fmt.Errorf("workflow.start: expected 3 arguments, got %d", len(args))
		}
		label, id, err := twoStrings(args)
		if err != nil {
			return nil, err
		}
		input, err := readBytes(args[2])
		if err != nil {
			return nil, fmt.Errorf("workflow.start: %w", err)
		}

		grant, ok := grants[label]
		if !ok {
			return errorResult(workflowFailure(errNoSuchWorkflow, label)), nil
		}
		if grant.Refused != "" {
			return errorResult(workflowFailure(errWorkflowDenied, grant.Refused)), nil
		}
		if !grant.Allows(opStart) {
			return errorResult(workflowFailure(errWorkflowDenied, label+" cannot start")), nil
		}

		// THE COMPOSER RESOLVED WHERE A START LANDS. A tier that read the
		// workflow to find out would need the compiled project on the request
		// path, which is the whole reason a grant carries a handle at all.
		handle := grant.Settings[settingStartHandle]
		if handle == "" || grant.Settings[settingStartKind] != "queue" {
			return errorResult(workflowFailure(errWorkflowUnavailable,
				label+" records no queue to start at")), nil
		}
		if r.queues == nil {
			return errorResult(workflowFailure(errWorkflowUnavailable,
				"this node has nothing to queue a run with")), nil
		}

		// THE GRANT HANDED ON IS THE QUEUE'S AND THE OPS ARE THE QUEUE'S, so
		// the backing enforces what it always does and this function invents no
		// permission: what was checked above is the workflow grant.
		if err := r.queues.Send(ctx, invoke.Grant{
			Kind: "queue", Label: label, Handle: handle,
			Ops: []string{"send"}, Settings: grant.Settings,
		}, id, input); err != nil {
			if errors.Is(err, ErrNotWritable) {
				return errorResult(workflowFailure(errWorkflowNotWritable, err.Error())), nil
			}
			return errorResult(workflowFailure(errWorkflowUnavailable, err.Error())), nil
		}
		return []component.Value{component.ResultValue{}}, nil
	}

	return []component.Option{
		component.WithImportCustom(workflowInterface, opStart, start, descriptor, table.Resolver()),
	}
}

func workflowFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
