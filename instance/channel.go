package instance

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"

	"github.com/taubyte/tau/instance/placement"
	"github.com/taubyte/tau/pkg/invoke"
	wasmtier "github.com/taubyte/tau/pkg/invoke/wasm"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/services/worker"
)

// Backs `taubyte:channel/dialer`, the `channel` kind (v1's p2p). A dial resolves the
// address the channel names and invokes it over the worker's own protocol: the
// serving workload is addressed through the deployment view and placement, not
// advertised per channel, so tenancy is structural. The channel name travels as a
// header so a workload serving several can tell them apart. Request and response
// only; a held-open conduit would need a WIT resource the ABI does not have yet.

// headerChannel is the author's own name for the conduit, so a workload serving
// more than one can tell them apart. headerDialer is the channel's label as the
// DIALING workload wrote it, which is a different string and useful in a log.
const (
	headerChannel = "X-Taubyte-Channel"
	headerDialer  = "X-Taubyte-Channel-Label"
)

// What the composer wrote onto a channel grant, spelled as the vocabulary
// spells the attributes — except the scope, which travels RESOLVED: the
// outward walk an omitted namespace means happens where the compiled project
// is open, and what arrives here is the chain it produced.
const (
	settingProtocol   = specs.ChannelKeyProtocol
	settingServeKind  = specs.ChannelServeKeyKind
	settingServeName  = specs.ChannelServeKeyName
	settingServeScope = specs.ChannelServeKeyNamespace
	settingServePort  = specs.ChannelServeKeyPort
)

// channels dials the workload a channel grant names.
type channels struct{ node *Instance }

// Dial invokes whatever serves the channel and answers with what came back.
//
// IT IS BOUNDED BY ctx AND NOTHING ELSE. The context carries the CALLING
// workload's own deadline: a channel declares none of its own, and the budget
// being spent belongs to the workload that is waiting.
func (c *channels) Dial(ctx context.Context, grant invoke.Grant, body []byte) ([]byte, error) {
	kind := grant.Settings[settingServeKind]
	name := grant.Settings[settingServeName]
	if kind == "" || name == "" {
		return nil, fmt.Errorf("%w: %s names nothing that serves it", wasmtier.ErrNoAnswer, grant.Label)
	}

	proj, environment, _ := specs.SplitHandle(grant.Handle)
	if proj == "" || environment == "" {
		return nil, fmt.Errorf("node: the handle %q does not name a deployment", grant.Handle)
	}

	at := worker.Target{
		Project:     proj,
		Environment: environment,
		Scope:       specs.ScopeOf(nil, grant.Settings[settingServeScope]),
		Kind:        kind,
		Name:        name,
	}

	// A WORKLOAD WITH PORTS GETS THE BYTES ON ONE. A channel is a byte channel,
	// and a container is a process listening on a socket: what it is handed is
	// what the dialer sent, not an HTTP request wrapped around it. A component
	// has no socket and is dispatched, below.
	if grant.Connects() {
		return c.connect(ctx, grant, at, body)
	}

	res, err := c.invoke(ctx, at, &invoke.Request{
		Method: http.MethodPost,
		Path:   "/",
		Headers: map[string][]string{
			headerChannel: {grant.Settings[settingProtocol]},
			headerDialer:  {grant.Label},
		},
		Body: body,
	})
	if err != nil {
		// NOT REACHED AND REFUSED ARE DIFFERENT ANSWERS, and the guest acts on
		// them differently: a caller RETRIES a channel whose far end is not up
		// yet, which on a cold deploy is a matter of seconds.
		if unanswered(err) {
			return nil, fmt.Errorf("%w: nothing in %s/%s serves %s/%s", wasmtier.ErrNoAnswer, proj, environment, kind, name)
		}
		return nil, fmt.Errorf("node: dialling %s failed: %w", grant.Label, err)
	}

	// THE FAR END'S OWN FAILURE IS ITS OWN ARM. A workload that answered with a
	// 500 was reached, and a caller fixes that rather than retrying it.
	if res.Status < 200 || res.Status > 299 {
		return nil, fmt.Errorf("%w: %s answered %d", wasmtier.ErrChannelFailed, grant.Label, res.Status)
	}
	return res.Body, nil
}

// connect delivers body to the named port of the workload at, half-closes, and
// answers with everything it wrote back until it closed.
//
// HALF-CLOSED, SO A SERVER READING TO EOF KNOWS THE REQUEST ENDED — the one
// signal a byte stream has. A transport that cannot half-close still delivers;
// its server then has to know where its own requests end, as HTTP/1.0 does.
func (c *channels) connect(ctx context.Context, grant invoke.Grant, at specs.Address, body []byte) ([]byte, error) {
	conn, err := c.conn(ctx, at, grant.Settings[settingServePort])
	if err != nil {
		if unanswered(err) {
			return nil, fmt.Errorf("%w: nothing in %s/%s serves %s/%s", wasmtier.ErrNoAnswer, at.Project, at.Environment, at.Kind, at.Name)
		}
		return nil, fmt.Errorf("node: dialling %s failed: %w", grant.Label, err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	if _, err := conn.Write(body); err != nil {
		return nil, fmt.Errorf("%w: %s: writing: %v", wasmtier.ErrChannelFailed, grant.Label, err)
	}
	if hc, ok := conn.(interface{ CloseWrite() error }); ok {
		hc.CloseWrite()
	}
	answer, err := io.ReadAll(conn)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: reading: %v", wasmtier.ErrChannelFailed, grant.Label, err)
	}
	return answer, nil
}

// conn is invoke's rule for a connection: through placement where this node
// can route, through the local worker where it cannot.
func (c *channels) conn(ctx context.Context, at specs.Address, port string) (net.Conn, error) {
	if c.node.workers != nil {
		return c.node.workers.Conn(ctx, at, port)
	}
	if c.node.worker != nil {
		return c.node.worker.Conn(ctx, at, port)
	}
	return nil, placement.ErrNoWorkers
}

func (c *channels) invoke(ctx context.Context, at specs.Address, req *invoke.Request) (*invoke.Response, error) {
	if c.node.workers != nil {
		return c.node.workers.Invoke(ctx, at, req)
	}
	if c.node.worker != nil {
		return c.node.worker.Invoke(ctx, at, req)
	}
	return nil, placement.ErrNoWorkers
}

// unanswered is "the far end is not there", as against "the far end said no".
// A deployment that holds no such resource is the far end not being there too:
// the dialer can do nothing about it but ask again once something serves it.
func unanswered(err error) bool {
	return errors.Is(err, placement.ErrNoWorkers) || errors.Is(err, worker.ErrNoTarget) ||
		errors.Is(err, specs.ErrNotFound)
}
