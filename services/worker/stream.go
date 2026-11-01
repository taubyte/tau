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

	workerv2 "github.com/taubyte/tau/pkg/proto/gen/tau/worker/v2"
	"github.com/taubyte/tau/pkg/specs"
	"github.com/taubyte/tau/pkg/splice"
)

// The connect RPC is unary, which a proxy cannot use: it carries unparsed bytes both
// ways for as long as the socket is open. So this is a second protocol on the same
// host: a length-delimited `Open` header (worker.proto, via protodelim), an `Opened`
// answer, then only the workload's bytes. `Opened` says whether another node is worth
// trying, which a closed stream cannot.

// StreamProtocol is the raw half of the worker, derived from Protocol with a suffix.
var StreamProtocol = protocol.ID(string(Protocol) + "/stream")

// ErrNoConnection is a target that could not be given a connection. It carries
// what the far node said.
var ErrNoConnection = errors.New("worker: that workload gave no connection")

// newByteReader gives protodelim the ByteReader it needs. It must not buffer past the
// header, or the splice's first bytes are swallowed.
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

	var open workerv2.Open
	if err := protodelim.UnmarshalFrom(newByteReader(from), &open); err != nil {
		// Nothing is answered to a caller not speaking this protocol.
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

	if err := answer(from, &workerv2.Opened{Ok: true}); err != nil {
		return
	}
	splice.Both(from, to)
}

// refuse answers with why, and says whether another node is worth trying.
func refuse(to net.Conn, why string, retry bool) {
	answer(to, &workerv2.Opened{Error: why, Unreachable: retry})
}

func answer(to net.Conn, msg *workerv2.Opened) error {
	_, err := protodelim.MarshalTo(to, msg)
	return err
}

// unreachable is whether this node failing is worth another node's try. A target
// that does not exist resolves nowhere on every node.
func unreachable(err error) bool {
	return !errors.Is(err, ErrNoTarget) && !errors.Is(err, specs.ErrNotFound)
}

// Conn resolves a target and opens a connection to one of its ports. Metered as an
// invocation at open; a connection has no known end, so no duration.
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

// DialWorkload opens a connection to a workload on the peer given. This protocol is
// not connect, so there is no client to hang it on.
func DialWorkload(ctx context.Context, h host.Host, at libp2ppeer.ID, t specs.Address, port string) (net.Conn, error) {
	conn, err := gostream.Dial(ctx, h, at, StreamProtocol)
	if err != nil {
		return nil, fmt.Errorf("worker: dialling %s: %w", at, err)
	}

	if _, err := protodelim.MarshalTo(conn, &workerv2.Open{
		Target: &workerv2.Target{
			Project: t.Project, Environment: t.Environment,
			Scope: t.Scope, Kind: t.Kind, Name: t.Name,
		},
		Port: port,
	}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("worker: opening %s/%s: %w", t.Kind, t.Name, err)
	}

	var opened workerv2.Opened
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
