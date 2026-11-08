package raft

import (
	"context"
	"fmt"
	"testing"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/stretchr/testify/require"
	pb "go.etcd.io/raft/v3/raftpb"
	"google.golang.org/protobuf/proto"

	taupeer "github.com/taubyte/tau/p2p/peer"
	raftv2 "github.com/taubyte/tau/pkg/proto/gen/tau/raft/v2"
)

// benchEnd hands every frame it receives to got, or, with back set, sends it back
// to its sender.
type benchEnd struct {
	got  chan *raftv2.Frame
	back transport
}

func (e *benchEnd) received(from peer.ID, frame *raftv2.Frame) {
	if e.back != nil {
		e.back.send(from, frame)
		return
	}
	e.got <- frame
}

func (*benchEnd) unreachable(peer.ID, *raftv2.Frame) {}
func (*benchEnd) sent(peer.ID, *raftv2.Frame)        {}

// benchFrame is a frame of n envelopes, each a raft message carrying size bytes of
// entry, or a heartbeat when size is zero.
func benchFrame(tb testing.TB, n, size int) *raftv2.Frame {
	msg := &pb.Message{
		Type: pb.MessageType_MsgHeartbeat.Enum(),
		To:   proto.Uint64(2), From: proto.Uint64(1), Term: proto.Uint64(7), Commit: proto.Uint64(1234),
	}
	if size > 0 {
		msg.Type = pb.MessageType_MsgApp.Enum()
		msg.Entries = []*pb.Entry{{Term: proto.Uint64(7), Index: proto.Uint64(1235), Data: make([]byte, size)}}
	}
	raw, err := proto.Marshal(msg)
	require.NoError(tb, err)
	f := &raftv2.Frame{}
	for i := range n {
		f.Envelopes = append(f.Envelopes, &raftv2.Envelope{Group: fmt.Sprintf("group-%d", i), Message: raw, Formation: make([]byte, formationSize)})
	}
	return f
}

// benchPair is two linked nodes, a's wire sending to b's.
func benchPair(b *testing.B, echo bool) (from transport, to peer.ID, got chan *raftv2.Frame) {
	ctx, cancel := context.WithCancel(context.Background())
	b.Cleanup(cancel)
	nodes := []taupeer.Node{taupeer.Mock(ctx), taupeer.Mock(ctx)}
	for _, n := range nodes {
		b.Cleanup(n.Close)
	}
	connectAllOf(b, nodes...)

	a := &benchEnd{got: make(chan *raftv2.Frame, linkBacklog)}
	far := &benchEnd{got: make(chan *raftv2.Frame, linkBacklog)}
	wa := newEngineWire(ctx, nodes[0].Host(), a)
	wb := newEngineWire(ctx, nodes[1].Host(), far)
	b.Cleanup(wa.close)
	b.Cleanup(wb.close)
	if echo {
		far.back = wb
		return wa, nodes[1].ID(), a.got
	}
	return wa, nodes[1].ID(), far.got
}

var benchShapes = []struct {
	name      string
	envelopes int
	size      int
}{
	{"heartbeats-1", 1, 0},
	{"heartbeats-10", 10, 0},
	{"heartbeats-100", 100, 0},
	{"append-64KiB", 1, 64 << 10},
}

// A FRAME THERE AND BACK: the round a heartbeat makes on the engine's wire.
func BenchmarkEngineWireRoundTrip(b *testing.B) {
	for _, shape := range benchShapes {
		b.Run(shape.name, func(b *testing.B) {
			wire, to, back := benchPair(b, true)
			frame := benchFrame(b, shape.envelopes, shape.size)
			wire.send(to, frame)
			<-back
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				wire.send(to, frame)
				<-back
			}
		})
	}
}

// FRAMES ONE WAY, sixty-four in flight: what a busy node's cycles put on a link.
func BenchmarkEngineWireThroughput(b *testing.B) {
	const window = 64
	for _, shape := range benchShapes {
		b.Run(shape.name, func(b *testing.B) {
			wire, to, got := benchPair(b, false)
			frame := benchFrame(b, shape.envelopes, shape.size)
			wire.send(to, frame)
			<-got
			b.ReportAllocs()
			b.ResetTimer()
			for sent := 0; sent < b.N; {
				n := min(window, b.N-sent)
				for range n {
					wire.send(to, frame)
				}
				for range n {
					<-got
				}
				sent += n
			}
		})
	}
}
