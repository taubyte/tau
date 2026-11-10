package hoarder

import (
	"errors"
	"testing"

	cr "github.com/taubyte/tau/p2p/streams/command/response"
	hoarderSpecs "github.com/taubyte/tau/pkg/specs/hoarder"
)

// A write the hoarder refused used to be reported as a success.
func TestRefusedWriteIsAnError(t *testing.T) {
	if err := refused(cr.Response{hoarderSpecs.BodyCode: hoarderSpecs.CodeOverCapacity}); !errors.Is(err, ErrOverCapacity) {
		t.Fatalf("over-capacity mapped to %v", err)
	}
	if err := refused(cr.Response{}); err != nil {
		t.Fatalf("success mapped to %v", err)
	}
}
