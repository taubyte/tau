package raft

import (
	"bufio"
	"context"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"google.golang.org/protobuf/encoding/protodelim"

	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// EngineProtocol is the stream a node's raft engine sends every group's messages on:
// one stream to each peer, whichever groups the two share.
const EngineProtocol protocol.ID = "/tau/raft/v2/engine"

const (
	// linkBacklog is how many frames wait for a slow peer before newer ones are
	// dropped. Raft retries what it does not hear back about, so a dropped frame
	// costs a round trip, never correctness.
	linkBacklog = 256

	// dialTimeout bounds opening a stream to a peer; a peer that cannot be reached
	// is reported unreachable and dialled again on the next frame.
	dialTimeout = 5 * time.Second

	// maxFrame is the largest frame read: a snapshot travels inline.
	maxFrame = 1 << 30
)

// receiver is what the wire hands frames to, and tells about a peer it could not
// reach or a snapshot it did or did not deliver.
type receiver interface {
	received(from peer.ID, frame *raftv2.Frame)
	unreachable(to peer.ID, frame *raftv2.Frame)
	sent(to peer.ID, frame *raftv2.Frame)
}

// engineWire is the engine's streams: one outgoing link per peer, each with its own
// goroutine so a slow peer holds up nobody else, and the handler reading what peers
// send.
type engineWire struct {
	host host.Host
	ctx  context.Context
	to   receiver

	mu      sync.Mutex
	links   map[peer.ID]*peerLink
	inbound map[network.Stream]struct{}
	closed  bool
	wg      sync.WaitGroup
}

func newEngineWire(ctx context.Context, h host.Host, to receiver) *engineWire {
	w := &engineWire{host: h, ctx: ctx, to: to, links: map[peer.ID]*peerLink{}, inbound: map[network.Stream]struct{}{}}
	h.SetStreamHandler(EngineProtocol, w.read)
	return w
}

// send queues frame for p. A full backlog drops it and says p is unreachable. It
// holds the lock through the hand-off, which never blocks, so close cannot close a
// link under it.
func (w *engineWire) send(p peer.ID, frame *raftv2.Frame) {
	w.mu.Lock()
	l, ok := w.links[p]
	if !ok {
		l = &peerLink{wire: w, to: p, out: make(chan *raftv2.Frame, linkBacklog)}
		w.links[p] = l
		w.wg.Add(1)
		go l.run()
	}
	select {
	case l.out <- frame:
		w.mu.Unlock()
	default:
		w.mu.Unlock()
		w.to.unreachable(p, frame)
	}
}

// close stops reading and writing; the links finish what they hold first.
//
// THE STREAMS IT READS ARE RESET, not left open: a peer writing to one would hear
// nothing wrong, never dial again, and talk to an engine that is gone while this
// host's next engine waits for it.
func (w *engineWire) close() {
	w.host.RemoveStreamHandler(EngineProtocol)
	w.mu.Lock()
	w.closed = true
	for s := range w.inbound {
		s.Reset()
	}
	w.inbound = nil
	for _, l := range w.links {
		close(l.out)
	}
	w.links = map[peer.ID]*peerLink{}
	w.mu.Unlock()
	w.wg.Wait()
}

func (w *engineWire) read(s network.Stream) {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		s.Reset()
		return
	}
	w.inbound[s] = struct{}{}
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		delete(w.inbound, s)
		w.mu.Unlock()
		s.Close()
	}()
	from := s.Conn().RemotePeer()
	r := bufio.NewReader(s)
	opts := protodelim.UnmarshalOptions{MaxSize: maxFrame}
	for {
		frame := &raftv2.Frame{}
		if err := opts.UnmarshalFrom(r, frame); err != nil {
			s.Reset()
			return
		}
		w.to.received(from, frame)
	}
}

// peerLink is the stream to one peer, opened on the first frame and again after a
// failure.
type peerLink struct {
	wire   *engineWire
	to     peer.ID
	out    chan *raftv2.Frame
	stream network.Stream
	writer *bufio.Writer
}

func (l *peerLink) run() {
	defer l.wire.wg.Done()
	defer l.drop()
	for frame := range l.out {
		if err := l.write(frame); err != nil {
			l.drop()
			l.wire.to.unreachable(l.to, frame)
			continue
		}
		l.wire.to.sent(l.to, frame)
	}
}

func (l *peerLink) write(frame *raftv2.Frame) error {
	if l.stream == nil {
		ctx, cancel := context.WithTimeout(l.wire.ctx, dialTimeout)
		s, err := l.wire.host.NewStream(network.WithAllowLimitedConn(ctx, string(EngineProtocol)), l.to, EngineProtocol)
		cancel()
		if err != nil {
			return err
		}
		l.stream, l.writer = s, bufio.NewWriter(s)
	}
	if _, err := protodelim.MarshalTo(l.writer, frame); err != nil {
		return err
	}
	return l.writer.Flush()
}

func (l *peerLink) drop() {
	if l.stream != nil {
		l.stream.Reset()
		l.stream, l.writer = nil, nil
	}
}
