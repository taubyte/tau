package wasm

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// model.go is the host half of `taubyte:model/inference`.
//
// # It takes a name, and that is the whole point
//
// A model resource carries the endpoint, the wire format, the credential and
// the parameters. A workload reaching an LLM by hand carries a copy of all
// four, which is how a provider key ends up in an environment variable on every
// caller and how rotating it becomes an edit per workload.
//
// # What the guest may and may not change
//
// Temperature and the stop list are the caller's to add to. `max-tokens` is
// CLAMPED by what the resource declares, because the vocabulary calls it "a
// cost ceiling as much as a length one" — a guest that could raise it would be
// lifting a limit its author wrote. Asking for less is allowed and is the
// reason this is a clamp rather than a refusal.
//
// The clamp is applied HERE rather than in whatever backs it, so that every
// backing gets it for free and none of them can forget.

const modelInterface = "taubyte:model/inference@0.1.0"

// The vocabulary declares one verb for this kind, and it is a write: it costs
// money and answers differently every time.
const opInfer = "infer"

// The error variant's cases, in the order model.wit declares them.
const (
	errNoSuchModel uint32 = iota
	errModelDenied
	errModelRefused
	errModelUnavailable
)

var (
	// ErrNoInference is a spec that grants a model on a tier composed without a
	// backing.
	ErrNoInference = errors.New("wasm: this resource calls a model and this tier has nothing to call one with")

	// ErrRefusedByProvider is a provider that answered and said no — a request
	// it would refuse again the same way. It is its own error because the
	// caller acts on it differently from one it could not reach.
	ErrRefusedByProvider = errors.New("wasm: the provider refused that request")
)

const modelKind = "model"

// SettingMaxTokens is where the resource's own ceiling travels, as the
// vocabulary's attribute path.
const SettingMaxTokens = "parameters/max_tokens"

// Inferences calls the model a grant names.
type Inferences interface {
	Infer(ctx context.Context, grant invoke.Grant, req *invoke.Inference) (*invoke.Answer, error)
}

// WithInference is what backs `taubyte:model/inference`.
func WithInference(i Inferences) Option {
	return func(r *Runtime) { r.inference = i }
}

func modelGrantsOf(spec invoke.Spec) map[string]invoke.Grant {
	var out map[string]invoke.Grant
	for _, g := range spec.Reaches {
		if g.Kind != modelKind {
			continue
		}
		if out == nil {
			out = map[string]invoke.Grant{}
		}
		out[g.Label] = g
	}
	return out
}

// modelImports registers infer for one instantiation.
func (r *Runtime) modelImports(spec invoke.Spec) []component.Option {
	grants := modelGrantsOf(spec)

	table := component.NewTypeTable()

	toolRef := table.Record(
		"name", component.Prim("string"),
		"description", component.Prim("string"),
		"schema", component.Prim("string"),
	)
	toolCallRef := table.Record(
		"id", component.Prim("string"),
		"name", component.Prim("string"),
		"arguments", component.Prim("string"),
	)
	messageRef := table.Record(
		"role", component.Prim("string"),
		"content", component.Prim("string"),
		"tool-call-id", component.Prim("string"),
		"tool-calls", table.List(toolCallRef),
	)
	requestRef := table.Record(
		"messages", table.List(messageRef),
		"temperature", table.Option(component.Prim("f64")),
		"max-tokens", table.Option(component.Prim("u32")),
		"stop", table.List(component.Prim("string")),
		"tools", table.List(toolRef),
	)
	usageRef := table.Record(
		"input-tokens", component.Prim("u32"),
		"output-tokens", component.Prim("u32"),
	)
	answerRef := table.Record(
		"content", component.Prim("string"),
		"stop-reason", component.Prim("string"),
		"tool-calls", table.List(toolCallRef),
		"usage", usageRef,
	)
	failureRef := table.Variant(
		component.VariantCaseSpec{Name: "no-such-model", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "denied", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "refused", Type: component.Prim("string")},
		component.VariantCaseSpec{Name: "unavailable", Type: component.Prim("string")},
	)
	descriptor := table.Func(
		[]component.TypeRef{component.Prim("string"), requestRef},
		table.Result(answerRef, failureRef),
	)

	infer := func(ctx context.Context, args []component.Value) ([]component.Value, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("model.infer: expected 2 arguments, got %d", len(args))
		}
		label, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("model.infer: first argument is %T, want string", args[0])
		}

		grant, ok := grants[label]
		if !ok {
			return errorResult(modelFailure(errNoSuchModel, label)), nil
		}
		if grant.Refused != "" {
			return errorResult(modelFailure(errModelDenied, grant.Refused)), nil
		}
		if !grant.Allows(opInfer) {
			return errorResult(modelFailure(errModelDenied, label+" cannot infer")), nil
		}
		if r.inference == nil {
			return errorResult(modelFailure(errModelUnavailable, "this node has nothing to call a model with")), nil
		}

		req, err := inferenceFrom(args[1])
		if err != nil {
			return nil, fmt.Errorf("model.infer: %w", err)
		}
		if len(req.Messages) == 0 {
			// A CALL WITH NOTHING TO SAY is refused here rather than paid for:
			// every provider bills the round trip and none of them answer it.
			return errorResult(modelFailure(errModelRefused, "there are no messages in that request")), nil
		}
		clamp(req, grant)

		out, err := r.inference.Infer(ctx, grant, req)
		if err != nil {
			return errorResult(modelArm(err)), nil
		}
		return []component.Value{component.ResultValue{Payload: answerValue(out)}}, nil
	}

	return []component.Option{
		component.WithImportCustom(modelInterface, opInfer, infer, descriptor, table.Resolver()),
	}
}

// clamp holds the caller to the ceiling its author wrote.
//
// A RESOURCE THAT DECLARED NONE CLAMPS NOTHING, which is the vocabulary's own
// default: absent means the provider's own ceiling rather than a number tau
// invented.
func clamp(req *invoke.Inference, grant invoke.Grant) {
	raw, ok := grant.Settings[SettingMaxTokens]
	if !ok {
		return
	}
	n, err := strconv.ParseUint(raw, 10, 32)
	if err != nil || n == 0 {
		return
	}

	declared := uint32(n)
	if req.MaxTokens == nil || *req.MaxTokens > declared {
		req.MaxTokens = &declared
	}
}

// inferenceFrom lifts the world's `request` record.
func inferenceFrom(value component.Value) (*invoke.Inference, error) {
	fields, ok := value.([]component.Value)
	if !ok || len(fields) != 5 {
		return nil, errors.New("that request is not this world's shape")
	}

	out := &invoke.Inference{}

	turns, ok := fields[0].([]component.Value)
	if !ok && fields[0] != nil {
		return nil, errors.New("messages is not a list")
	}
	for _, turn := range turns {
		fields, ok := turn.([]component.Value)
		if !ok || len(fields) != 4 {
			return nil, errors.New("a message is not this world's shape")
		}
		role, roleOK := fields[0].(string)
		content, contentOK := fields[1].(string)
		callID, idOK := fields[2].(string)
		if !roleOK || !contentOK || !idOK {
			return nil, errors.New("a message's strings are not strings")
		}

		calls, err := toolCallsFrom(fields[3])
		if err != nil {
			return nil, err
		}
		out.Messages = append(out.Messages, invoke.Message{
			Role: role, Content: content, ToolCallID: callID, ToolCalls: calls,
		})
	}

	// AN OPTION IS nil FOR none AND THE INNER VALUE OTHERWISE, so absent and
	// zero stay different — which they are: temperature 0 is a value an author
	// writes on purpose.
	if t, ok := fields[1].(float64); ok {
		out.Temperature = &t
	}
	if m, ok := fields[2].(uint32); ok {
		out.MaxTokens = &m
	}

	stops, ok := fields[3].([]component.Value)
	if !ok && fields[3] != nil {
		return nil, errors.New("stop is not a list")
	}
	for _, s := range stops {
		if str, ok := s.(string); ok {
			out.Stop = append(out.Stop, str)
		}
	}

	tools, ok := fields[4].([]component.Value)
	if !ok && fields[4] != nil {
		return nil, errors.New("tools is not a list")
	}
	for _, t := range tools {
		spec, ok := t.([]component.Value)
		if !ok || len(spec) != 3 {
			return nil, errors.New("a tool is not a name, a description and a schema")
		}
		name, nameOK := spec[0].(string)
		description, descOK := spec[1].(string)
		schema, schemaOK := spec[2].(string)
		if !nameOK || !descOK || !schemaOK {
			return nil, errors.New("a tool is not three strings")
		}
		if name == "" {
			// A TOOL WITH NO NAME CANNOT BE CALLED BACK. The model would answer
			// with an empty name and the caller would have nothing to look up.
			return nil, errors.New("a tool must have a name")
		}
		out.Tools = append(out.Tools, invoke.Tool{Name: name, Description: description, Schema: schema})
	}
	return out, nil
}

// toolCallsFrom lifts a `list<tool-call>`.
func toolCallsFrom(value component.Value) ([]invoke.ToolCall, error) {
	list, ok := value.([]component.Value)
	if !ok && value != nil {
		return nil, errors.New("tool-calls is not a list")
	}

	out := make([]invoke.ToolCall, 0, len(list))
	for _, entry := range list {
		fields, ok := entry.([]component.Value)
		if !ok || len(fields) != 3 {
			return nil, errors.New("a tool call is not an id, a name and arguments")
		}
		id, idOK := fields[0].(string)
		name, nameOK := fields[1].(string)
		arguments, argsOK := fields[2].(string)
		if !idOK || !nameOK || !argsOK {
			return nil, errors.New("a tool call is not three strings")
		}
		out = append(out, invoke.ToolCall{ID: id, Name: name, Arguments: arguments})
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// answerValue lowers the world's `answer` record.
func answerValue(a *invoke.Answer) component.Value {
	if a == nil {
		a = &invoke.Answer{}
	}
	calls := make([]component.Value, 0, len(a.ToolCalls))
	for _, c := range a.ToolCalls {
		calls = append(calls, []component.Value{c.ID, c.Name, c.Arguments})
	}

	return []component.Value{
		a.Content,
		a.StopReason,
		calls,
		[]component.Value{a.Usage.InputTokens, a.Usage.OutputTokens},
	}
}

// modelArm maps a Go error onto the arm the guest sees.
func modelArm(err error) *component.VariantValue {
	if errors.Is(err, ErrRefusedByProvider) {
		return modelFailure(errModelRefused, err.Error())
	}
	return modelFailure(errModelUnavailable, err.Error())
}

func modelFailure(disc uint32, detail string) *component.VariantValue {
	return &component.VariantValue{Disc: disc, Payload: detail}
}
