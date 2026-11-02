package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// dnsresolver.go is the host half of `taubyte:dns/dns-resolver`, the function a
// zone names for names its file lacks (per-tenant labels, addresses that follow
// placement). It answers zone-file lines, so both arms of a zone share one parse
// path in services/dns.

// WorldDNSResolver is `implements: dns-resolver`.
const WorldDNSResolver = specs.WorkloadBlockImplementsDnsResolver

const resolveExport = "resolve"

// The answer variant's arms, in the order the WIT declares them.
const (
	answerRecords uint32 = iota
	answerEmpty
	answerAbsent
)

// ErrNotADNSResolver is a component answering in a shape this world lacks. Never
// read as `absent`, which would be a cacheable NXDOMAIN.
var ErrNotADNSResolver = errors.New("wasm: that component did not answer as a dns resolver")

// Resolve instantiates spec as a dns resolver and asks one question; an instance
// per question, as for AuthorizeRequest.
func (r *Runtime) Resolve(ctx context.Context, spec invoke.Spec, q invoke.Question) (*invoke.Resolution, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldDNSResolver)
	if err != nil {
		return nil, err
	}
	defer runtime.Close(ctx)

	out, err := call(ctx, instance, resolveExport, dnsQuestionValue(q))
	if err != nil {
		return nil, fmt.Errorf("wasm: asking %s: %w", spec.Name, err)
	}
	if len(out) != 1 {
		return nil, fmt.Errorf("%w: %s returned %d values", ErrNotADNSResolver, spec.Name, len(out))
	}
	return resolutionFrom(out[0], spec.Name)
}

// dnsQuestionValue lowers the world's `question` record: the fields in WIT order.
func dnsQuestionValue(q invoke.Question) []component.Value {
	return []component.Value{q.Name, q.Type}
}

// resolutionFrom lifts the `answer` variant back, checking every field.
func resolutionFrom(value component.Value, name string) (*invoke.Resolution, error) {
	var variant *component.VariantValue
	switch v := value.(type) {
	case *component.VariantValue:
		variant = v
	case component.VariantValue:
		variant = &v
	default:
		return nil, fmt.Errorf("%w: %s answered with a %T rather than an answer", ErrNotADNSResolver, name, value)
	}

	switch variant.Disc {
	case answerAbsent:
		return &invoke.Resolution{}, nil

	case answerEmpty:
		return &invoke.Resolution{Exists: true, Records: []string{}}, nil

	case answerRecords:
		// A nil payload is an empty list: `empty`, not malformed.
		if variant.Payload == nil {
			return &invoke.Resolution{Exists: true, Records: []string{}}, nil
		}
		list, ok := variant.Payload.([]component.Value)
		if !ok {
			return nil, fmt.Errorf("%w: %s answered with a %T rather than a list",
				ErrNotADNSResolver, name, variant.Payload)
		}
		records := make([]string, 0, len(list))
		for _, entry := range list {
			line, ok := entry.(string)
			if !ok {
				return nil, fmt.Errorf("%w: %s answered with a record that is not a string",
					ErrNotADNSResolver, name)
			}
			records = append(records, line)
		}
		return &invoke.Resolution{Exists: true, Records: records}, nil
	}

	return nil, fmt.Errorf("%w: %s answered with arm %d", ErrNotADNSResolver, name, variant.Disc)
}
