package gateway

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/taubyte/tau/pkg/specs"
)

// Attaches a browser to a bus. A bus with a `host:` is reachable from the internet
// and this is the only internet-facing service; nothing is invoked or placed, since
// every node reaches the same topic.
//
// A bus is n:n, so a browser publishes as well as receives; writing `host:` declares
// that. No envelope and no sender id: a workload writes bare payloads to the same
// topic, and the sender gets its own message back.
//
// A bus with `instances:` is addressed by path (`wss://chat.example.com/room-42`), and
// its validator admits the room before anything subscribes; one without is attached
// at `/`. Naming an instance on the wrong kind is refused.

// busKind is what an ephemeral n:n bus is called in the vocabulary.
const busKind = specs.KindBus

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
	resource specs.Resource

	// id is the resolved resource id, which is half of the handle. The label
	// is not enough: three namespaces can each hold a `chat`.
	id string
}

// attachRouted is a bus reached through a route, which has already matched the path,
// checked methods, written CORS, spent the rate limit and run the transformer chain.
func (s *Service) attachRouted(w http.ResponseWriter, r *http.Request, billed *crossing, m match, p *specs.Project, projectName, environment string) {
	if !upgrading(r) {
		// The name and the path are right and the request is not the one this
		// serves. 404 here would send somebody hunting a typo in a route that
		// matched.
		refuse(w, http.StatusUpgradeRequired, "this route is a bus: attach to it with a websocket")
		return
	}

	at := m.address(projectName, environment)
	resource, id, err := p.Resource(at.Scope, busKind, at.Name)
	if err != nil {
		// The route names a bus the compiled object does not hold -- a deploy
		// that removed it, or a namespace this node cannot complete.
		refuse(w, http.StatusNotFound, "the bus this route names is not here")
		return
	}

	s.attach(w, r, billed, attachable{resource: resource, id: id}, m.stripped(r.URL.Path), projectName, environment)
}

// upgrading reports whether this request is asking for a websocket. A plain GET
// to a bus's name is not one, and is told so rather than attached.
func upgrading(r *http.Request) bool { return websocket.IsWebSocketUpgrade(r) }

// instanceOf is the instance a path names: "/room-42" is one, "/" is none, and
// anything with a second segment is not an instance name at all.
func instanceOf(path string) (string, bool) {
	trimmed := strings.Trim(path, "/")
	if strings.Contains(trimmed, "/") {
		return "", false
	}
	return trimmed, true
}

// instanceSettings is the compiled `instances:` block, or nil when this bus
// holds one topic.
//
// THE ADMISSION SWITCH IS WHAT SAYS THE BLOCK IS THERE, which is the
// vocabulary's own shape: it is a Captures() attribute, so its presence is the
// declaration.
func instanceSettings(resource specs.Resource) map[string]string {
	instances := specs.ReadInstances(resource)
	if instances.Admission == "" {
		return nil
	}

	out := map[string]string{specs.InstancesKeyAdmission: instances.Admission}
	if name := instances.ValidatorName; name != "" {
		out[specs.InstancesKeyValidatorName] = name
	}
	if namespace := instances.ValidatorNamespace; namespace != "" {
		out[specs.InstancesKeyValidatorNamespace] = namespace
	}
	if limit := instances.Limit; limit != nil {
		out[specs.InstancesKeyLimit] = strconv.FormatInt(*limit, 10)
	}
	return out
}

// attach holds one browser on one bus until either end goes away.
func (s *Service) attach(w http.ResponseWriter, r *http.Request, billed *crossing, bus attachable, path, projectName, environment string) {
	if s.buses == nil {
		// A composition fault rather than a deployment one: this node runs a
		// gateway without the gossip an attachment needs.
		refuse(w, http.StatusNotImplemented, "attaching to a bus is not available here")
		return
	}

	// THE PATH IS WHAT THE ROUTE LEFT, not what the client sent: a route that
	// strips its prefix means the instance is named by the rest, the same way
	// every other handler is handed a stripped path.
	instance, ok := instanceOf(path)
	if !ok {
		// An instance name is a KEY and not a path — it is looked up exactly,
		// and a separator in it would make one instance reachable by two names.
		refuse(w, http.StatusNotFound, "that is not an instance of this bus")
		return
	}

	handle := specs.Handle(projectName, environment, bus.id)
	settings := instanceSettings(bus.resource)

	switch {
	case settings == nil && instance != "":
		refuse(w, http.StatusNotFound, "this bus holds one topic and is attached to at /")
		return
	case settings != nil && instance == "":
		refuse(w, http.StatusNotFound, "this bus holds many instances; attach to one of them")
		return
	case settings != nil:
		if s.admits == nil {
			refuse(w, http.StatusNotImplemented, "attaching to an instance of a bus is not available here")
			return
		}
		if err := s.admits.AdmitInstance(r.Context(), handle, settings, instance); err != nil {
			// WHAT A VALIDATOR SAID, WITHOUT SAYING WHOSE. A refusal that named
			// the function would describe somebody's deployment to anyone who
			// asked, which is the rule `refuse` keeps.
			refuse(w, http.StatusForbidden, "this instance was not admitted")
			return
		}
		// THE INSTANCE IS PART OF THE HANDLE, which is what the tier does for
		// every other kind — so a workload publishing to `chat/room-42` and a
		// browser attached to `/room-42` are on one topic.
		handle += "/" + instance
	}

	// r.Context() AND NOT THE REQUEST BUDGET. The timeout above bounds a
	// request end to end, and an attachment is not one: inheriting it would
	// disconnect every browser after thirty seconds.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// ATTACHED BEFORE UPGRADED, so a bus that cannot be reached is still an
	// HTTP status. After the upgrade the only way to say anything is a close
	// frame, which a browser reports as a network error.
	at, err := s.buses.Attach(ctx, handle)
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
