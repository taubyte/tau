package node

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/taubyte/tau/pkg/invoke"
	"github.com/taubyte/tau/pkg/queue"
	"github.com/taubyte/tau/services/worker"
)

// step.go is the program a workflow's steps run.
//
// # A workflow is already queues; this is the only thing it was missing
//
// tcc LOWERS a workflow before anything runs it: one queue per step carrying
// that step's retry budget, one driver function per step, and a dead-letter
// queue at the end. The routing is the queues' own — a failed delivery is
// redelivered, an exhausted one is dead-lettered into the next step's queue or
// into the terminal one — so nothing here re-implements retries, ordering or
// error routing.
//
// What the lowering leaves is a driver whose `source: {kind: builtin, builtin:
// step}` names a program the runtime ships. This is that program, and its whole
// contract is the vocabulary's own sentence: RECEIVE ONE ITEM, INVOKE THE
// FUNCTION IT WAS GRANTED, SEND WHAT CAME BACK TO THE QUEUE IT WAS GRANTED.
//
// # It runs here rather than on a tier
//
// A driver is platform code, and running it as a workload would mean compiling
// this into a component, shipping it, and starting an instance to do what the
// dispatcher is already standing in front of. The queue delivery that would
// have started it is the delivery this handles.
//
// The function it invokes IS run on a tier, with its own memory, timeout and
// grants — which is the half that is the author's.
//
// # The run's state is the message
//
// What a run carries travels from one step's queue to the next, and WHICH QUEUE
// IT IS IN is which step it is at. There is no run record and there is nothing
// to keep in agreement with one. What is genuinely missing is run HISTORY —
// what ran, when, and what it returned — which is a database and a decision.

// stepBuiltin is the vocabulary's name for this program.
const stepBuiltin = "step"

// The headers a step's function is told about the run it is part of. A function
// written to be a step reads its input from the body; these say which step and
// which delivery, for a log line that can be followed.
const (
	headerStep     = "X-Taubyte-Step"
	headerRun      = "X-Taubyte-Run"
	headerAttempts = "X-Taubyte-Attempt"
)

// ErrNoStepTarget is a driver granted nothing to invoke. It is a compiled
// object that did not come from tcc: the lowering writes that grant for every
// step, from the step's own `call`.
var ErrNoStepTarget = errors.New("node: a workflow step's driver was granted no function to invoke")

// runStep is one delivery: invoke, then hand on.
//
// IT RETURNS AN ERROR AND SETTLES NOTHING. Whether a failure is redelivered,
// backed off or dead-lettered is the queue's, decided from the budget the step
// declared — see node/dispatch.go, which acks on success and leaves a failure
// to the queue exactly as it does for any other consumer.
func (d *dispatcher) runStep(ctx context.Context, consumer worker.Consumer, item queue.Delivery) error {
	if len(consumer.Invokes) == 0 {
		return fmt.Errorf("%w: %s", ErrNoStepTarget, consumer.Target.Name)
	}

	res, err := d.node.worker.Invoke(ctx, consumer.Invokes[0], &invoke.Request{
		Method: http.MethodPost,
		Path:   "/",
		Headers: map[string][]string{
			headerStep:     {consumer.Target.Name},
			headerRun:      {consumer.Queue.Label},
			headerAttempts: {strconv.FormatUint(uint64(item.Attempt), 10)},
		},
		Body: item.Data,
	})
	if err != nil {
		return fmt.Errorf("node: step %s: %w", consumer.Target.Name, err)
	}
	if res.Status < 200 || res.Status > 299 {
		// THE STEP FAILED, WHICH IS THE QUEUE'S BUSINESS. Returning an error
		// leaves the delivery unacked, so the budget the author wrote for THIS
		// step is what decides whether it is tried again and where it goes when
		// it is not.
		return fmt.Errorf("node: step %s answered %d", consumer.Target.Name, res.Status)
	}

	// FORWARD PROGRESS: the driver's one send. The last step was granted
	// nothing to send to, so the run ends by there being nowhere to hand it —
	// which is the same fact the lowering states by the term not filling.
	if len(consumer.Sends) == 0 {
		return nil
	}

	// THE ITEM'S ID IS THE RUN'S. Pushing the same id twice is one item, so a
	// step whose send landed and whose ack did not hands the next step the same
	// work rather than a second copy of it — which is what makes a redelivery
	// of THIS step safe.
	return wrapNotLeader(d.sendOn(ctx, consumer.Sends[0], item.ID, res.Body))
}

// sendOn hands the result to the next step's queue.
func (d *dispatcher) sendOn(ctx context.Context, to invoke.Grant, id string, body []byte) error {
	if d.node.work == nil {
		return ErrNoRaft
	}
	return d.node.work.Send(ctx, to, id, body)
}
