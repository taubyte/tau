package v2

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type recorder struct {
	name   string
	log    *[]string
	err    error
	closed bool
}

func (r *recorder) Close() error {
	r.closed = true
	*r.log = append(*r.log, r.name)
	return r.err
}

// TestTheServiceIsClosedBeforeTheNode. The service may still be publishing or
// answering when Close is called, and closing the node under it turns an
// orderly shutdown into a set of errors from a host that has gone.
func TestTheServiceIsClosedBeforeTheNode(t *testing.T) {
	var log []string
	svc := &recorder{name: "service", log: &log}
	node := &recorder{name: "node", log: &log}

	require.NoError(t, closeBoth(svc, func() { node.Close() }))
	require.Equal(t, []string{"service", "node"}, log)
}

// TestTheNodeIsClosedEvenWhenTheServiceFails. A node left running is a port
// still bound and an on-disk datastore still open, and the service's failure is
// not a reason to leak one.
func TestTheNodeIsClosedEvenWhenTheServiceFails(t *testing.T) {
	var log []string
	boom := errors.New("the service did not stop cleanly")
	svc := &recorder{name: "service", log: &log, err: boom}
	node := &recorder{name: "node", log: &log}

	err := closeBoth(svc, func() { node.Close() })
	require.ErrorIs(t, err, boom, "and the failure is still reported")
	require.True(t, node.closed, "the node was closed anyway")
}

// TestAServiceThatWasNeverBuiltStillClosesTheNode.
func TestAServiceThatWasNeverBuiltStillClosesTheNode(t *testing.T) {
	var log []string
	node := &recorder{name: "node", log: &log}

	require.NoError(t, closeBoth(nil, func() { node.Close() }))
	require.True(t, node.closed)
}

func TestRegisterRefusesWhatItCannotWire(t *testing.T) {
	require.Error(t, Register("", nil))
	require.Error(t, Register("stats", nil))
}
