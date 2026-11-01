package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// workflow.go is the host half of `taubyte:workflow/runs`. Starting a run is a send
// to the first step's queue, on the queue backing, but a separate interface because
// the grant differs: a workflow grant must not let a caller push into a later step's
// queue.

const workflowInterface = "taubyte:workflow/runs@0.1.0"

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

// workflowKind is the vocabulary's own name for the kind.
const workflowKind = specs.KindWorkflow

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

		// The composer resolved where a start lands; the grant rebuild is invoke.StartGrant,
		// shared with every other firer.
		queued, ok := invoke.StartGrant(grant)
		if !ok {
			return errorResult(workflowFailure(errWorkflowUnavailable,
				label+" records no queue to start at")), nil
		}
		if r.queues == nil {
			return errorResult(workflowFailure(errWorkflowUnavailable,
				"this node has nothing to queue a run with")), nil
		}

		if err := r.queues.Send(ctx, queued, id, input); err != nil {
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
