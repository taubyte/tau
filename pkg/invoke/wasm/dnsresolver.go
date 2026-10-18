package wasm

import (
	"context"
	"errors"
	"fmt"

	"github.com/taubyte/tau/pkg/specs"

	"github.com/samyfodil/wazy/component"

	"github.com/taubyte/tau/pkg/invoke"
)

// dnsresolver.go is the host half of `taubyte:dns/dns-resolver` — the function a
// zone names for the names its file does not carry.
//
// WHY A ZONE NEEDS ONE AT ALL. A zone file is what an author knows when they
// deploy. A label per tenant, an address that follows where a workload actually
// landed, a name that should not resolve until something is checked — none of
// those can be written down in advance, and a file that tried would be rewritten
// on every deploy.
//
// WHY IT ANSWERS IN ZONE-FILE NOTATION. The other arm of the same zone is a
// file, and services/dns turns a line into a record with one dns.NewRR under the
// zone's $ORIGIN. Answering in the same notation means both arms converge on ONE
// parse path, so a record cannot mean one thing written in the file and another
// returned by the function. A structured record would also have to enumerate A,
// AAAA, MX, TXT, SRV, CAA and whatever is standardised next; a line says all of
// them.

// WorldDNSResolver is `implements: dns-resolver`.
const WorldDNSResolver = specs.WorkloadBlockImplementsDnsResolver

const resolveExport = "resolve"

// The answer variant's arms, in the order the WIT declares them.
const (
	answerRecords uint32 = iota
	answerEmpty
	answerAbsent
)

// ErrNotADNSResolver is a component that answered with a shape this world does
// not have.
//
// IT IS NEVER READ AS AN ANSWER, and for DNS that matters more than for most: a
// malformed reply taken as `absent` would be NXDOMAIN, which is cacheable as an
// absence — so one broken component would teach every resolver that asked that
// the name does not exist.
var ErrNotADNSResolver = errors.New("wasm: that component did not answer as a dns resolver")

// Resolve instantiates spec as a dns resolver and asks it one question.
//
// AN INSTANCE PER QUESTION, for AuthorizeRequest's reason: the answer is a pure
// function of the question and whatever the resolver reads, so a warm pool would
// be a second lifecycle to reason about for no reuse worth having.
func (r *Runtime) Resolve(ctx context.Context, spec invoke.Spec, q invoke.Question) (*invoke.Resolution, error) {
	instance, runtime, err := r.instantiate(ctx, spec, WorldDNSResolver)
	if err != nil {
		return nil, err
	}
	defer runtime.Close(ctx)

	out, err := instance.Call(ctx, resolveExport, dnsQuestionValue(q))
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

// resolutionFrom lifts the world's `answer` variant back.
//
// EVERY FIELD IS CHECKED, for admissionFrom's reason: what came back was
// produced by somebody else's component, and a shape that is not this world's
// has to read as an error naming the resolver rather than as a DNS answer.
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
		// AN EMPTY LIST HERE IS `empty` AND NOT A REFUSAL, which the world says
		// outright. It arrives as a nil payload rather than an empty slice, and
		// reading that as malformed would turn "this name has nothing of that
		// type" into an author's bug — admissionFrom says the same about a grant
		// of nothing.
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
