package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/taubyte/tau/pkg/project"
)

// bus.go attaches a browser to a bus.
//
// # Why the gateway and not the worker
//
// A bus with a `host:` is reachable from the internet, and this is the only v2
// service that faces it. Nothing is invoked: the connection is held open and
// what arrives on the topic is written to it, so there is no request to route
// and no placement decision to make — every node on the swarm reaches the same
// topic, so whichever one the connection landed on is the one that serves it.
//
// # It is both directions, and that is what `attach` means
//
// A bus is n:n: "everything attached gets what is sent". A read-only socket
// would be 1:n and a different kind. So a browser publishes as well as
// receives, and WRITING `host:` IS WHAT DECLARES THAT — the vocabulary has no
// axis that says otherwise, and the kind's own doc calls the name "the name a
// browser attaches to". An author who wants a one-way feed does not give the
// bus a host and pushes from a workload instead.
//
// There is no envelope and no sender id. A workload publishing to the same
// topic writes the payload and nothing else, so a frame that arrived wrapped
// would be a second wire format for one topic — and the sender gets its own
// message back, because that is what "everything attached" says.
//
// # What it does not do yet
//
// A bus that declares `instances:` admits each instance through a validator
// function, and that function's host interface does not exist. Such a bus is
// refused rather than attached to as if the block were not there, which is the
// same answer this service already gives a router and a fail-closed
// transformer.

// busKind is what an ephemeral n:n bus is called in the vocabulary.
const busKind = "bus"

const (
	// maxBusMessage is the largest frame either direction carries.
	//
	// IT IS THE TRANSPORT'S OWN CEILING rather than a policy invented here:
	// gossipsub refuses a message over 1MiB, so a larger one could be read off
	// the socket and then never published. The part that matters is that there
	// is a limit at all — this is a public listener and a read allocates.
	maxBusMessage = 1 << 20

	// pongWait is how long a silent peer is given before the connection is
	// considered gone, and pingInterval is how often it is asked.
	//
	// A KEEPALIVE IS NOT OPTIONAL ON THIS PATH. A live-updates bus is idle
	// most of the time, and every intermediary between a browser and this node
	// drops an idle connection somewhere around a minute — so without a ping
	// the connection that carries nothing is exactly the one that dies.
	pongWait     = 60 * time.Second
	pingInterval = (pongWait * 9) / 10

	// writeWait bounds one frame, so a peer that stops reading cannot hold a
	// goroutine and a socket open forever.
	writeWait = 10 * time.Second
)

// upgrader is gorilla's, with the one policy this kind has.
var upgrader = websocket.Upgrader{
	// A BUS WITH A `host:` IS DECLARED PUBLIC, and there is nothing here to
	// check an origin against: this kind has no origin list, and the
	// attachment carries no session for a cross-origin page to ride. The
	// same-origin default would refuse the ordinary case — a page on one name
	// attaching to a bus on another — to protect nothing. If the kind ever
	// gains an origin axis, it is read here.
	CheckOrigin: func(*http.Request) bool { return true },
}

// attachable is one bus that answers on a name.
type attachable struct {
	host     project.Host
	resource project.Resource

	// id is the resolved resource id, which is half of the handle. The label
	// is not enough: three namespaces can each hold a `chat`.
	id string
}

// busesAt is every bus in this project answering on a name.
func (s *Service) busesAt(p *project.Project, name string) []attachable {
	var out []attachable
	for _, h := range p.At(s.lookupName(name)) {
		if h.Kind != busKind {
			continue
		}
		r, id, err := p.Resource(h.Scope, h.Kind, h.Name)
		if err != nil {
			continue
		}
		out = append(out, attachable{host: h, resource: r, id: id})
	}
	return out
}

// upgrading reports whether this request is asking for a websocket. A plain GET
// to a bus's name is not one, and is told so rather than attached.
func upgrading(r *http.Request) bool { return websocket.IsWebSocketUpgrade(r) }

// attach holds one browser on one bus until either end goes away.
func (s *Service) attach(w http.ResponseWriter, r *http.Request, billed *crossing, buses []attachable, projectName, environment string) {
	if s.buses == nil {
		// A composition fault rather than a deployment one: this node runs a
		// gateway without the gossip an attachment needs.
		refuse(w, http.StatusNotImplemented, "attaching to a bus is not available here")
		return
	}
	if len(buses) > 1 {
		// Two buses on one name. Two gateways on one name are told apart by
		// their paths; nothing tells these apart, so serving the first would
		// be a coin toss made once at compile time and kept forever.
		refuse(w, http.StatusBadGateway, "more than one bus answers on this name")
		return
	}

	bus := buses[0]
	if bus.resource.String("instances", "admission") != "" {
		refuse(w, http.StatusNotImplemented,
			"this bus admits instances through a validator function, which is not implemented")
		return
	}

	// r.Context() AND NOT THE REQUEST BUDGET. The timeout above bounds a
	// request end to end, and an attachment is not one: inheriting it would
	// disconnect every browser after thirty seconds.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// ATTACHED BEFORE UPGRADED, so a bus that cannot be reached is still an
	// HTTP status. After the upgrade the only way to say anything is a close
	// frame, which a browser reports as a network error.
	at, err := s.buses.Attach(ctx, project.Handle(projectName, environment, bus.id))
	if err != nil {
		refuse(w, statusFor(err), "this bus could not be attached to")
		return
	}
	defer at.Close()

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has already written its own refusal.
		return
	}
	defer conn.Close()

	billed.attached = true
	billed.status = http.StatusSwitchingProtocols

	in, out := s.pump(ctx, cancel, conn, at)
	billed.in, billed.out = in, out
}

// pump runs the connection and returns what crossed it.
//
// THREE PARTS AND ONE WRITER. gorilla allows one concurrent reader and one
// concurrent writer and no more, so the browser's frames are read in one
// goroutine, the bus's messages are collected in another, and this one is the
// only thing that writes — which is also what lets the keepalive share a select
// with the traffic instead of racing it.
func (s *Service) pump(ctx context.Context, cancel context.CancelFunc, conn *websocket.Conn, at Attachment) (in, out int64) {
	var read, written atomic.Int64

	conn.SetReadLimit(maxBusMessage)

	// The deadlines are wall clock and not the injected one: they are set on
	// the socket, which knows nothing about a test's clock.
	conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	var wg sync.WaitGroup
	wg.Add(2)

	// browser -> bus
	go func() {
		defer wg.Done()
		defer cancel()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				return
			}
			read.Add(int64(len(message)))
			if err := at.Publish(ctx, message); err != nil {
				return
			}
		}
	}()

	// bus -> here. It is a goroutine rather than a call in the loop below
	// because Next blocks, and a writer blocked in it would never reach the
	// keepalive.
	messages := make(chan []byte)
	go func() {
		defer wg.Done()
		defer close(messages)
		for {
			message, err := at.Next(ctx)
			if err != nil {
				return
			}
			select {
			case messages <- message:
			case <-ctx.Done():
				return
			}
		}
	}()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()

writing:
	for {
		select {
		case <-ctx.Done():
			break writing
		case message, ok := <-messages:
			if !ok {
				break writing
			}
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.BinaryMessage, message); err != nil {
				break writing
			}
			written.Add(int64(len(message)))
		case <-ping.C:
			conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				break writing
			}
		}
	}

	// Closing the connection is what unblocks the reader; cancelling is what
	// unblocks the receiver. Both are needed, because each of the three parts
	// can be the one that noticed first.
	cancel()
	conn.Close()
	wg.Wait()

	return read.Load(), written.Load()
}

// lookupName is the request's name in the terms the compiled project uses.
//
// A GENERATED NAME LOSES THE CLOUD'S SEGMENT FIRST. The compiled project
// carries `<label>.<project label>` because the cloud's own fqdn is not in the
// config; this is the node that knows it, so it takes it off the request rather
// than putting it into the project.
func (s *Service) lookupName(name string) string {
	lookup := strings.ToLower(name)
	if s.cfg.CloudFqdn == "" {
		return lookup
	}
	if trimmed, ok := strings.CutSuffix(lookup, "."+strings.ToLower(s.cfg.CloudFqdn)); ok {
		return trimmed
	}
	return lookup
}
