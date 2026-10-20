package node

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/taubyte/tau/node/primitive"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// refusingSeam is a primitives service that refuses every claim and every
// settlement with err, and counts what it was asked.
type refusingSeam struct {
	primitive.Seam
	err error

	mu      sync.Mutex
	claims  int
	settled []bool
}

func (s *refusingSeam) Claim(context.Context, invoke.Grant, int) ([]primitivesService.Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims++
	return nil, s.err
}

func (s *refusingSeam) Settle(_ context.Context, _ invoke.Grant, _ primitivesService.Delivery, done bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settled = append(s.settled, done)
	return s.err
}

// WHAT CLEARS ON ITS OWN IS ORDINARY, and what does not is worth a line.
func TestWhichRefusalsAreOrdinary(t *testing.T) {
	for err, want := range map[error]bool{
		ErrNoPrimitives:                      true,
		fmt.Errorf("w: %w", ErrNoPrimitives): true,
		wasmtier.ErrNotWritable:              true,
		primitivesService.ErrForming:         true,
		primitivesService.ErrClosed:          true,
		connect.NewError(connect.CodeUnavailable, errors.New("shutting down")): true,
		connect.NewError(connect.CodeInternal, errors.New("broken")):           false,
		errors.New("disk full"): false,
	} {
		require.Equal(t, want, ordinaryRefusal(err), err.Error())
	}
}

// A NODE WITH NO WORKER DRAINS NOTHING: there is nothing it could deliver to.
func TestADispatcherWithNoWorkerDeliversNothing(t *testing.T) {
	n, _, _ := makeNode(t, Config{Services: []string{Register}})
	require.Zero(t, (&dispatcher{node: n}).step(t.Context()))
}

// A REFUSED CLAIM ENDS THE DRAIN FOR THIS CYCLE, ordinary or not: the next
// cycle is the retry, and asking again at once would spin.
func TestARefusedClaimEndsTheDrain(t *testing.T) {
	for _, err := range []error{errors.New("disk full"), ErrNoPrimitives} {
		seam := &refusingSeam{err: err}
		d := &dispatcher{node: &Node{}}
		require.Zero(t, d.drain(t.Context(), worker.Consumer{Queue: invoke.Grant{Label: "jobs"}}, seam))
		require.Equal(t, 1, seam.claims, err.Error())
	}
}

// A REFUSED SETTLEMENT IS SAID AND NOT RETRIED: the claim expires on its own,
// and what follows is a redelivery the queue already knows how to make.
func TestARefusedSettlementIsNotRetried(t *testing.T) {
	seam := &refusingSeam{err: errors.New("not the leader")}
	d := &dispatcher{node: &Node{}}
	consumer := worker.Consumer{Queue: invoke.Grant{Label: "jobs", Settings: map[string]string{settingRetryEvery: "1s"}}}
	item := primitivesService.Delivery{ID: "1"}

	d.acknowledge(t.Context(), consumer, seam, item)
	d.failed(t.Context(), consumer, seam, item)
	require.Equal(t, []bool{true, false}, seam.settled)
}
