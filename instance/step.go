package instance

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/taubyte/tau/pkg/invoke"
	primitivesService "github.com/taubyte/tau/services/primitives"
	"github.com/taubyte/tau/services/worker"
)

// The program a workflow's steps run. tcc lowers a workflow into one queue per step
// (with its retry budget), one driver per step and a dead-letter queue, so routing is
// the queues'. The driver's `source: {kind: builtin, builtin: step}` is this program:
// receive one item, invoke the granted function, send what came back to the granted
// queue. It runs here, in the dispatcher, rather than as a workload; the author's
// function runs on a tier. The run's state is the message and which queue holds it;
// there is no run history yet.

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

// ErrNoStepTarget is a driver granted nothing to invoke AND nothing to hand on
// to — which is neither a step that calls nor a step that waits, so it is a
// compiled object that did not come from tcc.
var ErrNoStepTarget = errors.New("node: a workflow step's driver was granted no function to invoke")

// runStep is one delivery: invoke, then hand on.
//
// IT RETURNS AN ERROR AND SETTLES NOTHING. Whether a failure is redelivered,
// backed off or dead-lettered is the queue's, decided from the budget the step
// declared — see instance/dispatch.go, which acks on success and leaves a failure
// to the queue exactly as it does for any other consumer.
func (d *dispatcher) runStep(ctx context.Context, consumer worker.Consumer, item primitivesService.Delivery) error {
	// A step that only waits invokes nothing: its queue's `delay` already held the item
	// back, so it is handed on unchanged.
	if len(consumer.Invokes) == 0 {
		if len(consumer.Sends) == 0 {
			return fmt.Errorf("%w: %s", ErrNoStepTarget, consumer.Target.Name)
		}
		return d.sendOn(ctx, consumer.Sends[0], item.ID, item.Data)
	}

	// PLACED, like every other invocation in this node. The driver is platform
	// code and runs where the claim was made; the author's function is a
	// workload and runs wherever it should — see dispatcher.invoke.
	res, err := d.invoke(ctx, consumer.Invokes[0], &invoke.Request{
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
	return d.sendOn(ctx, consumer.Sends[0], item.ID, res.Body)
}

// sendOn hands the result to the next step's queue.
//
// THE REFUSAL ARRIVES ALREADY IN THE TIER'S WORDS. The primitives service maps
// a write off the leader onto ErrNotWritable itself, on both arms, so nothing
// is re-wrapped here — which is what makes a step on a follower say the same
// thing whether the namespace was in this process or on another node.
func (d *dispatcher) sendOn(ctx context.Context, to invoke.Grant, id string, body []byte) error {
	if d.node.syncs == nil {
		return ErrNoPrimitives
	}
	return d.node.syncs.Send(ctx, to, id, body)
}
