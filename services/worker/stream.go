package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	gostream "github.com/libp2p/go-libp2p-gostream"
	"github.com/libp2p/go-libp2p/core/host"
	libp2ppeer "github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/encoding/protodelim"

	"github.com/taubyte/tau/pkg/project"
	workerv1 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v1"
	"github.com/taubyte/tau/pkg/splice"
)

// stream.go is how a CONNECTION reaches a workload, as against a request.
//
// # Why this is not the connect RPC beside it
//
// `/tau/worker/v1` is connect-go over a libp2p stream and its own schema says it
// is unary on purpose: the whole request arrives before the workload sees any of
// it and the whole response is buffered before the caller sees any of it. That
// is right for an invocation and impossible for a proxy, which carries bytes it
// cannot parse, in both directions, for as long as the client holds the socket
// open — a psql session, an MQTT subscription, a game.
//
// So this is a second protocol on the same host: a length-delimited `Open`
// header, an `Opened` answer, and then nothing but the workload's own bytes.
//
// # The header is a generated message, and the framing is the library's
//
// Nothing here writes a wire format. `Open` and `Opened` are declared in
// worker.proto beside everything else, and protodelim is the protobuf module's
// own length-delimited reader and writer. A hand-rolled struct on a wire between
// two versions of this binary is how a field gets read as another one.
//
// # An answer before the splice, and why it is not just a closed stream
//
// The two failures a caller acts on differently are indistinguishable from a
// closed stream: a node that could not reach the workload is worth trying
// another node, and a target that does not exist is worth trying none. `Opened`
// carries which, so placement retries the first and reports the second.

// StreamProtocol is the raw half of the worker.
//
// IT IS DERIVED THE SAME WAY rpc.Protocol DERIVES THE OTHER, with a suffix, so
// the two sort together in a peer's protocol list and neither can be mistaken
// for v1's `/substrate/p2p/v1`.
var StreamProtocol = protocol.ID(string(Protocol) + "/stream")

// ErrNoConnection is a target that could not be given a connection. It carries
// what the far node said.
var ErrNoConnection = errors.New("worker: that workload gave no connection")

// newByteReader gives protodelim the ByteReader it needs.
//
// IT MUST NOT BUFFER MORE THAN THE HEADER. protodelim reads the length a byte at
// a time and then exactly that many bytes, so a bufio.Reader here would swallow
// the first bytes of the SPLICE into a buffer nothing afterwards reads from —
// the payload would arrive short by however much happened to be in flight.
func newByteReader(r io.Reader) protodelim.Reader { return &byteReader{r: r} }

type byteReader struct {
	r   io.Reader
	one [1]byte
}

func (b *byteReader) Read(p []byte) (int, error) { return b.r.Read(p) }

func (b *byteReader) ReadByte() (byte, error) {
	if _, err := io.ReadFull(b.r, b.one[:]); err != nil {
		return 0, err
	}
	return b.one[0], nil
}

// serveStreams accepts raw connections for this node's workloads.
func (s *Service) serveStreams(ctx context.Context) error {
	listener, err := gostream.Listen(s.host, StreamProtocol)
	if err != nil {
		return fmt.Errorf("worker: listen on %s: %w", StreamProtocol, err)
	}
	s.streams = listener

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// The listener is closed, or the host is gone. Either way there
				// is nothing left to accept.
				return
			}
			go s.carry(ctx, conn)
		}
	}()
	return nil
}

// carry reads one Open, gets a connection to what it names, and splices.
func (s *Service) carry(ctx context.Context, from net.Conn) {
	defer from.Close()

	var open workerv1.Open
	if err := protodelim.UnmarshalFrom(newByteReader(from), &open); err != nil {
		// NOTHING IS ANSWERED HERE. A caller that could not write a header is a
		// caller that is not speaking this protocol, and writing an Opened at
		// it would be answering a question nobody asked.
		return
	}

	t := open.GetTarget()
	if t == nil {
		refuse(from, "a connection with no target names nothing", false)
		return
	}

	to, err := s.Conn(ctx, Target{
		Project:     t.GetProject(),
		Environment: t.GetEnvironment(),
		Scope:       t.GetScope(),
		Kind:        t.GetKind(),
		Name:        t.GetName(),
	}, open.GetPort())
	if err != nil {
		refuse(from, err.Error(), unreachable(err))
		return
	}
	defer to.Close()

	if err := answer(from, &workerv1.Opened{Ok: true}); err != nil {
		return
	}
	splice.Both(from, to)
}

// refuse answers with why, and says whether another node is worth trying.
func refuse(to net.Conn, why string, retry bool) {
	answer(to, &workerv1.Opened{Error: why, Unreachable: retry})
}

func answer(to net.Conn, msg *workerv1.Opened) error {
	_, err := protodelim.MarshalTo(to, msg)
	return err
}

// unreachable is whether this node failing is worth another node's try.
//
// A TARGET THAT DOES NOT EXIST IS NOT, and that is the whole reason Opened
// carries the distinction: a name that resolves nowhere resolves nowhere on
// every node, and walking the fleet to be told so N times is a slower way to
// reach the same answer.
func unreachable(err error) bool {
	return !errors.Is(err, ErrNoTarget) && !errors.Is(err, project.ErrNotFound)
}

// Conn resolves a target and opens a connection to one of its ports.
//
// IT IS METERED AS AN INVOCATION because it is one: a connection holds a pooled
// instance for as long as it is open, which is strictly more than a request
// does. What it cannot record is a DURATION, since the point of a connection is
// that nobody here knows when it ends — so what is emitted is the open.
func (s *Service) Conn(ctx context.Context, t Target, port string) (net.Conn, error) {
	started := s.now()

	spec, err := s.Resolve(ctx, t)
	if err != nil {
		s.emit(ctx, accounted(t, "", 0, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	conn, err := s.pool.Conn(ctx, spec, port)
	if err != nil {
		s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpRefuse, s.now().Sub(started)))
		return nil, err
	}

	s.emit(ctx, accounted(t, spec.Revision, spec.Memory, OpInvoke, s.now().Sub(started)))
	return conn, nil
}

// DialWorkload opens a connection to a workload on the peer given.
//
// IT IS A FUNCTION RATHER THAN A CLIENT METHOD because there is no connect
// client here to hang it on: this protocol is not connect, and building an
// http.Client to reach something that is not HTTP would be a way to make the
// two look alike that helps nobody.
func DialWorkload(ctx context.Context, h host.Host, at libp2ppeer.ID, t project.Address, port string) (net.Conn, error) {
	conn, err := gostream.Dial(ctx, h, at, StreamProtocol)
	if err != nil {
		return nil, fmt.Errorf("worker: dialling %s: %w", at, err)
	}

	if _, err := protodelim.MarshalTo(conn, &workerv1.Open{
		Target: &workerv1.Target{
			Project: t.Project, Environment: t.Environment,
			Scope: t.Scope, Kind: t.Kind, Name: t.Name,
		},
		Port: port,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("worker: opening %s/%s: %w", t.Kind, t.Name, err)
	}

	var opened workerv1.Opened
	if err := protodelim.UnmarshalFrom(newByteReader(conn), &opened); err != nil {
		conn.Close()
		return nil, fmt.Errorf("worker: opening %s/%s: %w", t.Kind, t.Name, err)
	}
	if !opened.GetOk() {
		conn.Close()
		err := fmt.Errorf("%w: %s", ErrNoConnection, opened.GetError())
		if opened.GetUnreachable() {
			// The shape placement already retries on.
			return nil, fmt.Errorf("%w: %w", ErrUnreachableNode, err)
		}
		return nil, err
	}
	return conn, nil
}

// ErrUnreachableNode is a node that could not give this connection but another
// might. It is the sentinel placement retries on.
var ErrUnreachableNode = errors.New("worker: that node could not reach the workload")
